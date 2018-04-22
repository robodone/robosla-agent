package mmwave

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/samofly/serial"
)

const (
	CfgBaudRate  = 115200
	DataBaudRate = 921600
)

type Conn struct {
	CfgDev  string
	DataDev string
	Cfg     serial.Port
	Data    serial.Port
}

func OpenDev(cfgDev string, cfgBaud int, dataDev string, dataBaud int) (res *Conn, err error) {
	res = &Conn{CfgDev: cfgDev, DataDev: dataDev}
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

// Configure stops the radar sensor, configures it and starts the sensor again.
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
	send("sensorStart")
	return
}
