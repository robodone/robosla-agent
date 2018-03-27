package main

import "context"

type Snapshotter interface {
	TakeSnapshot(ctx context.Context, prefix string, numFrames int) error
}
