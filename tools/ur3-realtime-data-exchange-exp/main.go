package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"time"
)

const (
	MaxPacketSize = 65535

	RTDE_REQUEST_PROTOCOL_VERSION      = 86
	RTDE_GET_URCONTROL_VERSION         = 118
	RTDE_CONTROL_PACKAGE_SETUP_OUTPUTS = 79
	RTDE_CONTROL_PACKAGE_START         = 83

	RTDE_PROTOCOL_VERSION = 2
)

var (
	ur3Host     = flag.String("ur3_host", "", "UR3 robot host")
	ur3RTDEPort = flag.Int("ur3_rtde_port", 30004, "UR3 RealTime Data Exchange port")
)

type PacketType uint8

func readHeader(r io.Reader) (size int, typ PacketType, err error) {
	var buf [3]byte
	if _, err = io.ReadFull(r, buf[:]); err != nil {
		return
	}
	// Network byte order aka Big Endian
	size = int(buf[0])<<8 + int(buf[1])
	typ = PacketType(buf[2])
	if size < 3 {
		return 0, 0, fmt.Errorf("size is too small: %d, want at least 3", size)
	}
	return
}

func sendPacket(w io.Writer, typ PacketType, bodyParts ...[]byte) error {
	var body []byte
	for _, v := range bodyParts {
		body = append(body, v...)
	}
	size := len(body) + 3
	if size > MaxPacketSize {
		return fmt.Errorf("Packet size is too large: %d, MaxPacketSize: %d\n", size, MaxPacketSize)
	}
	if _, err := w.Write([]byte{byte(size >> 8), byte(size & 0xFF), byte(typ)}); err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	return nil
}

func receivePacket(conn net.Conn) (typ PacketType, body []byte, err error) {
	size, typ, err := readHeader(conn)
	if err != nil {
		return 0, nil, err
	}
	log.Printf("Got a header, typ: %d, size: %d. Now, reading the body...", typ, size)
	// Now, read the body.
	body = make([]byte, size-3)
	if _, err = io.ReadFull(conn, body); err != nil {
		return 0, nil, err
	}
	// TODO(krasin): make best effort to decode the packet.
	log.Printf("Type: %d, Packet: %v", typ, body)

	return typ, body, nil
}

func sendAndReceive(conn net.Conn, typ PacketType, bodyParts ...[]byte) (respTyp PacketType, body []byte, err error) {
	if err := sendPacket(conn, typ, bodyParts...); err != nil {
		return 0, nil, err
	}
	log.Printf("Packet type %d sent", typ)
	return receivePacket(conn)
}

func u16Bytes(val uint16) []byte {
	return []byte{byte(val >> 8), byte(val & 0xFF)}
}

func u64Bytes(val uint64) []byte {
	return []byte{
		byte(val >> 56), byte(val >> 48), byte(val >> 40), byte(val >> 32),
		byte(val >> 24), byte(val >> 16), byte(val >> 8), byte(val),
	}
}

func f64Bytes(val float64) []byte {
	return u64Bytes(math.Float64bits(val))
}

func main() {
	flag.Parse()
	if *ur3Host == "" {
		log.Fatal("--ur3_host is not specified")
	}
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", *ur3Host, *ur3RTDEPort))
	if err != nil {
		log.Fatalf("Could not open connection to UR3 at %s:%d. Error: %v", *ur3Host, *ur3RTDEPort, err)

	}
	defer conn.Close()
	log.Printf("Opened robot connection to %s:%d.", *ur3Host, *ur3RTDEPort)

	/*	go func() {
		}()*/

	send := func(typ PacketType, bodyParts ...[]byte) {
		_, _, err := sendAndReceive(conn, typ, bodyParts...)
		if err != nil {
			log.Fatalf("Failed to send/receive a packet, typ: %d, bodyParts: %v, err: %v", typ, bodyParts, err)
		}
	}
	send(RTDE_REQUEST_PROTOCOL_VERSION, u16Bytes(RTDE_PROTOCOL_VERSION))
	time.Sleep(2 * time.Millisecond)
	//send(RTDE_GET_URCONTROL_VERSION, nil)
	//time.Sleep(time.Second)
	send(RTDE_CONTROL_PACKAGE_SETUP_OUTPUTS, f64Bytes(1 /* frequency */), []byte("safety_status_bits"))
	time.Sleep(2 * time.Millisecond)
	send(RTDE_CONTROL_PACKAGE_START)

	// Read incoming packages, decode them and print to stdout.
	for {
		_, _, err := receivePacket(conn)
		if err != nil {
			log.Fatalf("Failed to read from the socket: %v", err)
		}
	}

	select {}
}
