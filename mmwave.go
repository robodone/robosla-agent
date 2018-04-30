package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"io/ioutil"
	"sync"

	"github.com/robodone/robosla-agent/pkg/mmwave"
)

type MmwaveSnapshotter struct {
	mu    sync.Mutex
	up    *Uplink
	radar *mmwave.Conn
}

func cubeToJPEG(cube []byte, width, height int) ([]byte, error) {
	if width*height*2 != len(cube) {
		return nil, fmt.Errorf("unexpected length of cube, want w*h*2 = %d*%d*2 = %d, got %d",
			width, height, width*height*2, len(cube))
	}
	img := image.NewGray16(image.Rect(0, 0, width, height))
	// 1. Make it big-endian.
	copy(img.Pix, cube)
	for i := 0; i*2+1 < len(cube); i++ {
		img.Pix[i*2], img.Pix[i*2+1] = img.Pix[i*2+1], img.Pix[i*2]
	}
	var res bytes.Buffer
	if err := jpeg.Encode(&res, img, nil); err != nil {
		return nil, fmt.Errorf("failed to encode JPEG: %v", err)
	}
	return res.Bytes(), nil
}

func (rss *MmwaveSnapshotter) TakeSnapshot(ctx context.Context, prefix string, numFrames int) error {
	if numFrames != 1 {
		return fmt.Errorf("mmwave snapshot does not support taking multiple frames, but %d frames were requested", numFrames)
	}
	rss.mu.Lock()
	defer rss.mu.Unlock()

	if rss.radar == nil {
		var err error
		rss.radar, err = mmwave.Open()
		if err != nil {
			return fmt.Errorf("failed to connect to mmwave radar: %v", err)
		}
		if err := rss.radar.Configure(); err != nil {
			return fmt.Errorf("failed to configure the radar device: %v", err)
		}
	}
	fname := fmt.Sprintf("%s%02d-camera0.jpg", prefix, 0)
	cube, err := rss.radar.TakeSnapshot()
	if err != nil {
		return fmt.Errorf("failed to read radar data: %v", err)
	}
	jpegData, err := cubeToJPEG(cube, 384, 128)
	if err != nil {
		return fmt.Errorf("cubeToImage: %v", err)
	}
	if err := ioutil.WriteFile(fname, jpegData, 0644); err != nil {
		return fmt.Errorf("Error: can't save %s: %v", fname, err)
	}
	return nil
}
