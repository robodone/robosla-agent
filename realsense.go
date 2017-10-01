package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type RealSenseSnapshotter struct {
	up         *Uplink
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdoutScan *bufio.Scanner
}

type RealSenseTrainPackParams struct {
	PackID    string  `json:"packID"`
	GraspID   string  `json:"graspID"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Z         float64 `json:"z"`
	Roll      float64 `json:"roll"`
	Pitch     float64 `json:"pitch"`
	Yaw       float64 `json:"yaw"`
	NumFrames int     `json:"numFrames"`
}

func (rss *RealSenseSnapshotter) TakeSnapshot(ctx context.Context, prefix string, numFrames int) error {
	if rss.cmd == nil {
		cmd := exec.Command("/opt/robodone/realsense-snapshot")
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdin pipe: %v", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdout pipe: %v", err)
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("failed to create stderr pipe: %v", err)
		}
		go func(stderr io.ReadCloser) {
			s := bufio.NewScanner(stderr)
			for s.Scan() {
				line := strings.TrimSpace(s.Text())
				rss.up.logf("realsense-snapshot: %s", line)
			}
			if s.Err() != nil {
				rss.up.logf("failed to read from realsense-snapshot stderr: %v", err)
				return
			}
		}(stderr)
		if err = cmd.Start(); err != nil {
			return fmt.Errorf("failed to start realsense-snapshot: %v", err)
		}
		rss.cmd = cmd
		rss.stdin = stdin
		rss.stdoutScan = bufio.NewScanner(stdout)
	}
	for i := 0; i < numFrames; i++ {
		if _, err := fmt.Fprintf(rss.stdin, "%s%02d-\n", prefix, i); err != nil {
			return fmt.Errorf("failed to write to realsense-snapshot stdin: %v", err)
		}
		// TODO(krasin): obey context cancellation here.
		if !rss.stdoutScan.Scan() {
			err := rss.stdoutScan.Err()
			if err != nil {
				return fmt.Errorf("failed to read from realsense-snapshot stdout: %v", err)
			}
			return errors.New("realsense-snapshot is probably dead, as reading from stdout reached EOF")
		}
		reply := strings.TrimSpace(rss.stdoutScan.Text())
		if reply != "OK" {
			return fmt.Errorf("unexpected reply from realsense-snapshot: %v", reply)
		}
	}
	return nil
}
