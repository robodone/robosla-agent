package main

import (
	"encoding/json"
	"fmt"
	"log"
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
}

func NewUplink(apiServerAddr string) *Uplink {
	return &Uplink{apiServerAddr: apiServerAddr, nd: pubsub.NewNode()}
}

func (up *Uplink) getClient() *device_api.Client {
	up.mu.Lock()
	defer up.mu.Unlock()
	return up.client
}

func (up *Uplink) setClientAndDeviceName(client *device_api.Client, deviceName string) {
	up.mu.Lock()
	defer up.mu.Unlock()
	up.client = client
	up.deviceName = deviceName
}

func (up *Uplink) Run() {
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
		deviceName, err := client.Hello(deviceCookie)
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
func (up *Uplink) Notify(out string) {
	// We don't want to block anyone with these updates.
	// Starting goroutines mean that the updates may potentially be
	// delivered out of order, but we don't enforce it anyway.
	go up.notify(out)
}

func (up *Uplink) notify(out string) {
	client := up.getClient()
	if client == nil {
		// We are not connected. Two options: postpone sending those updates,
		// or just forget about them. Let's just forget. They are low value.
		return
	}
	err := client.SendTerminalOutput(out)
	if err != nil {
		// TODO(krasin): rate limit messages from here.
		log.Printf("Failed to send terminal output: %v", err)
	}
}

func (up *Uplink) WaitForConnection() {
	for up.getClient() == nil {
		time.Sleep(time.Second)
	}
}

func (up *Uplink) NotifyJobDone(jobName string, success bool, comment string) {
	up.Notify(up.bestJson(&device_api.UplinkMessage{
		Type:    "notify-job-done",
		JobName: jobName,
		Success: success,
		Comment: comment,
	}))
}

func (up *Uplink) NotifyJobProgress(jobName string, progress float64) {
	up.Notify(up.bestJson(&device_api.UplinkMessage{
		Type:     "notify-job-progress",
		JobName:  jobName,
		Progress: progress,
	}))
}

func (up *Uplink) logf(format string, args ...interface{}) {
	format = strings.TrimRight(format, "\n")
	logf(format, args...)
	up.Notify(fmt.Sprintf(format, args...))
}

func (up *Uplink) bestJson(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		up.logf("json.Marshal(%+v): %v", v, err)
		return "{}"
	}
	return string(data)
}
