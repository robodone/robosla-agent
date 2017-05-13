package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

var (
	dev      = flag.String("dev", "", "Device to connect to the printer, such as /dev/ttyUSB0 or /dev/ttyACM0")
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

func main() {
	flag.Parse()

	if *dev == "" {
		failf("--dev not specified")
	}
	if *gcode == "" {
		failf("--gcode not specified")
	}
}
