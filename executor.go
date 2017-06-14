package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/robodone/robosla-common/pkg/autoupdate"
	"github.com/robodone/robosla-common/pkg/device_api"
)

type Executor struct {
	up   *Uplink
	down *Downlink
}

func NewExecutor(up *Uplink, down *Downlink) *Executor {
	return &Executor{up: up, down: down}
}

func (exe *Executor) Run() error {
	sub, err := exe.up.Sub("ts.gcode")
	if err != nil {
		return fmt.Errorf("Failed to subscribe to ts.gcode: %v", err)
	}
	var lastTS int64
	for reqJson := range sub.C() {
		lastTS = exe.processGcodeUpdates(reqJson, lastTS)
	}
	return nil
}

func (exe *Executor) processGcodeUpdates(reqJson string, lastTS int64) int64 {
	var resp device_api.Response
	if err := json.Unmarshal([]byte(reqJson), &resp); err != nil {
		exe.up.logf("Failed to parse json with gcode: %v", err)
		return lastTS
	}
	var cmds []string
	for _, v := range resp.TS.Gcode {
		if v.TS <= lastTS {
			continue
		}
		cmds = append(cmds, v.Value)
		lastTS = v.TS
	}
	for _, cmd := range cmds {
		cmd = strings.TrimSpace(cmd)
		parts := strings.Split(cmd, " ")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		verb := parts[0]
		var arg1, arg2 string
		if len(parts) > 1 {
			arg1 = parts[1]
		}
		if len(parts) > 2 {
			arg2 = parts[2]
		}
		switch verb {
		case "print":
			err := exe.ExecuteGcode(arg1)
			if err != nil {
				exe.up.logf("Failed to execute %q: %v", arg1, err)
				return lastTS
			}
			continue
		case "fetch":
			localGcodePath, err := exe.FetchJob(arg1)
			if err != nil {
				exe.up.logf("Failed to fetch %q: %v", arg1, err)
				return lastTS
			}
			exe.up.logf("Success. Job fetched into %s", localGcodePath)
			continue
		case "fetch-and-print":
			// fetch-and-print <jobName> <archiveURL>
			localGcodePath, err := exe.FetchJob(arg2)
			if err != nil {
				exe.up.logf("Failed to fetch %q: %v", arg2, err)
				return lastTS
			}
			err = exe.ExecuteGcode(localGcodePath)
			if err != nil {
				exe.up.logf("Failed to execute %q: %v", arg2, err)
			}
			var status string
			if err == nil {
				status = "OK"
			} else {
				status = err.Error()
			}
			exe.up.NotifyJobDone(arg1, err == nil, status)
			continue
		case "reboot", "restart":
			err := exe.Reboot()
			if err != nil {
				exe.up.logf("Failed to reboot: %v", err)
				return lastTS
			}
			continue
		case "version":
			exe.up.PrintVersion()
			continue
		}

		if err := exe.down.WriteAndWaitForOK(cmd); err != nil {
			exe.up.logf("Error while sending gcode: %v", err)
			return lastTS
		}
	}
	return lastTS
}

func (exe *Executor) ExecuteGcode(gcodePath string) error {
	if !exe.down.Connected() {
		return errors.New("can't execute gcode: printer not connected")
	}
	autoupdate.DisableUpdates()
	defer autoupdate.EnableUpdates()

	cmds, err := loadGcode(gcodePath)
	if err != nil {
		return fmt.Errorf("could not load gcode from %s: %v", gcodePath, err)
	}
	exe.up.logf("Loaded %d gcode commands from %s.", len(cmds), gcodePath)
	if !exe.down.WaitForConnection(time.Minute) {
		return ErrNoDownlinkConnection
	}
	// Wait to allow the downlink to read all pending messages.
	time.Sleep(time.Second)

	for i := 0; i < len(cmds); i++ {
		if cmds[i].IsHost() {
			// We should handle host command failures gracefully. At the very least,
			// we'll need to turn off the UV light.
			// But later. Later.
			if err := cmds[i].Run(); err != nil {
				return fmt.Errorf("failed to execute command %+v: %v", cmds[i], err)
			}
			continue
		}
		for {
			err := exe.down.WriteAndWaitForOK(cmds[i].Text)
			if err == nil {
				break
			}
			if err == ErrConnectionReset {
				exe.up.logf("Connection reset while printing. Sorry. There's nothing we can do about it.")
				return err
			}
			exe.up.logf("WriteAndWaitForOK failed: %v. Retrying...", err)
			if !exe.down.WaitForConnection(time.Minute) {
				return ErrNoDownlinkConnection
			}
		}
	}
	return nil
}

