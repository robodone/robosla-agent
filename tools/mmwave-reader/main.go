package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io/ioutil"
	"log"
	"strings"

	"github.com/robodone/robosla-agent/pkg/mmwave"
	"github.com/samofly/serial"
)

var (
	cfgDev   = flag.String("cfgdev", "", "Configuration serial port")
	cfgBaud  = flag.Int("cfgbaud", 115200, "Configuration baud rate")
	dataDev  = flag.String("datadev", "", "Data serial port")
	dataBaud = flag.Int("databaud", 921600, "Data baud rate")
)

func failf(format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	log.Fatalf(format, args...)
}

func readFromCfg(conn serial.Port) error {
	in := bufio.NewScanner(conn)
	for in.Scan() {
		txt := strings.TrimSpace(in.Text())
		log.Printf("%s\n", txt)
	}
	if err := in.Err(); err != nil {
		log.Printf("readFromCfg: %v", err)
		return err
	}
	return nil
}

func cubeToPNG(cube []byte, width, height int) ([]byte, error) {
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
	if err := png.Encode(&res, img); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %v", err)
	}
	return res.Bytes(), nil
}

func main() {
	flag.Parse()
	if *cfgDev == "" {
		failf("--cfgdev not specified")
	}
	if *dataDev == "" {
		failf("--dataDev not specified")
	}
	conn, err := mmwave.OpenDev(*cfgDev, *cfgBaud, *dataDev, *dataBaud)
	if err != nil {
		failf("can't open serial ports to mmWave radar: %v", err)
	}
	defer conn.Close()
	go readFromCfg(conn.Cfg)

	if err := conn.Configure(); err != nil {
		failf("Failed to configure the radar device: %v", err)
	}
	for i := 0; ; i++ {
		cube, err := conn.TakeSnapshot()
		if err != nil {
			failf("Failed to read radar data: %v", err)
		}
		pngData, err := cubeToPNG(cube, 384, 128)
		if err != nil {
			failf("cubeToPNG: %v", err)
		}
		log.Printf("PNG datalen: %d", len(pngData))
		fname := fmt.Sprintf("radar-cube-%04d.png", i)
		if err := ioutil.WriteFile(fname, pngData, 0644); err != nil {
			log.Printf("Error: can't save %s: %v", fname, err)
		}
	}
}
