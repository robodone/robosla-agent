package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robodone/robosla-agent/gcode"
	"github.com/samofly/serial"
)

var ErrNoDownlinkConnection = errors.New("No downlink connection to the device")

type Downlink struct {
	up       *Uplink
	ttyDev   string
	baudRate int
	mu       sync.Mutex
	conn     io.ReadWriteCloser
	closed   sync.WaitGroup
	reqCh    chan *Request
	lineno   int
}

func NewDownlink(up *Uplink, ttyDev string, baudRate int) *Downlink {
	return &Downlink{up: up, ttyDev: ttyDev, baudRate: baudRate, reqCh: make(chan *Request)}
}

func (dl *Downlink) getConn() io.ReadWriteCloser {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	return dl.conn
}

func (dl *Downlink) Run() error {
	go dl.handleTraffic()
	for {
		dl.up.WaitForConnection()
		conn, err := serial.Open(dl.ttyDev, dl.baudRate)
		if err != nil {
			dl.up.logf("Could not open serial port %s at %d bps. Error: %v", dl.ttyDev, dl.baudRate, err)
			// Avoid immediate reconnects.
			time.Sleep(10 * time.Second)
			continue
		}
		dl.up.logf("Opened %s at %d bps.", dl.ttyDev, dl.baudRate)
		// TODO(krasin): don't send M105 to the device; it's only needed for debug purposes.
		fmt.Fprintf(conn, "M105\n")
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

func (dl *Downlink) addLinenoAndWrite(cmd string) (lineno int, err error) {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if dl.conn == nil {
		return 0, ErrNoDownlinkConnection
	}
	dl.lineno++
	cmd = gcode.AddLineAndHash(dl.lineno, cmd)
	if err = dl.writeInternal(fmt.Sprintf("%s\n", cmd)); err != nil {
		return 0, err
	}
	return dl.lineno, nil
}

func (dl *Downlink) WriteAndWaitForOK(cmd string) error {
	lineno, err := dl.addLinenoAndWrite(cmd)
	if err != nil {
		return err
	}
	waitForOK(dl.reqCh, lineno)
	return nil
}

type Request struct {
	Type   string
	LineNo int
	AckCh  *chan bool
}

func (dl *Downlink) handleTraffic() {
	oks := make(map[int]bool)
	waits := make(map[int]*chan bool)
	for req := range dl.reqCh {
		switch req.Type {
		case "OK":
			oks[req.LineNo] = true
			if ch, ok := waits[req.LineNo]; ok {
				*ch <- true
				delete(waits, req.LineNo)
			}
		case "WaitForOK":
			if oks[req.LineNo] {
				*req.AckCh <- true
				continue
			}
			if _, ok := waits[req.LineNo]; ok {
				failf("Double wait for line %d", req.LineNo)
			}
			waits[req.LineNo] = req.AckCh

		default:
			failf("Unknown request type: %s", req.Type)
		}
	}
}

func (dl *Downlink) readFromDevice(conn io.Reader) {
	in := bufio.NewScanner(conn)
	for in.Scan() {
		txt := strings.TrimSpace(in.Text())
		dl.up.logf("%s\n", txt)
		if strings.HasPrefix(txt, "ok ") {
			lineno, err := strconv.ParseUint(txt[3:], 10, 64)
			if err != nil {
				dl.up.logf("Failed to parse a line number from an ok response %q: %v", txt, err)
				continue
			}
			dl.reqCh <- &Request{Type: "OK", LineNo: int(lineno)}
		}
	}
	if err := in.Err(); err != nil {
		dl.up.logf("readFromDevice: %v", err)
	}
}

func waitForOK(reqCh chan *Request, lineno int) {
	ackCh := make(chan bool)
	reqCh <- &Request{Type: "WaitForOK", LineNo: lineno, AckCh: &ackCh}
	<-ackCh
}
