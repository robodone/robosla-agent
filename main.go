package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/robodone/robosla-agent/gcode"
	"github.com/samofly/serial"
)

var (
	Version   = "dev"
	ttyDev    = flag.String("dev", "", "Device to connect to the printer, such as /dev/ttyUSB0 or /dev/ttyACM0")
	baudRate  = flag.Int("rate", 115200, "Baud rate")
	gcodePath = flag.String("gcode", "", "gcode file to print")
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

type Cmd struct {
	Text string
	Type string
	Idx  int
	Dict map[byte]float64
}

func (cmd *Cmd) IsHost() bool {
	return cmd.Type == "M" && cmd.Idx == 7820
}

func (cmd *Cmd) Run() error {
	if cmd.Type != "M" || cmd.Idx != 7820 {
		return fmt.Errorf("unsupported host command %s%d", cmd.Type, cmd.Idx)
	}
	// Show a new frame on the LCD.
	frameIdx := int(cmd.Dict['S'])
	fname := path.Join(path.Dir(*gcodePath), fmt.Sprintf("frame-%06d.png", frameIdx))
	data, err := exec.Command("killall", "fbi").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "killall fbi: %v, %v\n", string(data), err)
	}
	data, err = exec.Command("fbi", "-noverbose", "-a", "-T", "1", fname).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to display a frame: %v, %v", string(data), err)
	}
	return nil
}

func parseGcodeCommand(line string) (*Cmd, error) {
	// Canonical representation of gcode commands is uppercase.
	// There are firmwares sensitive to that. It also helps to parse gcode,
	// if the case is known.
	line = strings.ToUpper(line)

	// Below is a trivial gcode parser. It splits everything into the words,
	// then every word is split into a letter and number part.
	// Then they are loaded into a dictionary, and then the command is analyzed.
	// It requires the G/M command to go the first. It also does not allow to
	// redefine letters. Double spaces are fine.
	words := strings.Split(line, " ")

	m := make(map[byte]float64)

	for i, word := range words {
		if word == "" {
			continue
		}
		if len(word) == 1 {
			return nil, fmt.Errorf("a single letter word %q is not acceptable", word)
		}
		letter := word[0]
		if i == 0 && letter != 'G' && letter != 'M' {
			return nil, fmt.Errorf("command does not start with a G or M word")
		}
		if i > 0 && (letter == 'G' || letter == 'M') {
			return nil, fmt.Errorf("command has a 'G' or 'M' word %q in the middle of a command", word)
		}
		if letter == 'G' || letter == 'M' {
			// Require a positive integer value
			if _, err := strconv.ParseUint(word[1:], 10, 64); err != nil {
				return nil, fmt.Errorf("invalid index to a 'G' or 'M' word %q. Must be positive integer.", word)
			}
		}
		val, err := strconv.ParseFloat(word[1:], 64)
		if err != nil {
			return nil, fmt.Errorf("can't parse number %q: %v", word[1:], err)
		}
		if _, ok := m[letter]; ok {
			return nil, fmt.Errorf("words with duplicate letter %q", letter)
		}
		m[letter] = val
	}

	var text string
	var typ string
	var idx int

	asm := func(letters ...byte) {
		var tok []string
		if val, ok := m['G']; ok {
			tok = append(tok, fmt.Sprintf("G%d", int(val+0.5)))
		}
		if val, ok := m['M']; ok {
			tok = append(tok, fmt.Sprintf("M%d", int(val+0.5)))
		}
		for _, letter := range letters {
			if val, ok := m[letter]; ok {
				tok = append(tok, fmt.Sprintf("%c%.6f", letter, val))
			}
		}
		text = strings.Join(tok, " ")
	}

	if _, ok := m['G']; ok {
		// Filter G commands.
		// TODO(krasin): make it more rigor.
		num := int(m['G'] + 0.5)
		typ = "G"
		idx = num

		switch num {
		case 0:
			// G0. Rapid linear move.
			// Only allow Z movements for now.
			asm('Z', 'F')
		case 1:
			// G1. Linear move.
			// Only allow Z movements for now.
			asm('Z', 'F')
		case 4:
			// G4. Dwell. P value is the delay in ms.
			asm('P')
		case 21:
			// G21. Set units to millimeters.
			asm()
		case 28:
			// G28. Homing. Only support Z homing for now.
			// F is a feed rate in units per minute.
			asm('Z', 'F')
		case 90:
			// G90. Set to absolute positioning.
			asm()
		default:
			return nil, fmt.Errorf("unsupported command G%d", num)
		}
	}
	if _, ok := m['M']; ok {
		// Filter M commands.
		// TODO(krasin): make it more rigor.
		num := int(m['M'] + 0.5)
		typ = "M"
		idx = num
		switch num {
		case 106:
			asm('P', 'S')
		case 107:
			asm('P', 'S')
		case 7820:
			asm('S')
		default:
			return nil, fmt.Errorf("unsupported command M%d", num)
		}
	}
	if text == "" {
		return nil, fmt.Errorf("failed to parse line %q: generated text is empty. A parser bug?", line)
	}
	return &Cmd{Text: text, Type: typ, Idx: idx, Dict: m}, nil
}

