package mmwave

import (
	"fmt"

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
