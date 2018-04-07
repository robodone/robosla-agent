package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

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

func mustOpen(name, ttyDev string, baudRate int) serial.Port {
	conn, err := serial.Open(ttyDev, baudRate)
	if err != nil {
		failf("Could not open %s serial port %s at %d bps. Error: %v", name, ttyDev, baudRate, err)
	}
	log.Printf("Opened %s serial port %s at %d bps.", name, ttyDev, baudRate)
	return conn
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

func readFromData(conn serial.Port) error {
	r := bufio.NewReaderSize(conn, BufferSize)
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
		log.Printf("cube[:16]: %02x", cube[:16])
	}
	return nil
}

func configureRadar(cfgConn serial.Port) (err error) {
	send := func(cmd string) {
		if err != nil {
			return
		}
		if !strings.HasSuffix(cmd, "\n") {
			cmd += "\n"
		}
		_, err = fmt.Fprint(cfgConn, cmd)
		fmt.Print(cmd)
		time.Sleep(100 * time.Millisecond)
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
	cfgConn := mustOpen("configuration", *cfgDev, *cfgBaud)
	defer cfgConn.Close()
	dataConn := mustOpen("data", *dataDev, *dataBaud)
	defer dataConn.Close()
	go readFromCfg(cfgConn)

	if err := configureRadar(cfgConn); err != nil {
		failf("Failed to configure the radar device: %v", err)
	}
	if err := readFromData(dataConn); err != nil {
		failf("Failed to read radar data: %v", err)
	}
}
