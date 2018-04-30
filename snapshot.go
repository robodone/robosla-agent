package main

import (
	"context"
	"fmt"
	"sync"
)

type Snapshotter interface {
	TakeSnapshot(ctx context.Context, prefix string, numFrames int) error
}

type CombinedSnapshotter struct {
	Snaps map[string]Snapshotter
}

func (cs *CombinedSnapshotter) TakeSnapshot(ctx context.Context, prefix string, numFrames int) error {
	if numFrames != 1 {
		return fmt.Errorf("only taking a single snapshot is supported by CombinedSnapshot, but %d was requested", numFrames)
	}
	errs := make(map[string]error)
	var wg sync.WaitGroup
	wg.Add(len(cs.Snaps))
	for name, snap := range cs.Snaps {
		go func(name string, snap Snapshotter) {
			defer wg.Done()
			err := snap.TakeSnapshot(ctx, prefix, numFrames)
			if err != nil {
				errs[name] = err
			}
		}(name, snap)
	}
	wg.Wait()
	for name, err := range errs {
		if err != nil {
			return fmt.Errorf("failed to take a %s snapshot: %v", name, err)
		}
	}
	return nil
}
