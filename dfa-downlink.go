package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/samofly/serial"
)

var ErrPrinterDeviceNotFound = errors.New("printer device is not found. May be it's turned off?")
var ErrNoDownlinkConnection = errors.New("no downlink connection to the device")
var ErrConnectionReset = errors.New("downlink connection was reset")

type Downlink interface {
	WriteAndWaitForOK(ctx context.Context, cmd string) error
	WaitForConnection(wait time.Duration) bool
	Connected() bool
}

// DFADownlink is empowered by Deterministic Finite Automata to track
// all states, requests and connections.
type DFADownlink struct {
	up           *Uplink
	baudRate     int
	reqCh        chan *DFAMsg
	conn         io.ReadWriteCloser
	pendingOKAck chan<- bool
	// These are pending writes which we have not yet processed at all.
	pendingWrites []*DFAMsg
}

func NewDFADownlink(up *Uplink, baudRate int) *DFADownlink {
	return &DFADownlink{up: up, baudRate: baudRate, reqCh: make(chan *DFAMsg)}
}

type State int

const (
	Terminated        = State(-1)
	Disconnected      = State(0)
	Connecting        = State(1)
	Connected         = State(2)
	Normal            = State(3)
	WaitingForOK      = State(4)
	WaitingForWritten = State(5)
)

type MsgType int

const (
	MsgConnected         = MsgType(1)
	MsgIsConnected       = MsgType(2)
	MsgDisconnected      = MsgType(3)
	MsgOK                = MsgType(4)
	MsgWriteAndWaitForOK = MsgType(5)
	MsgWritten           = MsgType(6)
	MsgResend            = MsgType(7)
	MsgSomeReply         = MsgType(8)
)

type DFAMsg struct {
	Type   MsgType
	Lineno int
	Cmd    string
	Err    error
	RespCh chan<- bool
}

func (dl *DFADownlink) Connected() bool {
	respCh := make(chan bool, 1)
	dl.reqCh <- &DFAMsg{Type: MsgIsConnected, RespCh: respCh}
	return <-respCh
}

func (dl *DFADownlink) WaitForConnection(wait time.Duration) bool {
	until := time.Now().Add(wait)
	for {
		if time.Now().After(until) {
			return false
		}
		if dl.Connected() {
			return true
		}
		time.Sleep(time.Second)
	}
}

func (dl *DFADownlink) WriteAndWaitForOK(ctx context.Context, cmd string) error {
	respCh := make(chan bool, 1)
	dl.reqCh <- &DFAMsg{Type: MsgWriteAndWaitForOK, Cmd: cmd, RespCh: respCh}
	select {
	case ack, ok := <-respCh:
		if ok && !ack {
			// If we have not got an positive ack, but still got something out of the channel,
			// it means that this firmware does not send acks at all. Impose an artificial
			// delay of 20ms.
			time.Sleep(20 * time.Millisecond)
		}
		if !ok {
			return errors.New("OK not received")
		}
		return nil
	case <-ctx.Done():
		return context.Canceled
	}
}

func (dl *DFADownlink) Run() error {
	st := Disconnected
	for {
		switch st {
		case Disconnected:
			st = dl.handleDisconnected()
		case Connecting:
			st = dl.handleConnecting()
		case Connected:
			st = dl.handleConnected()
		case Normal:
			st = dl.handleNormal()
		case WaitingForOK:
			st = dl.handleWaitingForOK()
		case WaitingForWritten:
			st = dl.handleWaitingForWritten()
		default:
			return fmt.Errorf("unknown state %v", st)
		}
	}
}

func (dl *DFADownlink) handleDisconnected() State {
	dl.up.logf("State: Disconnected")
	// We are disconnected. Our only choice is to try to connect to the device.
	// We do not accept any input in this node.
	go dl.connect()
	return Connecting
}

func (dl *DFADownlink) connect() {
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
		dl.conn = conn
		dl.reqCh <- &DFAMsg{Type: MsgConnected}
		return
	}
}

func (dl *DFADownlink) handleConnecting() State {
	dl.up.logf("State: Connecting")
	for msg := range dl.reqCh {
		switch msg.Type {
		case MsgConnected:
			// Yay! We are connected. Transferring to the normal state.
			return Connected
		case MsgIsConnected:
			// We are not connected.
			msg.RespCh <- false
		case MsgDisconnected:
			dl.up.Fatalf("handleConnecting: received MsgDisconnected. Inconceivable!")
		case MsgOK:
			dl.up.Fatalf("handleConnecting: received MsgOK. Inconceivable!")
		case MsgWriteAndWaitForOK:
			// We have received a request to send a command, but we are not connected.
			// This is a valid possibility, but we have to decline this request.
			dl.up.logf("handleConnecting: unable to write a command (%q), because we are not connected. May be the printer is turned off?", msg.Cmd)
			msg.RespCh <- false
		case MsgWritten:
			dl.up.Fatalf("handleConnecting: received MsgWritten. Inconceivable!")
		case MsgResend:
			dl.up.Fatalf("handleConnecting: received MsgResend. Inconceivable!")
		case MsgSomeReply:
			// Just ignore.
		default:
			dl.up.Fatalf("handleConnecting: unexpected message type: %v, full message: %+v", msg.Type, msg)
		}
	}
	// We can only arrive here, if reqCh is closed. At this time, DFADownlink does not support shutdown, so complain and kill the process.
	dl.up.Fatalf("handleConnecting: reqCh is closed")
	return Terminated
}

