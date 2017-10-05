package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
)

const (
	MaxPacketSize = 65535

	RTDE_REQUEST_PROTOCOL_VERSION = 86

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

func sendPacket(w io.Writer, typ PacketType, body []byte) error {
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

func u16Bytes(val uint16) []byte {
	return []byte{byte(val >> 8), byte(val & 0xFF)}
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

	go func() {
		// Read incoming packages, decode them and print to stdout.
		buf := make([]byte, MaxPacketSize)

		// First, we read the header.
		size, typ, err := readHeader(conn)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				// The connection is closed. Stop reading from it.
				log.Printf("Robot connection is closed: %v", err)
				return
			}
			log.Fatalf("Unexpected error while reading a packet header: %v", err)
		}
		// Now, read the body.
		body := buf[:size-3]
		if _, err = io.ReadFull(conn, body); err != nil {
			log.Fatalf("Failed to read the packet body (size=%d, type=%d): %v", size, typ, err)
		}
		// TODO(krasin): make best effort to decode the packet.
		log.Printf("Type: %d, Packet: %v", typ, body)
	}()

	if err := sendPacket(conn, RTDE_REQUEST_PROTOCOL_VERSION, u16Bytes(RTDE_PROTOCOL_VERSION)); err != nil {
		log.Fatalf("Failed to send RTDE_REQUEST_PROTOCOL_VERSION packet: %v", err)
	}
	log.Printf("RTDE_REQUEST_PROTOCOL_VERSION sent")
	select {}
}
