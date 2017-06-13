package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robodone/robosla-agent/gcode"
	"github.com/samofly/serial"
)

var ErrPrinterDeviceNotFound = errors.New("printer device is not found. May be it's turned off?")
var ErrNoDownlinkConnection = errors.New("no downlink connection to the device")
var ErrConnectionReset = errors.New("downlink connection was reset")

const (
	OKType             = "OK"
	NeverWaitForOKType = "NeverWaitForOK"
	WaitForOKType      = "WaitForOK"
	SendType           = "Send"
	ResendType         = "Resend"
	ResetType          = "Reset"
)

type Downlink struct {
	up       *Uplink
	baudRate int
	mu       sync.Mutex
	conn     io.ReadWriteCloser
	closed   sync.WaitGroup
	reqCh    chan *Request
	lineno   int
}

func NewDownlink(up *Uplink, baudRate int) *Downlink {
	return &Downlink{up: up, baudRate: baudRate, reqCh: make(chan *Request)}
}

func (dl *Downlink) getConn() io.ReadWriteCloser {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	return dl.conn
}

func (dl *Downlink) Connected() bool {
	return dl.getConn() != nil
}

func (dl *Downlink) Run() error {
	go dl.handleTraffic()
	var lastAttempt time.Time
	for {
		dl.up.WaitForConnection()
		ttyDev, err := findTTYDev()
		if err != nil {
			now := time.Now()
			// Avoid log spam
			if now.Sub(lastAttempt) > 30*time.Minute {
				lastAttempt = now
				dl.up.logf("Scanning serial devices failed: %s", err)
			}
			// Avoid immediate reconnects
			time.Sleep(5 * time.Second)
			continue
		}
		conn, err := serial.Open(ttyDev, dl.baudRate)
		if err != nil {
			dl.up.logf("Could not open serial port %s at %d bps. Error: %v", ttyDev, dl.baudRate, err)
			// Avoid immediate reconnects.
			time.Sleep(10 * time.Second)
			continue
		}
		dl.up.logf("Opened %s at %d bps.", ttyDev, dl.baudRate)
		// TODO(krasin): don't send M105 to the device; it's only needed for debug purposes.
		fmt.Fprintf(conn, "M110 N0\n")
		fmt.Fprintf(conn, gcode.AddLineAndHash(1, "M105\n"))
		dl.mu.Lock()
		dl.conn = conn
		dl.lineno = 0
		dl.closed.Add(1)
		dl.mu.Unlock()
		go dl.readFromDevice(conn)

		// Wait until it's closed. Then we will try to reconnect.
		dl.closed.Wait()
	}
}

func (dl *Downlink) WaitForConnection() {
	for {
		if conn := dl.getConn(); conn != nil {
			return
		}
		time.Sleep(time.Second)
	}
}

func (dl *Downlink) writeInternal(cmd string) (err error) {
	dl.up.logf("> %s", cmd)
	defer func() {
		if err != nil {
			dl.up.logf("Downlink write error: %v", err)
		}
	}()
	_, err = dl.conn.Write([]byte(cmd))
	if err != nil {
		dl.closed.Done()
		dl.conn = nil
	}
	return
}

func (dl *Downlink) takeLineno() (int, error) {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if dl.conn == nil {
		return 0, ErrNoDownlinkConnection
	}
	dl.lineno++
	return dl.lineno, nil
}

func (dl *Downlink) addLinenoAndWrite(cmd string) (lineno int, err error) {
	lineno, err = dl.takeLineno()
	if err != nil {
		return
	}
	cmd = gcode.AddLineAndHash(lineno, cmd)
	dl.reqCh <- &Request{Type: SendType, Lineno: lineno, Str: cmd}
	return lineno, nil
}

func (dl *Downlink) WriteAndWaitForOK(cmd string) error {
	lineno, err := dl.addLinenoAndWrite(cmd)
	if err != nil {
		return err
	}
	if !waitForOK(dl.reqCh, lineno) {
		return ErrConnectionReset
	}
	return nil
}

type Request struct {
	Type   string
	Lineno int
	Str    string
	AckCh  *chan bool
}

