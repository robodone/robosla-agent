package main

import (
	"fmt"
	"io"
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
	Normal       = State(2)
	WaitingForOK = State(3)
)

type MsgType int

const (
	MsgConnected = MsgType(1)
)

type DFAMsg struct {
	Type MsgType
}

func (dl *DFADownlink) Run() error {
	st := Disconnected
	for {
		switch st {
		case Disconnected:
			st = dl.handleDisconnected()
		case Connecting:
			st = dl.handleConnecting()
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
			return Normal
		default:
			dl.up.Fatalf("handleConnecting: unexpected message type: %v, full message: %+v", msg.Type, msg)
		}
	}
	// We can only arrive here, if reqCh is closed. At this time, DFADownlink does not support shutdown, so complain and kill the process.
	dl.up.Fatalf("handleConnecting: reqCh is closed")
	return Terminated
}
