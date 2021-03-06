package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/robodone/robosla-common/pkg/device_api"
	"github.com/robodone/robosla-common/pkg/pubsub"
)

type Uplink struct {
	apiServerAddr string
	nd            *pubsub.Node
	mu            sync.Mutex
	client        *device_api.Client
	deviceName    string
	// This is likely not an appropriate place, but I don't have good ideas right now.
	jobName  string
	notifyCh chan *device_api.UplinkMessage

	// Pending logs
	pendingLogsMu    sync.Mutex
	pendingLogs      []string
	pendingLogsStart time.Time
}

func NewUplink(apiServerAddr string) *Uplink {
	return &Uplink{apiServerAddr: apiServerAddr, nd: pubsub.NewNode(), notifyCh: make(chan *device_api.UplinkMessage, 20)}
}

func (up *Uplink) getClient() *device_api.Client {
	up.mu.Lock()
	defer up.mu.Unlock()
	return up.client
}

func (up *Uplink) DeviceName() string {
	up.mu.Lock()
	defer up.mu.Unlock()
	return up.deviceName
}

func (up *Uplink) WaitForDeviceName() string {
	for {
		deviceName := up.DeviceName()
		if deviceName != "" {
			return deviceName
		}
		up.logf("Waiting for device name...")
		time.Sleep(2 * time.Second)
	}
}

func (up *Uplink) setClientAndDeviceName(client *device_api.Client, deviceName string) {
	up.mu.Lock()
	defer up.mu.Unlock()
	up.client = client
	up.deviceName = deviceName
}

func (up *Uplink) getJobName() string {
	up.mu.Lock()
	defer up.mu.Unlock()
	return up.jobName
}

func (up *Uplink) SetJobName(jobName string) {
	up.mu.Lock()
	defer up.mu.Unlock()
	up.jobName = jobName
}

func (up *Uplink) Run() {
	go up.runNotify()
	go up.runKeepAlive()
	go up.runFlushLogs(time.Second)
	for {
		if up.getClient() != nil {
			up.setClientAndDeviceName(nil, "")
			// Avoid immediate reconnects.
			time.Sleep(5 * time.Second)
		}

		var conn device_api.Conn
		var err error
		for {
			conn, err = device_api.ConnectWS(up.apiServerAddr)
			if err == nil {
				break
			}
			log.Printf("Failed to connect to the API server: %v. Will try again in a minute.", err)
			time.Sleep(time.Minute)
		}
		log.Printf("Connected to %s", up.apiServerAddr)
		client := device_api.NewClient(conn, up.nd)
		firstRun, err := isFirstRun()
		if err != nil {
			log.Fatalf("isFirstRun: %v", err)
		}
		if firstRun {
			userCookie, err := readUserCookie()
			if err != nil {
				log.Fatalf("Unable to read user cookie: %v", err)
			}
			deviceCookie, err := client.RegisterDevice(userCookie)
			if err != nil {
				log.Fatalf("Failed to register the current device: %v", err)
			}
			if err := saveDeviceCookie(deviceCookie); err != nil {
				log.Fatalf("Failed to save device.json: %v", err)
			}
		}
		deviceCookie, err := readDeviceCookie()
		if err != nil {
			log.Fatalf("Failed to read device.json: %v", err)
		}
		deviceName, err := client.Hello(deviceCookie, up.getJobName())
		if err != nil {
			log.Fatalf("Hello: %v", err)
		}
		up.setClientAndDeviceName(client, deviceName)
		up.PrintVersion()
		// It will return when an underlying connection is closed.
		<-client.Stopped()
	}
}

func (up *Uplink) PrintVersion() {
	up.logf("RoboSLA agent version %s running on printer %s", Version, up.deviceName)
}

func (up *Uplink) Sub(paths ...string) (*pubsub.Sub, error) {
	return up.nd.Sub(paths...)
}

// Notify makes best effort to notify about the received terminal output or errors.
func (up *Uplink) Notify(msg *device_api.UplinkMessage) {
	up.notifyCh <- msg
}

