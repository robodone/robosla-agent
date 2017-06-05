package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robodone/robosla-common/pkg/device_api"
)

type Uplink struct {
	apiServerAddr string
	mu            sync.Mutex
	client        *device_api.Client
}

func NewUplink(apiServerAddr string) *Uplink {
	return &Uplink{apiServerAddr: apiServerAddr}
}

func (up *Uplink) getClient() *device_api.Client {
	up.mu.Lock()
	defer up.mu.Unlock()
	return up.client
}

func (up *Uplink) setClient(client *device_api.Client) {
	up.mu.Lock()
	defer up.mu.Unlock()
	up.client = client
}

func (up *Uplink) Run() {
	for {
		if up.getClient() != nil {
			up.setClient(nil)
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
		client := device_api.NewClient(conn)
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
		log.Printf("deviceName: %s\n", deviceName)
		up.setClient(client)
		sub, err := client.SubString("never/happen")
		if err != nil {
			log.Printf("Failed to subscribe to never/happen: %v", err)
			continue
		}
		// It will return when an underlying connection is closed.
		<-sub.C()
	}
}

// Notify makes best effort to notify about the received terminal output or errors.
func (up *Uplink) Notify(out string) {
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

func (up *Uplink) logf(format string, args ...interface{}) {
	format = strings.TrimRight(format, "\n")
	format += "\n"
	logf(format, args...)
	up.Notify(fmt.Sprintf(format, args...))
}
