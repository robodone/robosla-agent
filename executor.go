package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robodone/robosla-common/pkg/autoupdate"
	"github.com/vincent-petithory/dataurl"
)

const numSaturationDelays = 20

type Executor struct {
	up      *Uplink
	down    Downlink
	virtual bool
	rss     *RealSenseSnapshotter
}

// NB: the caller MUST set downlink before using the executor.
func NewExecutor(up *Uplink, virtual bool, rss *RealSenseSnapshotter) *Executor {
	return &Executor{up: up, virtual: virtual, rss: rss}
}

func isCanceled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func (exe *Executor) ExecuteFewCommands(ctx context.Context, cmds ...string) (err error) {
	if !exe.down.Connected() {
		return errors.New("can't execute commands: printer not connected")
	}
	// Append with enough small delays to saturate the command buffer. That allows us to make sure,
	// that this function returns when all important commands are executed.
	for i := 0; i < numSaturationDelays; i++ {
		cmds = append(cmds, "G4 P1")
	}
	for i := 0; i < len(cmds); i++ {
		if isCanceled(ctx) {
			return context.Canceled
		}
		// TODO: support host commands this way.
		err := exe.down.WriteAndWaitForOK(ctx, cmds[i])
		if err != nil {
			return fmt.Errorf("failed to write a command: %v", err)
		}
	}
	return nil
}

func (exe *Executor) ExecuteGcode(ctx context.Context, jobName, gcodePath string) (err error) {
	if !exe.down.Connected() {
		return errors.New("can't execute gcode: printer not connected")
	}
	autoupdate.DisableUpdates()
	defer autoupdate.EnableUpdates()
	exe.up.SetJobName(jobName)
	defer exe.up.SetJobName("")

	exe.up.NotifyJobProgress(jobName, 0.01 /*progress*/, 0 /*elapsed*/, 0 /*remaining*/)

	// Home the printer before the download is completed. This will save us some time later.
	// Note: this is incompatible with other devices, like robotic arms. We will care about that later.
	// It will likely have different executors for each device kind.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := exe.down.WriteAndWaitForOK(ctx, "G28 Z0"); err != nil {
			exe.up.logf("Failed to home the printer. Error: %v", err)
		}
	}()

	cmds, numFrames, err := loadGcode(gcodePath)
	if err != nil {
		return fmt.Errorf("could not load gcode from %s: %v", gcodePath, err)
	}

	exe.up.NotifyJobProgress(jobName, 0.02 /*progress*/, 0 /*elapsed*/, 0 /*remaining*/)
	exe.up.NotifyFrameIndex(jobName, 0, numFrames)
	if isCanceled(ctx) {
		return context.Canceled
	}
	exe.up.logf("Loaded %d gcode commands from %s.", len(cmds), gcodePath)
	// Wait until it's homed.
	wg.Wait()

	if !exe.down.WaitForConnection(time.Minute) {
		return ErrNoDownlinkConnection
	}
	// Wait to allow the downlink to read all pending messages.
	time.Sleep(time.Second)

	// No matter what, if this function returns an error, we will try to turn off UV LED.
	defer func() {
		if err == nil {
			return
		}
		exe.up.NotifyJobProgress(jobName, 0, 0, 0)
		// Don't block it for more than 70*3 seconds.
		ctx, _ := context.WithTimeout(context.Background(), 70*time.Second)
		// Best effort.
		for _, cmd := range []string{"M107", "G1 Z170 F200", "M84"} {
			if err := exe.down.WriteAndWaitForOK(ctx, cmd); err != nil {
				exe.up.logf("Failed to run abort procedures. Error: %v", err)
			}
		}
	}()

	var lastProgress float64
	start := time.Now()
	var profileStart time.Time
	skipN := 10
	for i := 0; i < len(cmds); i++ {
		if isCanceled(ctx) {
			return context.Canceled
		}
		// Skip first skipN commands for to make estimates closer to the reality.
		if i >= skipN && profileStart.IsZero() {
			profileStart = time.Now()
		}
		progress := float64(int(float64(i*1000)/float64(len(cmds)))) / 10
		if progress == 0 {
			progress = 0.05
		}
		if progress > lastProgress {
			now := time.Now()
			elapsed := now.Sub(start)
			var remaining time.Duration
			if !profileStart.IsZero() {
				profileElapsed := now.Sub(profileStart)
				profileProgress := 100 * float64(i-skipN) / float64(len(cmds)-skipN)
				if profileProgress >= 0.3 {
					remaining = time.Duration(float64(profileElapsed) * (100 - profileProgress) / profileProgress)
				}
			}

			exe.up.NotifyJobProgress(jobName, progress, elapsed, remaining)
			lastProgress = progress
		}
		if cmds[i].IsHost() {
			// We should handle host command failures gracefully. At the very least,
			// we'll need to turn off the UV light.
			// But later. Later.
			if err := cmds[i].Run(jobName, numFrames, exe.up, exe.virtual); err != nil {
				return fmt.Errorf("failed to execute command %+v: %v", cmds[i], err)
			}
			continue
		}
		for {
			err := exe.down.WriteAndWaitForOK(ctx, cmds[i].Text)
			if err == nil {
				break
			}
			if err == ErrConnectionReset {
				exe.up.logf("Connection reset while printing. Sorry. There's nothing we can do about it.")
				return err
			}
			exe.up.logf("WriteAndWaitForOK failed: %v. Retrying...", err)
			if isCanceled(ctx) {
				return context.Canceled
			}
			if !exe.down.WaitForConnection(time.Minute) {
				return ErrNoDownlinkConnection
			}
		}
	}
	return nil
}

