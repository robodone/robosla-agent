package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var raspistillOutFname = "/tmp/robosla-raspistill.jpg"

type RaspistillSnapshotter struct {
	mu  sync.Mutex
	up  *Uplink
	cmd *exec.Cmd
}

func (rss *RaspistillSnapshotter) TakeSnapshot(ctx context.Context, prefix string, numFrames int) error {
	if numFrames != 1 {
		return fmt.Errorf("raspistill snapshot does not support taking multiple frames, but %d frames were requested", numFrames)
	}
	rss.mu.Lock()
	defer rss.mu.Unlock()

	if rss.cmd == nil {
		cmd := exec.Command("/usr/bin/raspistill", "-s",
			"--nopreview", "--exposure", "sports", "-t", "1",
			"-w", "640", "-h", "480",
			"-o", raspistillOutFname)
		//if err := cmd.Start(); err != nil {
		//	return fmt.Errorf("failed to start raspistill: %v", err)
		//}
		rss.cmd = cmd
		rss.up.logf("About to start raspistill with the command: %v", cmd)
		go func() {
			data, err := cmd.CombinedOutput()
			rss.up.logf("raspistill exited, stdout/stderr: %v, err: %v", string(data), err)
		}()
		// Give it some time to start up.
		time.Sleep(2 * time.Second)
		rss.up.logf("2 seconds since the start. Cmd: %v", cmd)
	}
	if err := os.RemoveAll(raspistillOutFname); err != nil {
		return fmt.Errorf("can't delete stale raspistill output %s: %v", raspistillOutFname, err)
	}

	fname := fmt.Sprintf("%s%02d-camera0.jpg", prefix, 0)
	// We need to send SIGUSR1 to raspistill, which will create a file on the disk.
	if err := rss.cmd.Process.Signal(syscall.SIGUSR1); err != nil {
		return fmt.Errorf("failed to send SIGUSR1 to raspistill: %v", err)
	}
	// Wait for the file to appear.
	start := time.Now()
	for i := 0; i < 200; i++ {
		_, err := os.Stat(raspistillOutFname)
		if err == nil {
			// The file has been created. Great!
			break
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("can't stat %s: %v", raspistillOutFname, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	waited := time.Now().Sub(start)
	rss.up.logf("Waited for %v till snapshot appeared on the disk.", waited)
	// The file is there. Rename it.
	err := os.Rename(raspistillOutFname, fname)
	if err != nil {
		return fmt.Errorf("failed to create a raspistill snapshot: %v", err)
	}
	rss.up.logf("raspistill snapshot created. fname: %s", fname)

	return nil
}
