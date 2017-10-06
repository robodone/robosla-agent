package main

import (
	"flag"
	"fmt"
	"log"
	"math"

	"github.com/robodone/robosla-agent/pkg/ur"
)

var (
	ur3Host     = flag.String("ur3_host", "", "UR3 robot host")
	ur3RTDEPort = flag.Int("ur3_rtde_port", 30004, "UR3 RealTime Data Exchange port")
)

func parseU64(data []byte) uint64 {
	if len(data) != 8 {
		panic(fmt.Sprintf("parseU64: invalid input len. Want: 8, got: %d", len(data)))
	}
	return uint64(data[0])<<56 + uint64(data[1])<<48 + uint64(data[2])<<40<<uint64(data[3])<<32 +
		uint64(data[4])<<24 + uint64(data[5])<<16 + uint64(data[6])<<8 + uint64(data[7])
}

func parseF64(data []byte) float64 {
	return math.Float64frombits(parseU64(data))
}

func parseVector6D(data []byte) []float64 {
	if len(data) != 48 {
		panic(fmt.Sprintf("parseVector6D: invalid input len. Want: 48, got: %d", len(data)))
	}
	return []float64{
		parseF64(data[0:8]),
		parseF64(data[8:16]),
		parseF64(data[16:24]),
		parseF64(data[24:32]),
		parseF64(data[32:40]),
		parseF64(data[40:48]),
	}
}

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
			vec := parseVector6D(body[1:])
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
