package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type VirtualDownlink struct {
	up      *Uplink
	speedup float64
}

func NewVirtualDownlink(up *Uplink, speedup float64) *VirtualDownlink {
	return &VirtualDownlink{up: up, speedup: speedup}
}

func (dl *VirtualDownlink) Connected() bool { return true }

func (dl *VirtualDownlink) WaitForConnection(wait time.Duration) bool { return true }

func (dl *VirtualDownlink) WriteAndWaitForOK(ctx context.Context, line string) error {
	dl.up.logf(">%s", line)
	cmd, err := parseGcodeCommand("" /*baseDir*/, line)
	if err != nil {
		return fmt.Errorf("failed to parse gcode %q: %v", line, err)
	}
	if cmd.Type != "G" || cmd.Idx != 4 {
		// If it's a non-delay command, return immediately
		return nil
	}
	delay, ok := cmd.Dict['P']
	if !ok {
		return errors.New("delay is not specified in G4")
	}
	delay /= dl.speedup
	time.Sleep(time.Duration(delay) * time.Millisecond)
	return nil
}