func (dl *DFADownlink) handleConnected() State {
	dl.up.logf("State: Connected")
	go dl.readFromDevice(dl.conn)
	return Normal
}

func (dl *DFADownlink) readFromDevice(conn io.ReadWriteCloser) {
	defer func() {
		// Most likely, the connection is already closed, but we make the best effort, if it is not.
		conn.Close()
		dl.reqCh <- &DFAMsg{Type: MsgDisconnected}
	}()
	in := bufio.NewScanner(conn)
	for in.Scan() {
		txt := strings.TrimSpace(in.Text())
		dl.up.logf("%s\n", txt)
		if txt == "ok" {
			// The firmware did not send us a lineno. Okay.
			dl.up.logf("Sending MsgOK without a lineno...")
			dl.reqCh <- &DFAMsg{Type: MsgOK}
			continue
		}
		if strings.HasPrefix(txt, "ok ") {
			lineno, err := strconv.ParseUint(txt[3:], 10, 64)
			if err != nil {
				dl.up.logf("Failed to parse a line number from an ok response %q: %v. Just ignoring the lineno.", txt, err)
				lineno = 0
			}
			dl.reqCh <- &DFAMsg{Type: MsgOK, Lineno: int(lineno)}
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
			dl.reqCh <- &DFAMsg{Type: MsgResend, Lineno: int(lineno)}
			continue
		}
		dl.reqCh <- &DFAMsg{Type: MsgSomeReply}
	}
	if err := in.Err(); err != nil {
		dl.up.logf("readFromDevice: %v", err)
	}
}

func (dl *DFADownlink) handleNormal() State {
	dl.up.logf("State: Normal")
	wr := func(msg *DFAMsg) State {
		if msg.RespCh == nil {
			dl.up.Fatalf("RespCh == nil in MsgWriteAndWaitForOK message. Inconceivable!")
		}
		dl.pendingOKAck = msg.RespCh
		go dl.write(dl.conn, msg.Cmd)
		return WaitingForOK
	}
	if len(dl.pendingWrites) > 0 {
		// We have some pending write requests. Take one.
		msg := dl.pendingWrites[0]
		dl.pendingWrites = dl.pendingWrites[1:]
		return wr(msg)
	}
	for msg := range dl.reqCh {
		switch msg.Type {
		case MsgConnected:
			dl.up.Fatalf("handleNormal: MsgConnected received. Inconceivable!")
		case MsgIsConnected:
			// We are connected.
			msg.RespCh <- true
		case MsgDisconnected:
			dl.up.logf("handleNormal: received MsgDisconnected")
			return Disconnected
		case MsgOK:
			dl.up.logf("handleNormal: received MsgOK. Could be a leftover since previous connection. Ignore (mildly dangerous)")
		case MsgWriteAndWaitForOK:
			// This is exactly the message we want to receive here.
			return wr(msg)
		case MsgWritten:
			dl.up.Fatalf("handleNormal: received MsgWritten. Inconceivable!")
		case MsgResend:
			// It is possible to receive MsgResend, if we screwed up something earlier. Or may be there was some glitch on the wire.
			// Currently, we don't yet support line numbers, so it's impossible to implement.
			dl.up.Fatalf("handleNormal: MsgResend is not implemented")
		case MsgSomeReply:
			// Just ignore.
		default:
			dl.up.Fatalf("handleNormal: unexpected message type: %v, full message: %+v", msg.Type, msg)
		}
	}
	dl.up.Fatalf("handleNormal: reqCh is closed")
	return Terminated
}

func (dl *DFADownlink) write(conn io.ReadWriteCloser, cmd string) {
	dl.up.logf("> %s", cmd)
	var err error
	defer func() {
		if err != nil {
			dl.up.logf("DFADownlink write error: %v", err)
		}
		dl.reqCh <- &DFAMsg{Type: MsgWritten, Err: err}
	}()
	if !strings.HasSuffix(cmd, "\n") {
		cmd += "\n"
	}
	_, err = dl.conn.Write([]byte(cmd))
	return
}

