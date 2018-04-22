package mmwave

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/samofly/serial"
)

const (
	CfgBaudRate  = 115200
	DataBaudRate = 921600

	BufferSize  = 4 << 10
	PreviewSize = 1 << 10
	HeaderSize  = 36
)

var (
	MagicWord = []byte{0x02, 0x01, 0x04, 0x03, 0x06, 0x05, 0x08, 0x07} // {0x0102,0x0304,0x0506,0x0708}
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

type Conn struct {
	CfgDev  string
	DataDev string
	Cfg     serial.Port
	Data    serial.Port
	cubeCh  <-chan []byte
}

func OpenDev(cfgDev string, cfgBaud int, dataDev string, dataBaud int) (res *Conn, err error) {
	cubeCh := make(chan []byte, 1)
	res = &Conn{CfgDev: cfgDev, DataDev: dataDev, cubeCh: cubeCh}
	res.Cfg, err = serial.Open(cfgDev, cfgBaud)
	if err != nil {
		return nil, fmt.Errorf("failed to open cfg port: %v", err)
	}
	defer func() {
		if err != nil {
			res.Cfg.Close()
		}
	}()
	res.Data, err = serial.Open(dataDev, dataBaud)
	if err != nil {
		return nil, fmt.Errorf("failed to open data port: %v", err)
	}
	go res.readFromData(cubeCh)
	go res.readFromCfg()
	return res, nil
}

func (c *Conn) Close() error {
	err1 := c.Cfg.Close()
	err2 := c.Data.Close()
	if err1 == nil && err2 == nil {
		return nil
	}
	if err1 != nil && err2 != nil {
		return fmt.Errorf("errors while closing cfg and data serial ports. Cfg err: %v, data err: %v", err1, err2)
	}
	if err1 != nil {
		return fmt.Errorf("error while closing cfg serial port: %v", err1)
	}
	if err2 != nil {
		return fmt.Errorf("error while closing data serial port: %v", err2)
	}
	panic("unreachable")
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

// Configure stops the radar sensor and configures it and leaves the sensor stopped.
// It must be called at least once after opening the connection.
func (c *Conn) Configure() (err error) {
	send := func(cmd string) {
		if err != nil {
			return
		}
		err = sendSerial(c.Cfg, cmd)
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
	//send("sensorStart")
	return
}

func (c *Conn) TakeSnapshot() ([]byte, error) {
	// Receive all stale data and forget it.
	var cleared bool
	for !cleared {
		select {
		case <-c.cubeCh:
		default:
			cleared = true
		}
	}
	// Start the sensor
	sendSerial(c.Cfg, "sensorStart")
	select {
	case cube, ok := <-c.cubeCh:
		if !ok {
			// The connection was closed.
			return nil, io.EOF
		}
		return cube, nil
	case <-time.After(20 * time.Second):
		return nil, fmt.Errorf("taking a snapshot timed out")
	}
}

func (c *Conn) readFromData(cubeCh chan<- []byte) {
	defer close(cubeCh)
	r := bufio.NewReaderSize(c.Data, BufferSize)
	cube := make([]byte, 128*16*3*4*4) // numRangeBins * numDopplerBins * numTxAntennas * numRxAntennas * 4 bytes
	for {
		data, err := r.Peek(PreviewSize)
		if err != nil && err != io.EOF {
			log.Printf("readFromData, peek failed: %v", err)
			return
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
			log.Printf("failed to read radar message header: %v", err)
			return
		}
		log.Printf("hdr: %+v", hdr)
		r.Discard(int(hdr.TotalPacketLen) - HeaderSize)
		if _, err = io.ReadFull(r, cube); err != nil {
			log.Printf("failed to read radar data cube (size: %d): %v", len(cube), err)
			return
		}
		sendSerial(c.Cfg, "sensorStop")
		// TODO(krasin): properly wait for sensorStop confirmation.
		time.Sleep(2 * time.Second)
		cubeCh <- cube
	}
}

func (c *Conn) readFromCfg() error {
	in := bufio.NewScanner(c.Cfg)
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