func (dl *Downlink) handleTraffic() {
	oks := make(map[int]bool)
	waits := make(map[int]*chan bool)
	hist := make(map[int]string)
	resends := make(map[int]bool)
	lastWriteWasAResend := false
	lastResendLineno := 0
	neverWaitForOK := false

	send := func(lineno int, cmd string, isResend bool) bool {
		// Check if we had already resent the command. We only try to resend it once.
		if isResend && resends[lineno] {
			dl.up.logf("Line %d was already resent. Ignoring the resend request", lineno)
			return true
		}

		if err := dl.writeInternal(fmt.Sprintf("%s\n", cmd)); err != nil {
			dl.up.logf("Failed to write to serial port: %v", err)
			return false
		}
		if isResend {
			lastWriteWasAResend = true
			lastResendLineno = lineno
		} else {
			lastWriteWasAResend = false
		}
		return true
	}
	for req := range dl.reqCh {
		switch req.Type {
		case OKType:
			oks[req.Lineno] = true
			delete(hist, req.Lineno)
			if ch, ok := waits[req.Lineno]; ok {
				*ch <- true
				delete(waits, req.Lineno)
			}
		case NeverWaitForOKType:
			neverWaitForOK = true
		case WaitForOKType:
			if neverWaitForOK {
				*req.AckCh <- false
				continue
			}
			if oks[req.Lineno] {
				*req.AckCh <- true
				continue
			}
			if _, ok := waits[req.Lineno]; ok {
				dl.up.logf("Incredible error: double wait for line %d", req.Lineno)
				continue
			}
			waits[req.Lineno] = req.AckCh
		case SendType:
			if lastWriteWasAResend && lastResendLineno < req.Lineno-1 {
				// We have a backlog of commands we need to resend.
				for lineno := lastResendLineno + 1; lineno < req.Lineno; lineno++ {
					send(lineno, hist[lineno], true)
				}
			}
			if !send(req.Lineno, req.Str, false) {
				continue
			}
			hist[req.Lineno] = req.Str
		case ResendType:
			// Tough case: we are requested to resend a line.
			// Either we have hit the input buffer too much, or had
			// a geniune transmission error.

			// First, we need to check, if we even remember about this line.
			cmd, ok := hist[req.Lineno]
			if !ok {
				dl.up.logf("Resend for line %d is requested. Either we had never send it, "+
					"or an OK was already received. Sending M105 to keep it calm.", req.Lineno)

				// Lying that it's not a resend, because the request is really just bogus.
				send(req.Lineno, "M105", false /*isResend*/)
				continue
			}
			// Resending the command.
			send(req.Lineno, cmd, true /*isResend*/)
		case ResetType:
			// We have been reconnected to the printer. All line numbers are reset.
			// First of all, we need to close all response channels to free all goroutines
			// waiting for confirmations.
			for _, waitCh := range waits {
				close(*waitCh)
			}
			// Initializing maps again
			oks = make(map[int]bool)
			waits = make(map[int]*chan bool)
			hist = make(map[int]string)
			resends = make(map[int]bool)
			lastWriteWasAResend = false
			lastResendLineno = 0
		default:
			dl.up.logf("Unknown request type: %s", req.Type)
		}
	}
}

func (dl *Downlink) readFromDevice(conn io.Reader) {
	defer func() {
		dl.reqCh <- &Request{Type: ResetType}
	}()
	in := bufio.NewScanner(conn)
	for in.Scan() {
		txt := strings.TrimSpace(in.Text())
		dl.up.logf("%s\n", txt)
		if strings.Index(txt, "echo:Marlin 1.1") >= 0 {
			// At least in the case of uARM Swift Pro, ok responses are not set.
			// So, we notify the handleTraffic goroutine that waitForOK must return immediately.
			// This likely means inability to send gcode files. This is to be fixed later.
			// For now, just unblock manual commands.
			dl.reqCh <- &Request{Type: NeverWaitForOKType}
		}
		if strings.HasPrefix(txt, "ok ") {
			lineno, err := strconv.ParseUint(txt[3:], 10, 64)
			if err != nil {
				dl.up.logf("Failed to parse a line number from an ok response %q: %v", txt, err)
				continue
			}
			dl.reqCh <- &Request{Type: OKType, Lineno: int(lineno)}
			continue
		}
		// Resend:17206
		if strings.HasPrefix(txt, "Resend:") {
			rest := strings.TrimSpace(txt[len("Resend:"):])
			lineno, err := strconv.ParseUint(rest, 10, 64)
			if err != nil {
				dl.up.logf("Failed to parse a resend response %q: %v", txt, err)
				continue
			}
			dl.reqCh <- &Request{Type: ResendType, Lineno: int(lineno)}
			continue
		}
	}
	if err := in.Err(); err != nil {
		dl.up.logf("readFromDevice: %v", err)
	}
}

func waitForOK(reqCh chan *Request, lineno int) bool {
	ackCh := make(chan bool)
	reqCh <- &Request{Type: WaitForOKType, Lineno: lineno, AckCh: &ackCh}
	ack, ok := <-ackCh
	if ok && !ack {
		// If we have not got an positive ack, but still got something out of the channel,
		// it means that this firmware does not send acks at all. Impose an artificial
		// delay of 20ms.
		time.Sleep(20 * time.Millisecond)
	}
	return ok
}

// Find tty dev for the printer. As we work in a relatively stable environment,
// it's going to be either /dev/ttyACM? or /dev/ttyUSB?. The numbers will also likely be low, like 0 or 1.
// For now, just have a short list and go through it.
func findTTYDev() (string, error) {
	for _, ttyDev := range []string{
		"/dev/ttyACM0",
		"/dev/ttyACM1",
		"/dev/ttyACM2",
		"/dev/ttyUSB0",
		"/dev/ttyUSB1",
		"/dev/ttyUSB2",
	} {
		_, err := os.Stat(ttyDev)
		if err == nil {
			// We have found the device we want.
			return ttyDev, nil
		}
	}
	return "", ErrPrinterDeviceNotFound
}
