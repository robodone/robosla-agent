package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

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

func mustOpen(name, ttyDev string, baudRate int) serial.Port {
	conn, err := serial.Open(ttyDev, baudRate)
	if err != nil {
		failf("Could not open %s serial port %s at %d bps. Error: %v", name, ttyDev, baudRate, err)
	}
	log.Printf("Opened %s serial port %s at %d bps.", name, ttyDev, baudRate)
	return conn
}

func readFromDevice(conn serial.Port) {
	in := bufio.NewScanner(conn)
	for in.Scan() {
		txt := strings.TrimSpace(in.Text())
		log.Printf("%s\n", txt)
	}
	if err := in.Err(); err != nil {
		log.Printf("readFromDevice: %v", err)
	}
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
	send("profileCfg 0 77 267 7 57.14 0 0 70 1 240 4884 0 0 30")
	send("chirpCfg 0 0 0 0 0 0 0 1")
	send("chirpCfg 1 1 0 0 0 0 0 4")
	send("chirpCfg 2 2 0 0 0 0 0 2")
	send("frameCfg 0 2 16 0 100 1 0")
	send("guiMonitor 1 1 0 0 0 1")
	send("cfarCfg 0 2 8 4 3 0 1280")
	send("peakGrouping 1 1 1 1 229")
	send("multiObjBeamForming 1 0.5")
	send("clutterRemoval 0")
	send("calibDcRangeSig 0 -5 8 256")
	send("compRangeBiasAndRxChanPhase 0.0 1 0 1 0 1 0 1 0 1 0 1 0 1 0 1 0 1 0 1 0 1 0 1 0")
	send("measureRangeBiasAndRxChanPhase 0 1.5 0.2")
	send("CQRxSatMonitor 0 3 5 123 0")
	send("CQSigImgMonitor 0 119 4")
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
	go readFromDevice(cfgConn)

	if err := configureRadar(cfgConn); err != nil {
		failf("Failed to configure the radar device: %v", err)
	}
	select {}
}
