package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"reflect"
	"time"

	"github.com/ev3go/ev3dev"

	_ "image/jpeg"
)

var (
	fbDev     = flag.String("dev", "", "Framebuffer device to draw on")
	imagePath = flag.String("image", "", "Image to display")
	width     = flag.Int("width", 1920, "Display width in pixels")
	height    = flag.Int("height", 1080, "Display height in pixels")
	stride    = flag.Int("stride", 1920*8, "Display width in bytes")
)

func checkFlag(value *string, name string) {
	if *value == "" {
		log.Fatalf("%s is not specified", name)
	}
}

type MmapRGBA struct {
	buf    []byte
	bounds image.Rectangle
}

func (img *MmapRGBA) ColorModel() color.Model {
	return color.RGBAModel
}

func (img *MmapRGBA) Bounds() image.Rectangle {
	return img.bounds
}

func (img *MmapRGBA) At(x, y int) color.Color {
	idx := y*4*img.bounds.Dx() + x*4
	return &color.RGBA{B: img.buf[idx+0], G: img.buf[idx+1], R: img.buf[idx+2], A: img.buf[idx+3]}
}

func (img *MmapRGBA) Set(x, y int, c color.Color) {
	r, g, b, a := c.RGBA()
	idx := y*4*img.bounds.Dx() + x*4
	// color.RGBA returns 16 bit values. 65535 / 257 = 255.
	img.buf[idx+0], img.buf[idx+1], img.buf[idx+2], img.buf[idx+3] = byte(b/257), byte(g/257), byte(r/257), byte(a/257)
}

func newImage(buf []byte, rect image.Rectangle, stride int) (draw.Image, error) {
	log.Printf("newImage: len(buf)=%d, rect=%+v, stride=%d", len(buf), rect, stride)
	if stride != rect.Dx()*4 {
		return nil, fmt.Errorf("Unsupported bytes per pixel: %v", float64(stride)/float64(rect.Dx()))
	}
	for i := range buf {
		buf[i] = byte(((i / 32) % 2) * 255)
	}
	return &MmapRGBA{buf: buf, bounds: rect}, nil
}

func loadImage(fname string) (image.Image, error) {
	f, err := os.Open(fname)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	log.Printf("Image type: %v", reflect.TypeOf(img))
	return img, err
}

func savePNG(fname string, img image.Image) error {
	f, err := os.OpenFile(fname, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	return nil
}

func main() {
	flag.Parse()

	checkFlag(fbDev, "--dev")
	checkFlag(imagePath, "--image")
	fbuf := ev3dev.NewFrameBuffer(*fbDev, newImage, *width, *height, *stride)
	if err := fbuf.Init(true); err != nil {
		log.Fatalf("fbuf.Init: %v", err)
	}
	defer fbuf.Close()

	img, err := loadImage(*imagePath)
	if err != nil {
		log.Fatalf("loadImage(%q): %v", *imagePath, err)
	}
	draw.Draw(fbuf, fbuf.Bounds(), img, image.ZP, draw.Src)
	if err := savePNG("fb.png", fbuf); err != nil {
		log.Printf("Error while saving the image sent to the framebuffer: %v", err)
	}
	for {
		time.Sleep(time.Second)
	}
}