func (exe *Executor) FetchJob(jobURL string) (gcodePath string, err error) {
	exe.up.logf("Downloading a job from %s", jobURL)
	data, err := exe.getURL(jobURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch a job from %q: %v", jobURL, err)
	}
	// Make a best effort to create the dir for jobs.
	os.MkdirAll("/opt/robodone/jobs", 0755)
	// Make a best effort to delete old jobs.
	if err := tryToRemoveOldJobs("/opt/robodone/jobs"); err != nil {
		exe.up.logf("Failed to remove old jobs: %v. Proceeding, like it didn't happen.", err)
	}
	dir, err := ioutil.TempDir("/opt/robodone/jobs", "job")
	if err != nil {
		return "", fmt.Errorf("failed to create a directory for a job: %v", err)
	}
	if err := ioutil.WriteFile(path.Join(dir, "job.zip"), data, 0644); err != nil {
		return "", fmt.Errorf("failed to write an archive with a job to disk: %v", err)
	}
	cmd := exec.Command("unzip", "job.zip")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to unzip the archive with a job: %v\nOutput:\n%s\n", err, string(out))
	}
	if err = os.Remove(path.Join(dir, "job.zip")); err != nil {
		return "", fmt.Errorf("failed to remove the archive with a job after it's extracted: %v", err)
	}
	gcodePath = path.Join(dir, "job.gcode")
	return
}

func (exe *Executor) Reboot() error {
	exe.up.logf("Rebooting Raspberry Pi...")
	// Allow the delivery of the message above.
	time.Sleep(time.Second)
	data, err := exec.Command("reboot").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to reboot: %v\nOutput:\n%s", err, string(data))
	}
	return nil
}

func loadGcode(fname string) ([]*Cmd, error) {
	data, err := ioutil.ReadFile(fname)
	if err != nil {
		return nil, err
	}
	baseDir := path.Dir(fname)
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
		cmd, err := parseGcodeCommand(baseDir, line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: invalid gcode: %v", fname, lineno, err)
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}

func (exe *Executor) getURL(srcURL string) (res []byte, err error) {
	// Validate url to make sure no malware is downloaded this way.
	// Theoretically, we are dealing with secure connections, but
	// the users are conned very easily. So, no.
	purl, err := url.Parse(srcURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url %q: %v", srcURL, err)
	}
	purl.Path = path.Clean(purl.Path)
	if purl.Hostname() != "storage.googleapis.com" ||
		!strings.HasPrefix(purl.Path, "/robosla-data/") {
		return nil, errors.New("downloading arbitrary urls is disabled for security reasons. " +
			"Let us know if you need this functionality by writing at beta@robodone.com")
	}
	cleanURL := purl.String()

	start := time.Now()
	defer func() {
		if err == nil {
			exe.up.logf("Download took %.1f seconds", time.Now().Sub(start).Seconds())
		}
	}()
	resp, err := http.Get(cleanURL)
	if err != nil {
		return nil, fmt.Errorf("http.Get(%q): %v", cleanURL, err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP response: %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected HTTP status: %s %d. Want 200.", resp.Status, resp.StatusCode)
	}
	return body, nil
}

func parseGcodeCommand(baseDir, line string) (*Cmd, error) {
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
	return &Cmd{Text: text, Type: typ, Idx: idx, Dict: m, BaseDir: baseDir}, nil
}

type Cmd struct {
	Text string
	Type string
	Idx  int
	Dict map[byte]float64

	// BaseDir is useful for locating frames. It's the directory where the job gcode file is located.
	BaseDir string
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
	fname := path.Join(cmd.BaseDir, fmt.Sprintf("frame-%06d.png", frameIdx))
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

func tryToRemoveOldJobs(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to access the jobs directory %q: %v", dir, err)
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return fmt.Errorf("failed to list jobs in %q: %v", dir, err)
	}
	var firstErr error
	for _, name := range names {
		if !strings.HasPrefix(name, "job") {
			// Some other file; not a job.
			continue
		}
		// Best effort to remove the job and everything inside
		if err := os.RemoveAll(path.Join(dir, name)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
