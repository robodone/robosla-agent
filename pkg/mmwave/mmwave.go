package mmwave

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/samofly/serial"
)

const (
	CfgBaudRate  = 115200
	DataBaudRate = 921600

	BufferSize  = 1 << 10
	PreviewSize = 1 << 9
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

type Logger interface {
	Logf(format string, args ...interface{})
}

type Conn struct {
	log     Logger
	cfgDev  string
	dataDev string
	cfg     serial.Port
	data    serial.Port
	cubeCh  <-chan []byte
}

// Open assumes that the radar is on /dev/ttyACM0 and /dev/ttyACM1 ports.
// It also uses the standard baud rates.
func Open(logger Logger) (*Conn, error) {
	return OpenDev(logger, "/dev/ttyACM0", CfgBaudRate, "/dev/ttyACM1", DataBaudRate)
}

func OpenDev(logger Logger, cfgDev string, cfgBaud int, dataDev string, dataBaud int) (res *Conn, err error) {
	cubeCh := make(chan []byte, 1)
	res = &Conn{log: logger, cfgDev: cfgDev, dataDev: dataDev, cubeCh: cubeCh}
	res.cfg, err = serial.Open(cfgDev, cfgBaud)
	if err != nil {
		return nil, fmt.Errorf("failed to open cfg port: %v", err)
	}
	defer func() {
		if err != nil {
			res.cfg.Close()
		}
	}()
	res.data, err = serial.Open(dataDev, dataBaud)
	if err != nil {
		return nil, fmt.Errorf("failed to open data port: %v", err)
	}
	go res.readFromData(cubeCh)
	go res.readFromCfg()
	return res, nil
}

func (c *Conn) Close() error {
	err1 := c.cfg.Close()
	err2 := c.data.Close()
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

func sendSerial(logger Logger, conn serial.Port, cmd string) error {
	if !strings.HasSuffix(cmd, "\n") {
		cmd += "\n"
	}
	fmt.Print(cmd)
	_, err := fmt.Fprint(conn, cmd)
	if err != nil {
		logger.Logf("Error while writing to serial port: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	return err
}

// Configure stops the radar sensor and configures it and leaves the sensor stopped.
// It must be called at least once after opening the connection.
func (c *Conn) Configure() (err error) {
	send := func(cmd string) {
		if err != nil {
			return
		}
		err = sendSerial(c.log, c.cfg, cmd)
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

// Configure stops the radar sensor and configures it and leaves the sensor stopped.
// It must be called at least once after opening the connection.
func (c *Conn) MiniConfigure() (err error) {
	send := func(cmd string) {
		if err != nil {
			return
		}
		err = sendSerial(c.log, c.cfg, cmd)
	}

	send("% mmwave-reader")
	send("sensorStop")
	send("flushCfg")
	send("dfeDataOutputMode 1")
	send("channelCfg 15 7 0")
	send("adcCfg 2 1")
	send("profileCfg 0 77 267 7 57.14 0 0 70 1 112 2279 0 0 30")
	send("chirpCfg 0 0 0 0 0 0 0 1")
	send("chirpCfg 1 1 0 0 0 0 0 4")
	send("chirpCfg 2 2 0 0 0 0 0 2")
	send("frameCfg 0 2 16 0 1200 1 0")
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
	// Trying to be more robust about timeouts.
	for i := 0; i < 3; i++ {
		if err := c.MiniConfigure(); err != nil {
			return nil, fmt.Errorf("failed to configure before taking a snapshot: %v", err)
		}
		// Start the sensor
		sendSerial(c.log, c.cfg, "sensorStart")
		select {
		case cube, ok := <-c.cubeCh:
			if !ok {
				// The connection was closed.
				return nil, io.EOF
			}
			return cube, nil
		case <-time.After(20 * time.Second):
		}
	}
	return nil, fmt.Errorf("taking a snapshot timed out (even after retries)")
}

func (c *Conn) readFromData(cubeCh chan<- []byte) {
	defer close(cubeCh)
	r := bufio.NewReaderSize(c.data, BufferSize)
	cube := make([]byte, 128*16*3*4*4) // numRangeBins * numDopplerBins * numTxAntennas * numRxAntennas * 4 bytes
	for {
		data, err := r.Peek(PreviewSize)
		if err != nil && err != io.EOF {
			c.log.Logf("readFromData, peek failed: %v", err)
			return
		}
		if len(data) < HeaderSize {
			// Not enough data, let's wait a bit.
			time.Sleep(20 * time.Millisecond)
			continue
		}
		pos := bytes.Index(data, MagicWord)
		if pos < 0 {
			// No magic word in the preview window. Discarding the data.
			c.log.Logf("Discard %d bytes", len(data))
			r.Discard(len(data))
			continue
		}
		if pos > 0 {
			// Discard the remainings of the previous frame.
			c.log.Logf("Discard %d bytes", pos)
			r.Discard(pos)
		}
		_, err = r.Peek(HeaderSize)
		if err == io.EOF {
			// Not enough data
			continue
		}
		var hdr Header
		if err = binary.Read(r, binary.LittleEndian, &hdr); err != nil {
			c.log.Logf("failed to read radar message header: %v", err)
			return
		}
		c.log.Logf("hdr: %+v", hdr)
		r.Discard(int(hdr.TotalPacketLen) - HeaderSize)
		if _, err = io.ReadFull(r, cube); err != nil {
			c.log.Logf("failed to read radar data cube (size: %d): %v", len(cube), err)
			return
		}
		sendSerial(c.log, c.cfg, "sensorStop")
		// TODO(krasin): properly wait for sensorStop confirmation.
		time.Sleep(500 * time.Millisecond)
		select {
		case cubeCh <- cube:
		default:
		}
	}
}

func (c *Conn) readFromCfg() error {
	in := bufio.NewScanner(c.cfg)
	for in.Scan() {
		txt := strings.TrimSpace(in.Text())
		c.log.Logf("%s\n", txt)
	}
	if err := in.Err(); err != nil {
		c.log.Logf("readFromCfg: %v", err)
		return err
	}
	return nil
}
