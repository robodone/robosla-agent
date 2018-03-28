package main

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
)

type RaspistillSnapshotter struct {
	mu sync.Mutex
	up *Uplink
}

func (rss *RaspistillSnapshotter) TakeSnapshot(ctx context.Context, prefix string, numFrames int) error {
	if numFrames != 1 {
		return fmt.Errorf("raspistill snapshot does not support taking multiple frames, but %d frames were requested", numFrames)
	}
	rss.mu.Lock()
	defer rss.mu.Unlock()

	fname := fmt.Sprintf("%s%02d-camera0.jpg", prefix, 0)
	cmd := exec.Command("/usr/bin/raspistill", "-w", "640", "-h", "480", "-q", "65", "-o", fname)
	data, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to take a raspistill snapshot: %v\nCombined output:\n%v", err, string(data))
	}
	rss.up.logf("raspistill output: %s. fname: %s", string(data), fname)

	return nil
}
