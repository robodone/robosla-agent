package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/robodone/robosla-common/pkg/autoupdate"
)

var (
	Version     = "dev"
	showVersion = flag.Bool("version", false, "If specified, the binary will show its version and exit")
	ttyDev      = flag.String("dev", "", "Device to connect to the printer, such as /dev/ttyUSB0 or /dev/ttyACM0")
	baudRate    = flag.Int("rate", 115200, "Baud rate")
	apiServer   = flag.String("api_server", "test1.robosla.com", "Address of the API server")
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
	if *showVersion {
		// Show version and quit. This is important for autoupdates.
		fmt.Printf("%s\n", Version)
		os.Exit(0)
	}
	needsRestart, err := autoupdate.UpdateCurrentBinaryIfNeeded(autoupdate.ProdManifestURL, Version)
	if err != nil {
		log.Printf("Autoupdates don't work: %v\n", err)
		// We will still proceed, as the work is more important than updates.
	}
	if needsRestart {
		log.Printf("Autoupdates installed a new version. Quitting to allow a restart.")
		os.Exit(0)
	}

	fmt.Fprintf(os.Stderr, "RoboSLA agent version: %s\n", Version)
	if *ttyDev == "" {
		failf("--dev not specified")
	}
	up := NewUplink(*apiServer)
	go up.Run()

	down := NewDownlink(up, *ttyDev, *baudRate)
	go down.Run()

	exe := NewExecutor(up, down)
	go exe.Run()

	// Never exit
	select {}
}
