package main

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

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
}

func NewDownlink(up *Uplink, ttyDev string, baudRate int) *Downlink {
	return &Downlink{up: up, ttyDev: ttyDev, baudRate: baudRate}
}

func (dl *Downlink) getConn() io.ReadWriteCloser {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	return dl.conn
}

func (dl *Downlink) Run() error {
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
		dl.closed.Add(1)
		dl.mu.Unlock()

		// Wait until it's closed. Then we will try to reconnect.
		dl.closed.Wait()
	}
}

func (dl *Downlink) WaitForConnection() io.ReadWriteCloser {
	for {
		if conn := dl.getConn(); conn != nil {
			return conn
		}
		time.Sleep(time.Second)
	}
}

func (dl *Downlink) Write(cmd string) (err error) {
	dl.up.logf("> %s", cmd)
	defer func() {
		if err != nil {
			dl.up.logf("Downlink write error: %v", err)
		}
	}()
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if dl.conn == nil {
		return ErrNoDownlinkConnection
	}
	_, err = dl.conn.Write([]byte(cmd))
	if err != nil {
		dl.closed.Done()
		dl.conn = nil
	}
	return
}
