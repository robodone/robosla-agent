package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
)

func mustGetExecutablePath() string {
	execPath, err := os.Executable()
	if err != nil {
		log.Fatalf("failed to get the path to the current executable: %v", err)
	}
	return execPath
}

func getUserJsonPath() string {
	return path.Join(path.Dir(mustGetExecutablePath()), "user.json")
}

func getDeviceJsonPath() string {
	return path.Join(path.Dir(mustGetExecutablePath()), "device.json")
}

func readCookie(fname string) (string, error) {
	data, err := ioutil.ReadFile(fname)
	if err != nil {
		return "", err
	}
	var m map[string]interface{}
	if err = json.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("failed to parse json: %v", err)
	}
	val, ok := m["cookie"]
	if !ok {
		return "", fmt.Errorf("no cookie in %s", fname)
	}
	cookie, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("invalid %s: cookie is not a string", fname)
	}
	return cookie, nil
}

func readUserCookie() (string, error) {
	return readCookie(getUserJsonPath())
}

func readDeviceCookie() (string, error) {
	return readCookie(getDeviceJsonPath())
}

func saveDeviceCookie(cookie string) error {
	m := make(map[string]interface{})
	m["cookie"] = cookie
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(getDeviceJsonPath(), data, 0644)
}

func isFirstRun() (bool, error) {
	// In the first run, we have user.json, but not device.json near the binary.
	if _, err := os.Stat(getUserJsonPath()); err != nil {
		return false, fmt.Errorf("failed to access user.json: %v", err)
	}
	_, err := os.Stat(getDeviceJsonPath())
	if err == nil {
		// This is not the first run, as we have already generated the device cookie.
		return false, nil
	}
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, err
}
