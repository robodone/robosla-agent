package main

import (
	"log"
	"time"

	"github.com/robodone/robosla-common/pkg/device_api"
)

func RunUplink() {
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
			log.Fatalf("Faield to read device.json: %v", err)
		}
		deviceName, err := client.Hello(deviceCookie)
		if err != nil {
			log.Fatalf("Hello: %v", err)
		}
		log.Printf("deviceName: %s\n", deviceName)
		sub, err := client.SubString("never/happen")
		if err != nil {
			log.Fatalf("Failed to subscribe: %v", err)
		}
		// It will return when an underlying connection is closed.
		<-sub.C()

		// Avoid immediate reconnects.
		time.Sleep(5 * time.Second)
	}
}
