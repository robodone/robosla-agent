package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"time"

	"github.com/robodone/robosla-agent/pkg/ur"
)

type UR3Downlink struct {
	up                   *Uplink
	host                 string
	port                 int
	rtdePort             int
	onMovingStateChanged func(state string)
	reqCh                chan *DFAMsg
	conn                 io.ReadWriteCloser
	rtdeConn             net.Conn
	pendingOKAck         chan<- bool
	// These are pending writes which we have not yet processed at all.
	pendingWrites []*DFAMsg
}

func NewUR3Downlink(up *Uplink, host string, port, rtdePort int, onMovingStateChanged func(state string)) *UR3Downlink {
	if onMovingStateChanged == nil {
		onMovingStateChanged = func(state string) {}
	}
	return &UR3Downlink{
		up:                   up,
		host:                 host,
		port:                 port,
		rtdePort:             rtdePort,
		onMovingStateChanged: onMovingStateChanged,
		reqCh:                make(chan *DFAMsg),
	}
}

func (dl *UR3Downlink) Connected() bool {
	respCh := make(chan bool, 1)
	dl.reqCh <- &DFAMsg{Type: MsgIsConnected, RespCh: respCh}
	return <-respCh
}

func (dl *UR3Downlink) WaitForConnection(wait time.Duration) bool {
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

func (dl *UR3Downlink) WriteAndWaitForOK(ctx context.Context, cmd string) error {
	respCh := make(chan bool, 1)
	dl.reqCh <- &DFAMsg{Type: MsgWriteAndWaitForOK, Cmd: cmd, RespCh: respCh}
	select {
	case _, ok := <-respCh:
		if !ok {
			return errors.New("OK not received")
		}
		return nil
	case <-ctx.Done():
		return context.Canceled
	}
}

func (dl *UR3Downlink) Run() (err error) {
	defer func() {
		dl.up.logf("UR3Downlink.Run failed, err: %v", err)
	}()
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

func (dl *UR3Downlink) handleDisconnected() State {
	dl.up.logf("UR3Downlink: Disconnected")
	// We are disconnected. Our only choice is to try to connect to the robot.
	// We do not accept any input in this node.
	go dl.connect()
	return Connecting
}

func (dl *UR3Downlink) connect() {
	if dl.conn != nil {
		dl.conn.Close()
		dl.conn = nil
	}
	if dl.rtdeConn != nil {
		dl.rtdeConn.Close()
		dl.rtdeConn = nil
	}

	first := true
	for {
		if !first {
			// Avoid immediate reconnects.
			time.Sleep(10 * time.Second)
		}
		first = false

		dl.up.WaitForConnection()
		conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", dl.host, dl.port))
		if err != nil {
			dl.up.logf("Could not open URScript connection to UR3 at %s:%d. Error: %v", dl.host, dl.port, err)
			continue
		}
		dl.up.logf("Opened URScript robot connection to %s:%d.", dl.host, dl.port)
		rtdeConn, err := ur.ConnectRTDE(dl.host, dl.rtdePort, "actual_TCP_speed")
		if err != nil {
			conn.Close()
			dl.up.logf("Could not open RTDE connection to UR3 at %s:%d. Error: %v", dl.host, dl.rtdePort, err)
			continue
		}
		dl.conn = conn
		dl.rtdeConn = rtdeConn
		go dl.readFromRTDE(dl.rtdeConn)
		dl.reqCh <- &DFAMsg{Type: MsgConnected}
		return
	}
}

func l2(vec []float64) float64 {
	var sum2 float64
	for _, v := range vec {
		sum2 += v * v
	}
	return math.Sqrt(sum2)
}

func (dl *UR3Downlink) readFromRTDE(conn net.Conn) {
	prevState := "unknown"
	for {
		// Read incoming packages, decode them and generate events we are interested in.
		typ, body, err := ur.ReceiveRTDEPacket(conn)
		if err != nil {
			// TODO(krasin): make sure we see this disconnect and act on it.
			dl.up.logf("UR3Downlink.readFromRTDE, read error: %v", err)
			return
		}
		if typ == ur.RTDE_DATA_PACKAGE {
			vec := ur.ParseVector6D(body[1:])
			linSpeed := l2(vec[:3])
			rotSpeed := l2(vec[3:])
			if linSpeed < 2E-5 {
				linSpeed = 0
			}
			if rotSpeed < 5E-4 {
				rotSpeed = 0
			}
			var state string
			if linSpeed == 0 && rotSpeed == 0 {
				state = "idle"
			} else {
				state = "moving"
			}
			if state != prevState {
				dl.onMovingStateChanged(state)
			}
			prevState = state
		}
	}
}

func (dl *UR3Downlink) handleConnecting() State {
	dl.up.logf("UR3Downlink: Connecting")
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
		//case MsgOK:
		//	dl.up.Fatalf("handleConnecting: received MsgOK. Inconceivable!")
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

func (dl *UR3Downlink) handleConnected() State {
	dl.up.logf("UR3Downlink: Connected")
	go dl.readFromDevice(dl.conn)
	return Normal
}

func (dl *UR3Downlink) readFromDevice(conn io.ReadWriteCloser) {
	defer func() {
		// Most likely, the connection is already closed, but we make the best effort, if it is not.
		conn.Close()
		dl.up.logf("UR3Downlink.readFromDevice, sending MsgDisconnected ...")
		dl.reqCh <- &DFAMsg{Type: MsgDisconnected}
		dl.up.logf("UR3Downlink.readFromDevice, MsgDisconnected sent")
	}()
	buf := make([]byte, 1024)
	// Just read everything and immediately discard it.
	// Later, we will parse these responses, as they might indicate that the robot has reached
	// an error condition and that all subsequent commands will be ignored.
	for {
		_, err := conn.Read(buf)
		if err != nil {
			dl.up.logf("UR3Downlink.readFromDevice, read error: %v", err)
			return
		}
	}
}

func (dl *UR3Downlink) handleNormal() State {
	dl.up.logf("UR3Downlink: Normal")
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

func (dl *UR3Downlink) write(conn io.ReadWriteCloser, cmd string) {
	dl.up.logf("> %s", cmd)
	var err error
	defer func() {
		if err != nil {
			dl.up.logf("UR3Downlink write error: %v", err)
		}
		dl.reqCh <- &DFAMsg{Type: MsgWritten, Err: err}
	}()
	if !strings.HasSuffix(cmd, "\n") {
		cmd += "\n"
	}
	_, err = dl.conn.Write([]byte(cmd))
	return
}

func (dl *UR3Downlink) handleWaitingForOK() State {
	dl.up.logf("UR3Downlink: WaitingForOK")
	for msg := range dl.reqCh {
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
			// We need to wait until our write is complete (most likely, as a failed one)
			return WaitingForWritten
		case MsgWriteAndWaitForOK:
			// It's expected that new commands could arrive while we wait for OK. Adding them to the |pendingWrites| queue.
			dl.pendingWrites = append(dl.pendingWrites, msg)
			dl.up.logf("Added command %q to the queue. Current queue length: %d", msg.Cmd, len(dl.pendingWrites))
			continue
		case MsgWritten:
			dl.pendingOKAck <- true
			dl.pendingOKAck = nil
			return Normal
		//case MsgResend:
		//	// It is possible to receive MsgResend, if we screwed up something earlier. Or may be there was some glitch on the wire.
		//	// Currently, we don't yet support line numbers, so it's impossible to implement.
		//	dl.up.Fatalf("handleWaitingForOK: MsgResend is not implemented")
		//case MsgSomeReply:
		//	gotSomeReply = true
		default:
			dl.up.Fatalf("handleWaitingForOK: unexpected message type: %v, full message: %+v", msg.Type, msg)
		}
	}
	dl.up.Fatalf("handleWaitingForOK: reqCh is closed")
	return Terminated
}

func (dl *UR3Downlink) handleWaitingForWritten() State {
	// We arrive to this state, when Disconnected was received while WaitingForOK. We need to wait until the write is completed
	// before transferring to the Disconnected state to maintain the invariant that MsgWritten is only expected during WaitingForOK or WaitingForWritten.
	dl.up.logf("UR3Downlink: WaitingForWritten")
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
