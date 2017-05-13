package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/samofly/serial"
)

var (
	ttyDev   = flag.String("dev", "", "Device to connect to the printer, such as /dev/ttyUSB0 or /dev/ttyACM0")
	baudRate = flag.Int("rate", 115200, "Baud rate")
	gcode    = flag.String("gcode", "", "gcode file to print")
)

func failf(format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}

func logf(format string, args ...interface{}) {
	log.Printf(format, args...)
}

func main() {
	flag.Parse()

	if *ttyDev == "" {
		failf("--dev not specified")
	}
	if *gcode == "" {
		failf("--gcode not specified")
	}
	conn, err := serial.Open(*ttyDev, *baudRate)
	if err != nil {
		failf("Could not open serial port %s at %d bps. Error: %v", *ttyDev, *baudRate, err)
	}
	defer conn.Close()
	logf("Opened %s at %d bps.", *ttyDev, *baudRate)
}
