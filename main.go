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
	virtual     = flag.Bool("virtual", false, "If specified, the printer will simulate a connection to a printer.")
	speedup     = flag.Float64("speedup", 10, "Speedup for --virtual mode")
	deviceType  = flag.String("device_type", "usb-gcode", "Device type. Default value (usb-gcode) covers most common 3d printers / CNC machines based on g-code. Another possible value: ur3 for Universal Robots UR3.")
	ur3Host     = flag.String("ur3_host", "", "UR3 robot host (only used if -device_type=ur3)")
	ur3Port     = flag.Int("ur3_port", 30002, "UR3 port (only used if -device_type=ur3)")
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

	var down Downlink
	switch *deviceType {
	case "usb-gcode":
		if *virtual {
			down = NewVirtualDownlink(up, *speedup)
		} else {
			/*realDown := NewRealDownlink(up, *baudRate)
			go realDown.Run()
			down = realDown*/
			dfaDown := NewDFADownlink(up, *baudRate)
			go dfaDown.Run()
			down = dfaDown
		}
	case "ur3":
		if *ur3Host == "" {
			up.Fatalf("-ur3_host not specified")
		}
		if *ur3Port == 0 {
			up.Fatalf("-ur3_port not specified")
		}
		if *virtual {
			up.Fatalf("virtual UR3 is not supported")
		}
		ur3Down := NewUR3Downlink(up, *ur3Host, *ur3Port)
		go ur3Down.Run()
		down = ur3Down
	default:
		up.Fatalf("Unsupported -device_type value: %q", *deviceType)
	}
	sh := NewShell(up, down, *virtual)
	go sh.Run()

	// Never exit
	select {}
}
