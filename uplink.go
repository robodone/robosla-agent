package main

import (
	"log"
	"sync"
	"time"

	"github.com/robodone/robosla-common/pkg/device_api"
)

type Uplink struct {
	mu     sync.Mutex
	client *device_api.Client
}

func NewUplink() *Uplink {
	return &Uplink{}
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
		var conn device_api.Conn
		var err error
		for {
			conn, err = device_api.ConnectWS(*apiServer)
			if err == nil {
				break
			}
			log.Printf("Failed to connect to the API server: %v. Will try again in a minute.", err)
			time.Sleep(time.Minute)
		}
		log.Printf("Connected to %s", *apiServer)
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
			log.Fatalf("Failed to subscribe: %v", err)
		}
		// It will return when an underlying connection is closed.
		<-sub.C()
		up.setClient(nil)

		// Avoid immediate reconnects.
		time.Sleep(5 * time.Second)
	}
}

// NotifyGcode makes best effort to notify about the received terminal output.
func (up *Uplink) NotifyTerminalOutput(out string) {
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