func (dl *DFADownlink) handleWaitingForOK() State {
	dl.up.logf("State: WaitingForOK")
	start := time.Now()
	gotOK := false
	gotWritten := false
	gotSomeReply := false
	for msg := range dl.reqCh {
		dur := time.Now().Sub(start)
		if dur > 10*time.Second && gotSomeReply && !gotOK {
			dl.up.logf("handleWaitingForOK: %v passed, some reply (!OK) received, consider the command is accepted", dur)
			gotOK = true
		}
		switch msg.Type {
		case MsgConnected:
			dl.up.Fatalf("handleWaitingForOK: MsgConnected received. Inconceivable!")
			return Terminated
		case MsgIsConnected:
			// We are connected.
			msg.RespCh <- true
		case MsgDisconnected:
			dl.up.logf("handleWaitingForOK: received MsgDisconnected")
			close(dl.pendingOKAck)
			dl.pendingOKAck = nil
			if gotWritten {
				return Disconnected
			} else {
				// We need to wait until our write is complete (most likely, as a failed one)
				return WaitingForWritten
			}
		case MsgOK:
			if gotOK {
				dl.up.logf("handleWaitingForOK: got duplicate OK. Mildly dangerous. Ignoring.")
				continue
			}
			gotOK = true
			if gotOK && gotWritten {
				dl.up.logf("handleWaitingForOK: gotOK and we had gotWritten == true. Sending ack...")
				dl.pendingOKAck <- true
				dl.pendingOKAck = nil
				dl.up.logf("handleWaitingForOK: ack sent. Transferring to Normal state")
				return Normal
			}
			dl.up.logf("handleWaitingForOK: got OK, now waiting for MsgWritten.")
		case MsgWriteAndWaitForOK:
			// It's expected that new commands could arrive while we wait for OK. Adding them to the |pendingWrites| queue.
			dl.pendingWrites = append(dl.pendingWrites, msg)
			dl.up.logf("Added command %q to the queue. Current queue length: %d", msg.Cmd, len(dl.pendingWrites))
			continue
		case MsgWritten:
			if gotWritten {
				dl.up.Fatalf("handleWaitingForOK: got duplicate MsgWritten. Inconceivable!")
				return Terminated
			}
			gotWritten = true
			if gotOK && gotWritten {
				dl.pendingOKAck <- true
				dl.pendingOKAck = nil
				return Normal
			}
			dl.up.logf("handleWaitingForOK: got MsgWritten, now waiting for OK.")
		case MsgResend:
			// It is possible to receive MsgResend, if we screwed up something earlier. Or may be there was some glitch on the wire.
			// Currently, we don't yet support line numbers, so it's impossible to implement.
			dl.up.Fatalf("handleWaitingForOK: MsgResend is not implemented")
		case MsgSomeReply:
			gotSomeReply = true
		default:
			dl.up.Fatalf("handleWaitingForOK: unexpected message type: %v, full message: %+v", msg.Type, msg)
		}
	}
	dl.up.Fatalf("handleWaitingForOK: reqCh is closed")
	return Terminated
}

func (dl *DFADownlink) handleWaitingForWritten() State {
	// We arrive to this state, when Disconnected was received while WaitingForOK. We need to wait until the write is completed
	// before transferring to the Disconnected state to maintain the invariant that MsgWritten is only expected during WaitingForOK or WaitingForWritten.
	dl.up.logf("State: WaitingForWritten")
	for msg := range dl.reqCh {
		switch msg.Type {
		case MsgConnected:
			dl.up.Fatalf("handleWaitingForWritten: MsgConnected received. Inconceivable!")
			return Terminated
		case MsgIsConnected:
			// We are not connected; we are transferring to the disconnected state (although, not there yet)
			msg.RespCh <- false
		case MsgDisconnected:
			dl.up.Fatalf("handleWaitingForWritten: MsgDisconnected received. Inconceivable!")
		case MsgOK:
			dl.up.Fatalf("handleWaitingForWritten: MsgOK received. Inconceivable!")
		case MsgWriteAndWaitForOK:
			dl.up.logf("handleWaitingForWritten: unable to write a command (%q), because we are not connected. May be the printer was just turned off?", msg.Cmd)
			msg.RespCh <- false
		case MsgWritten:
			return Disconnected
		case MsgResend:
			dl.up.Fatalf("handleWaitingForWritten: MsgResend received. Inconceivable!")
		case MsgSomeReply:
			// Just ignore
		default:
			dl.up.Fatalf("handleWaitingForWritten: unexpected message type: %v, full message: %+v", msg.Type, msg)
		}
	}
	dl.up.Fatalf("handleWaitingForWritten: reqCh is closed")
	return Terminated
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