func loadGcode(fname string) ([]*Cmd, error) {
	data, err := ioutil.ReadFile(fname)
	if err != nil {
		return nil, err
	}
	var cmds []*Cmd
	for i, line := range strings.Split(string(data), "\n") {
		lineno := i + 1
		// Cut comments. They start with ;
		idx := strings.Index(line, ";")
		if idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			// An empty or comment-only line. Eat it right here.
			continue
		}
		cmd, err := parseGcodeCommand(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: invalid gcode: %v", fname, lineno, err)
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}

type Request struct {
	Type   string
	LineNo int
	AckCh  *chan bool
}

func handleTraffic(reqCh chan *Request) {
	oks := make(map[int]bool)
	waits := make(map[int]*chan bool)
	for req := range reqCh {
		switch req.Type {
		case "OK":
			oks[req.LineNo] = true
			if ch, ok := waits[req.LineNo]; ok {
				*ch <- true
				delete(waits, req.LineNo)
			}
		case "WaitForOK":
			if oks[req.LineNo] {
				*req.AckCh <- true
				continue
			}
			if _, ok := waits[req.LineNo]; ok {
				failf("Double wait for line %d", req.LineNo)
			}
			waits[req.LineNo] = req.AckCh

		default:
			failf("Unknown request type: %s", req.Type)
		}
	}
}

func readFromDevice(r io.Reader, reqCh chan *Request) {
	in := bufio.NewScanner(r)
	for in.Scan() {
		txt := strings.TrimSpace(in.Text())
		if strings.HasPrefix(txt, "ok ") {
			lineno, err := strconv.ParseUint(txt[3:], 10, 64)
			if err != nil {
				failf("Failed to parse a line number from an ok response %q: %v", txt, err)
			}
			reqCh <- &Request{Type: "OK", LineNo: int(lineno)}
		}
		fmt.Printf("%s\n", txt)
	}
	if err := in.Err(); err != nil {
		failf("readFromDevice: %v", err)
	}
}

func waitForOK(reqCh chan *Request, lineno int) {
	ackCh := make(chan bool)
	reqCh <- &Request{Type: "WaitForOK", LineNo: lineno, AckCh: &ackCh}
	<-ackCh
}

func main() {
	fmt.Fprintf(os.Stderr, "RoboSLA agent version: %s\n", Version)
	flag.Parse()

	if *ttyDev == "" {
		failf("--dev not specified")
	}
	if *gcodePath == "" {
		failf("--gcode not specified")
	}
	conn, err := serial.Open(*ttyDev, *baudRate)
	if err != nil {
		failf("Could not open serial port %s at %d bps. Error: %v", *ttyDev, *baudRate, err)
	}
	defer conn.Close()
	logf("Opened %s at %d bps.", *ttyDev, *baudRate)

	cmds, err := loadGcode(*gcodePath)
	if err != nil {
		failf("Could not load gcode from %s: %v", *gcodePath, err)
	}
	logf("Loaded %d gcode commands from %s.", len(cmds), *gcodePath)

	reqCh := make(chan *Request)
	go handleTraffic(reqCh)
	go readFromDevice(conn, reqCh)

	time.Sleep(time.Second)
	var lineno int
	for i := 0; i < len(cmds); i++ {
		if cmds[i].IsHost() {
			// We should handle host command failures gracefully. At the very least,
			// we'll need to turn off the UV light.
			// But later. Later.
			if err := cmds[i].Run(); err != nil {
				failf("Failed to execute command %+v: %v", cmds[i], err)
			}
			continue
		}
		lineno++
		cmd := gcode.AddLineAndHash(lineno, cmds[i].Text)
		fmt.Printf("%s\n", cmd)
		if _, err := fmt.Fprintf(conn, "%s\n", cmd); err != nil {
			failf("Writing to the serial port: %v", err)
		}
		waitForOK(reqCh, lineno)
	}
}