func (up *Uplink) runNotify() {
	var pending []*device_api.UplinkMessage
	inProgress := false
	doneCh := make(chan bool)
	for {
		var msg *device_api.UplinkMessage
		if inProgress {
			select {
			case msg = <-up.notifyCh:
			case <-doneCh:
				inProgress = false
				if len(pending) == 0 {
					continue
				}
				msg = pending[0]
				pending = pending[1:]
			}
		} else {
			msg = <-up.notifyCh
		}
		if inProgress {
			pending = append(pending, msg)
			continue
		}
		inProgress = true
		go func(msg *device_api.UplinkMessage) {
			defer func() { doneCh <- true }()
			client := up.getClient()
			if client == nil {
				// We are not connected. Two options: postpone sending those updates,
				// or just forget about them. Let's just forget. They are low value.
				return
			}
			err := client.Notify(msg)
			if err != nil {
				// TODO(krasin): rate limit messages from here.
				log.Printf("Failed to notify: %v", err)
			}
		}(msg)
	}
}

func (up *Uplink) runKeepAlive() {
	for {
		up.WaitForConnection()
		up.logf("keep-alive")
		time.Sleep(time.Minute)
	}
}

func (up *Uplink) WaitForConnection() {
	for up.getClient() == nil {
		time.Sleep(time.Second)
	}
}

func (up *Uplink) NotifyJobDone(jobName string, success bool, comment string) {
	up.Notify(&device_api.UplinkMessage{
		Type:    "notify-job-done",
		JobName: jobName,
		Success: success,
		Comment: comment,
	})
}

func (up *Uplink) NotifyJobProgress(jobName string, progress float64, elapsed, remaining time.Duration) {
	up.Notify(&device_api.UplinkMessage{
		Type:      "notify-job-progress",
		JobName:   jobName,
		Progress:  progress,
		Elapsed:   elapsed,
		Remaining: remaining,
	})
}

func (up *Uplink) NotifyFrameIndex(jobName string, frameIndex, numFrames int) {
	up.Notify(&device_api.UplinkMessage{
		Type:       "notify-frame-index",
		JobName:    jobName,
		FrameIndex: frameIndex,
		NumFrames:  numFrames,
	})
}

func (up *Uplink) NotifySnapshot(cameras map[string]string) {
	up.Notify(&device_api.UplinkMessage{
		Type:    "notify-snapshot",
		Cameras: cameras,
	})
}

func (up *Uplink) NotifyMovingState(state string, pose []float64) {
	up.Notify(&device_api.UplinkMessage{
		Type:        "notify-moving-state",
		MovingState: state,
		Pose:        pose,
	})
}

func (up *Uplink) NotifyGripperState(state string) {
	up.Notify(&device_api.UplinkMessage{
		Type:         "notify-gripper-state",
		GripperState: state,
	})
}

func (up *Uplink) Logf(format string, args ...interface{}) {
	up.logf(format, args...)
}

func (up *Uplink) logf(format string, args ...interface{}) {
	up.pendingLogsMu.Lock()
	defer up.pendingLogsMu.Unlock()
	format = strings.TrimRight(format, "\n")
	if len(up.pendingLogs) == 0 {
		up.pendingLogsStart = time.Now()
	}
	up.pendingLogs = append(up.pendingLogs, fmt.Sprintf(format, args...))
	logf(format, args...)
}

func (up *Uplink) runFlushLogs(delay time.Duration) {
	for {
		time.Sleep(delay)
		up.flushLogs(delay)
	}
}

func (up *Uplink) flushLogs(delay time.Duration) {
	up.pendingLogsMu.Lock()
	defer up.pendingLogsMu.Unlock()

	if len(up.pendingLogs) == 0 {
		// Already flushed.
		return
	}
	if time.Now().Sub(up.pendingLogsStart) < delay/2 {
		// Too fresh logs. It's worth waiting for longer.
		return
	}

	up.Notify(&device_api.UplinkMessage{
		Type:           "notify-terminal-output",
		TerminalOutput: strings.Join(up.pendingLogs, "\n"),
	})
	up.pendingLogs = nil
}

func (up *Uplink) Fatalf(format string, args ...interface{}) {
	// Allow robosla agent to setup (useful for the Fatalf calls happening in the very beginning of the program)
	time.Sleep(5 * time.Second)
	up.logf("FATAL: "+format, args...)
	// Allow uplink to write to websocket.
	time.Sleep(5 * time.Second)
	os.Exit(1)
}

func (up *Uplink) bestJson(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		up.logf("json.Marshal(%+v): %v", v, err)
		return "{}"
	}
	return string(data)
}

func getImageNames(dirName string) ([]string, error) {
	f, err := os.Open(dirName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fnames, err := f.Readdirnames(0)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %v", err)
	}
	var imageFnames []string
	for _, fname := range fnames {
		switch path.Ext(fname) {
		case ".png", ".jpg", ".jpeg":
			imageFnames = append(imageFnames, fname)
		default:
		}
	}
	return imageFnames, nil
}
