package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/robodone/robosla-common/pkg/autoupdate"
	"github.com/robodone/robosla-common/pkg/device_api"
)

var (
	Version     = "dev"
	showVersion = flag.Bool("version", false, "If specified, the binary will show its version and exit")
	baudRate    = flag.Int("rate", 115200, "Baud rate")
	apiServer   = flag.String("api_server", "", "Address of the API server")
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
	go autoupdate.Run(autoupdate.ProdManifestURL, Version, time.Minute, 5*time.Minute)

	fmt.Fprintf(os.Stderr, "RoboSLA agent version: %s\n", Version)

	if *apiServer == "" {
		*apiServer = device_api.ChooseServer(Version)
	}
	up := NewUplink(*apiServer)
	go up.Run()

	down := NewDownlink(up, *baudRate)
	go down.Run()

	exe := NewExecutor(up, down)
	go exe.Run()

	// Never exit
	select {}
}
