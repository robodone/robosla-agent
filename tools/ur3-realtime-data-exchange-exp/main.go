package main

import (
	"flag"
	"log"
	"math"

	"github.com/robodone/robosla-agent/pkg/ur"
)

var (
	ur3Host     = flag.String("ur3_host", "", "UR3 robot host")
	ur3RTDEPort = flag.Int("ur3_rtde_port", 30004, "UR3 RealTime Data Exchange port")
)

func l2(vec []float64) float64 {
	var sum2 float64
	for _, v := range vec {
		sum2 += v * v
	}
	return math.Sqrt(sum2)
}

func sub(a, b []float64) []float64 {
	res := make([]float64, len(a))
	if b != nil {
		for i := range a {
			res[i] = a[i] - b[i]
		}
	}
	return res
}

func main() {
	flag.Parse()
	if *ur3Host == "" {
		log.Fatal("--ur3_host is not specified")
	}
	conn, err := ur.ConnectRTDE(*ur3Host, *ur3RTDEPort, "actual_TCP_speed")
	if err != nil {
		log.Fatalf("ConnectRTDE: %v", err)
	}
	defer conn.Close()
	log.Printf("Opened robot connection to %s:%d.", *ur3Host, *ur3RTDEPort)

	// Read incoming packages, decode them and print to stdout.
	for {
		typ, body, err := ur.ReceiveRTDEPacket(conn)
		if err != nil {
			log.Fatalf("Failed to read from the socket: %v", err)
		}
		if typ == ur.RTDE_DATA_PACKAGE {
			vec := ur.ParseVector6D(body[1:])
			linSpeed := l2(vec[:3])
			rotSpeed := l2(vec[3:])
			if linSpeed < 2E-5 {
				linSpeed = 0
			}
			if rotSpeed < 5E-4 {
				rotSpeed = 0
			}
			log.Printf("Current TCP speed:  %+v, %+v", linSpeed, rotSpeed)
		}

	}

	select {}
}