func (exe *Executor) FetchJob(ctx context.Context, jobURL string) (gcodePath string, err error) {
	exe.up.logf("Downloading a job from %s", jobURL)
	data, err := exe.getURL(ctx, jobURL)
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

func loadGcode(fname string) (cmds []*Cmd, numFrames int, err error) {
	data, err := ioutil.ReadFile(fname)
	if err != nil {
		return nil, 0, err
	}
	baseDir := path.Dir(fname)
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
			return nil, 0, fmt.Errorf("%s:%d: invalid gcode: %v", fname, lineno, err)
		}
		if cmd.Type == "M" && cmd.Idx == 7820 {
			frameIdx := int(cmd.Dict['S'])
			if numFrames < frameIdx {
				numFrames = frameIdx
			}
		}
		cmds = append(cmds, cmd)
	}
	return
}

func (exe *Executor) getURL(ctx context.Context, srcURL string) (res []byte, err error) {
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
	body, err := readAll(ctx, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP response: %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected HTTP status: %s %d. Want 200.", resp.Status, resp.StatusCode)
	}
	return body, nil
}

func readAll(ctx context.Context, r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	b := make([]byte, 128<<10)
	for {
		if isCanceled(ctx) {
			return nil, context.Canceled
		}
		n, err := r.Read(b)
		if n > 0 {
			buf.Write(b[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
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
		case 84:
			// Release motors
			asm()
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

func (cmd *Cmd) Run(jobName string, numFrames int, up *Uplink, virtual bool) error {
	if cmd.Type != "M" || cmd.Idx != 7820 {
		return fmt.Errorf("unsupported host command %s%d", cmd.Type, cmd.Idx)
	}
	// Show a new frame on the LCD.
	frameIdx := int(cmd.Dict['S'])
	fname := path.Join(cmd.BaseDir, fmt.Sprintf("frame-%06d.png", frameIdx))
	if !virtual {
		data, err := exec.Command("killall", "fbi").CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "killall fbi: %v, %v\n", string(data), err)
		}
		data, err = exec.Command("fbi", "-noverbose", "-a", "-T", "1", fname).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to display a frame: %v, %v", string(data), err)
		}
	}
	up.NotifyFrameIndex(jobName, frameIdx, numFrames)
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

func (exe *Executor) RealSenseTrainPack(ctx context.Context, packID, graspID string,
	x, y, z, roll, pitch, yaw float64) error {
	if exe.rss == nil {
		return errors.New("RealSense functionality is not enabled")
	}
	if packID == "" {
		return errors.New("packID not specified")
	}
	if !isHexID(packID) {
		return errors.New("packID is not a valid hex ID")
	}
	if graspID == "" {
		return errors.New("graspID not specified")
	}
	if !isHexID(graspID) {
		return errors.New("graspID is not a valid hex ID")
	}
	packDir := path.Join("/opt/robodone/realsense/", graspID, packID)
	if err := os.MkdirAll(packDir, 0777); err != nil {
		return fmt.Errorf("failed to create a directory for a pack of snapshots")
	}
	exe.up.logf("Pack dir %s created", packDir)
	prefix := path.Join(packDir, packID) + "-"

	numFrames := 5
	if err := exe.rss.TakeSnapshot(ctx, prefix, numFrames); err != nil {
		return fmt.Errorf("failed to take a RealSense snapshot (%d frames): %v", numFrames, err)
	}
	// Now, it's time to write parameters.json with the pose and possibly other values.
	p := &RealSenseTrainPackParams{
		PackID:    packID,
		GraspID:   graspID,
		X:         x,
		Y:         y,
		Z:         z,
		Roll:      roll,
		Pitch:     pitch,
		Yaw:       yaw,
		NumFrames: numFrames,
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize RealSense Train Pack params to JSON: %v", err)
	}
	if err := ioutil.WriteFile(path.Join(packDir, "parameters.json"), data, 0644); err != nil {
		return fmt.Errorf("failed to write RealSense Train Pack params to the disk: %v", err)
	}
	return nil
}

func (exe *Executor) Snapshot(ctx context.Context) error {
	if exe.rss == nil {
		return errors.New("RealSense functionality is not enabled")
	}
	dirName, err := ioutil.TempDir("", "robosla-shell-snapshot-")
	if err != nil {
		return fmt.Errorf("failed to create a temp directory")
	}
	exe.up.logf("Temp dir %s created", dirName)
	// TODO(krasin): remove the temp directory.
	//defer os.RemoveAll(dirName)

	prefix := path.Join(dirName, "realsense-")
	if err := exe.rss.TakeSnapshot(ctx, prefix, 1 /*numFrames*/); err != nil {
		return fmt.Errorf("failed to take a RealSense snapshot: %v", err)
	}

	// Scan the directory and load all images into a map.
	fnames, err := getImageNames(dirName)
	if err != nil {
		return fmt.Errorf("failed to list images in %s: %v", dirName, err)
	}
	cameras := make(map[string]string)
	for _, fname := range fnames {
		data, err := ioutil.ReadFile(path.Join(dirName, fname))
		if err != nil {
			return fmt.Errorf("failed to load a camera frame from %s: %v", fname, err)
		}
		cameras[fname[:len(fname)-len(path.Ext(fname))]] = dataurl.EncodeBytes(data)
	}
	exe.up.NotifySnapshot(cameras)

	return nil
}

func isHexID(str string) bool {
	if len(str) != 16 {
		return false
	}
	if _, err := strconv.ParseUint(str, 16, 64); err != nil {
		return false
	}
	return true
}
