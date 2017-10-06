package ur

import (
	"fmt"
	"io"
	"log"
	"math"
	"net"
)

type PacketType uint8

const (
	MaxPacketSize = 65535

	RTDE_REQUEST_PROTOCOL_VERSION      = PacketType(86)
	RTDE_GET_URCONTROL_VERSION         = PacketType(118)
	RTDE_CONTROL_PACKAGE_SETUP_OUTPUTS = PacketType(79)
	RTDE_CONTROL_PACKAGE_START         = PacketType(83)
	RTDE_DATA_PACKAGE                  = PacketType(85)

	RTDE_PROTOCOL_VERSION = 2
)

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

func ReceiveRTDEPacket(conn net.Conn) (typ PacketType, body []byte, err error) {
	size, typ, err := readHeader(conn)
	if err != nil {
		return 0, nil, err
	}
	// Now, read the body.
	body = make([]byte, size-3)
	if _, err = io.ReadFull(conn, body); err != nil {
		return 0, nil, err
	}
	return typ, body, nil
}

func sendAndReceive(conn net.Conn, typ PacketType, bodyParts ...[]byte) (respTyp PacketType, body []byte, err error) {
	if err := sendPacket(conn, typ, bodyParts...); err != nil {
		return 0, nil, err
	}
	log.Printf("Packet type %d sent", typ)
	return ReceiveRTDEPacket(conn)
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

func ConnectRTDE(host string, port int, output string) (net.Conn, error) {
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, fmt.Errorf("could not open connection to a UR robot at %s:%d. Error: %v", host, port, err)
	}
	sr := func(typ PacketType, bodyParts ...[]byte) {
		if err != nil {
			return
		}
		_, _, err = sendAndReceive(conn, typ, bodyParts...)
	}
	sr(RTDE_REQUEST_PROTOCOL_VERSION, u16Bytes(RTDE_PROTOCOL_VERSION))
	sr(RTDE_CONTROL_PACKAGE_SETUP_OUTPUTS, f64Bytes(6 /* frequency */), []byte("actual_TCP_speed"))
	sr(RTDE_CONTROL_PACKAGE_START)
	if err != nil {
		return nil, fmt.Errorf("failed to establish RTDE connection to a UR robot: %v", err)
	}
	return conn, err
}
