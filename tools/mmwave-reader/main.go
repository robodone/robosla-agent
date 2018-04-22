package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/ioutil"
	"log"
	"strings"
	"time"

	"github.com/robodone/robosla-agent/pkg/mmwave"
	"github.com/samofly/serial"
)

const (
	BufferSize  = 4 << 10
	PreviewSize = 1 << 10
	HeaderSize  = 36
)

var (
	MagicWord = []byte{0x02, 0x01, 0x04, 0x03, 0x06, 0x05, 0x08, 0x07} // {0x0102,0x0304,0x0506,0x0708}

	cfgDev   = flag.String("cfgdev", "", "Configuration serial port")
	cfgBaud  = flag.Int("cfgbaud", 115200, "Configuration baud rate")
	dataDev  = flag.String("datadev", "", "Data serial port")
	dataBaud = flag.Int("databaud", 921600, "Data baud rate")
)

type Header struct {
	Magic          [8]byte
	Version        uint32
	TotalPacketLen uint32
	Platform       uint32
	FrameNumber    uint32
	TimeCPUCycles  uint32
	NumDetectedObj uint32
	NumTLVs        uint32
}

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

// We assume that cube consists of serialized fixed-point little-endian float16 numbers.
// We return a slice of uint16 values. The result length is exactly
// the half of the input length.
func cubeToUint16(cube []byte) []uint16 {
	res := make([]uint16, len(cube)/2)
	for i := range res {
		res[i] = uint16(cube[i*2]) + uint16(cube[i*2+1])<<8
	}
	return res
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

func readFromData(conn *mmwave.Conn) error {
	r := bufio.NewReaderSize(conn.Data, BufferSize)
	cube := make([]byte, 128*16*3*4*4) // numRangeBins * numDopplerBins * numTxAntennas * numRxAntennas * 4 bytes
	for {
		data, err := r.Peek(PreviewSize)
		if err != nil && err != io.EOF {
			return err
		}
		if len(data) < HeaderSize {
			// Not enough data, let's wait a bit.
			time.Sleep(100 * time.Millisecond)
			continue
		}
		pos := bytes.Index(data, MagicWord)
		if pos < 0 {
			// No magic word in the preview window. Discarding the data.
			log.Printf("Discard %d bytes", len(data))
			r.Discard(len(data))
			continue
		}
		if pos > 0 {
			// Discard the remainings of the previous frame.
			log.Printf("Discard %d bytes", pos)
			r.Discard(pos)
		}
		_, err = r.Peek(HeaderSize)
		if err == io.EOF {
			// Not enough data
			continue
		}
		var hdr Header
		if err = binary.Read(r, binary.LittleEndian, &hdr); err != nil {
			return err
		}
		log.Printf("hdr: %+v", hdr)
		r.Discard(int(hdr.TotalPacketLen) - HeaderSize)
		if _, err = io.ReadFull(r, cube); err != nil {
			return fmt.Errorf("failed to read radar data cube (size: %d): %v", len(cube), err)
		}
		ucube := cubeToUint16(cube)
		log.Printf("ucube[:16]: %v", ucube[:128])
		pngData, err := cubeToPNG(cube, 384, 128)
		if err != nil {
			return fmt.Errorf("cubeToPNG: %v", err)
		}
		log.Printf("PNG datalen: %d", len(pngData))
		go func(frame int, data []byte) {
			fname := fmt.Sprintf("radar-cube-%04d.png", frame)
			if err := ioutil.WriteFile(fname, data, 0644); err != nil {
				log.Printf("Error: can't save %s: %v", fname, err)
			}
		}(int(hdr.FrameNumber), pngData)
		sendSerial(conn.Cfg, "sensorStop")
		time.Sleep(2 * time.Second)
		sendSerial(conn.Cfg, "sensorStart")
	}
	return nil
}

func sendSerial(conn serial.Port, cmd string) error {
	if !strings.HasSuffix(cmd, "\n") {
		cmd += "\n"
	}
	fmt.Print(cmd)
	_, err := fmt.Fprint(conn, cmd)
	if err != nil {
		log.Printf("Error while writing to serial port: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	return err
}

func configureRadar(cfgConn serial.Port) (err error) {
	send := func(cmd string) {
		if err != nil {
			return
		}
		err = sendSerial(cfgConn, cmd)
	}

	send("% mmwave-reader")
	send("sensorStop")
	send("flushCfg")
	send("dfeDataOutputMode 1")
	send("channelCfg 15 7 0")
	send("adcCfg 2 1")
	send("adcbufCfg 0 1 0 1")
	send("profileCfg 0 77 267 7 57.14 0 0 70 1 112 2279 0 0 30")
	send("chirpCfg 0 0 0 0 0 0 0 1")
	send("chirpCfg 1 1 0 0 0 0 0 4")
	send("chirpCfg 2 2 0 0 0 0 0 2")
	send("frameCfg 0 2 16 0 1200 1 0")
	send("guiMonitor 1 1 0 0 0 1")
	send("cfarCfg 0 2 8 4 3 0 1280")
	send("peakGrouping 1 1 1 1 114")
	send("multiObjBeamForming 1 0.5")
	send("clutterRemoval 0")
	send("calibDcRangeSig 0 -5 8 256")
	send("compRangeBiasAndRxChanPhase 0.0 1 0 1 0 1 0 1 0 1 0 1 0 1 0 1 0 1 0 1 0 1 0 1 0")
	send("measureRangeBiasAndRxChanPhase 0 1.5 0.2")
	send("CQRxSatMonitor 0 3 5 123 0")
	send("CQSigImgMonitor 0 55 4")
	send("analogMonitor 1 1")
	send("sensorStart")
	return
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

	if err := configureRadar(conn.Cfg); err != nil {
		failf("Failed to configure the radar device: %v", err)
	}
	if err := readFromData(conn); err != nil {
		failf("Failed to read radar data: %v", err)
	}
}
