package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/samofly/serial"
)

var (
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
		switch num {
		case 28:
			// G28. Homing. Only support Z homing for now.
			// F is a feed rate in units per minute.
			asm('Z', 'F')
		default:
			return nil, fmt.Errorf("unsupported command G%d", num)
		}
	}
	if _, ok := m['M']; ok {
		// Filter M commands.
		// TODO(krasin): make it more rigor.
		num := int(m['M'] + 0.5)
		switch num {
		default:
			return nil, fmt.Errorf("unsupported command M%d", num)
		}
	}
	if text == "" {
		return nil, fmt.Errorf("failed to parse line %q: generated text is empty. A parser bug?", line)
	}
	return &Cmd{Text: text}, nil
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

func main() {
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
	for i := 0; i < 100 && i < len(cmds); i++ {
		logf("#%d: %s", i+1, cmds[i].Text)
	}
}
