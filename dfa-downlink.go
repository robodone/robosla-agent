package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/samofly/serial"
)

// DFADownlink is empowered by Deterministic Finite Automata to track
// all states, requests and connections.
type DFADownlink struct {
	up       *Uplink
	baudRate int
	reqCh    chan *DFAMsg
	conn     io.ReadWriteCloser
}

func NewDFADownlink(up *Uplink, baudRate int) *DFADownlink {
	return &DFADownlink{up: up, baudRate: baudRate, reqCh: make(chan *DFAMsg)}
}

type State int

const (
	Terminated   = State(-1)
	Disconnected = State(0)
	Connecting   = State(1)
	Connected    = State(2)
	Normal       = State(3)
	WaitingForOK = State(4)
)

type MsgType int

const (
	MsgConnected    = MsgType(1)
	MsgIsConnected  = MsgType(2)
	MsgDisconnected = MsgType(3)
	MsgOK           = MsgType(4)
)

type DFAMsg struct {
	Type   MsgType
	Lineno int
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
		default:
			return fmt.Errorf("unknown state %v", st)
		}
	}
	return nil
}

func (dl *DFADownlink) handleDisconnected() State {
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
		default:
			dl.up.Fatalf("handleConnecting: unexpected message type: %v, full message: %+v", msg.Type, msg)
		}
	}
	// We can only arrive here, if reqCh is closed. At this time, DFADownlink does not support shutdown, so complain and kill the process.
	dl.up.Fatalf("handleConnecting: reqCh is closed")
	return Terminated
}

func (dl *DFADownlink) handleConnected() State {
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
			dl.reqCh <- &DFAMsg{Type: MsgOK}
			continue
		}
		if strings.HasPrefix(txt, "ok ") {
			lineno, err := strconv.ParseUint(txt[3:], 10, 64)
			if err != nil {
				dl.up.logf("Failed to parse a line number from an ok response %q: %v", txt, err)
				continue
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
	}
	if err := in.Err(); err != nil {
		dl.up.logf("readFromDevice: %v", err)
	}
}

func (dl *DFADownlink) handleNormal() State {
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
		default:
			dl.up.Fatalf("handleNormal: unexpected message type: %v, full message: %+v", msg.Type, msg)
		}
	}
	dl.up.Fatalf("handleNormal: reqCh is closed")
	return Terminated
}
