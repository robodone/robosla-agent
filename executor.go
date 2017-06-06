package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	exe.up.logf("Received gcode update: %+v", reqJson)
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
		// TODO(krasin): make 'print' less hacky
		prefix := "print "
		if strings.HasPrefix(cmd, prefix) {
			// Print file
			fname := cmd[len(prefix):]
			err := exe.ExecuteGcode(fname)
			if err != nil {
				exe.up.logf("Failed to execute %q: %v", err)
				return lastTS
			}
		}
		if err := exe.down.WriteAndWaitForOK(cmd); err != nil {
			exe.up.logf("Error while sending gcode: %v", err)
			return lastTS
		}
	}
	return lastTS
}

func (exe *Executor) ExecuteGcode(path string) error {
	cmds, err := loadGcode(*gcodePath)
	if err != nil {
		failf("Could not load gcode from %s: %v", *gcodePath, err)
	}
	logf("Loaded %d gcode commands from %s.", len(cmds), *gcodePath)
	exe.down.WaitForConnection()
	// Wait to allow the downlink to read all pending messages.
	time.Sleep(time.Second)

	for i := 0; i < len(cmds); i++ {
		if cmds[i].IsHost() {
			// We should handle host command failures gracefully. At the very least,
			// we'll need to turn off the UV light.
			// But later. Later.
			if err := cmds[i].Run(); err != nil {
				return fmt.Errorf("Failed to execute command %+v: %v", cmds[i], err)
			}
			continue
		}
		for {
			if err := exe.down.WriteAndWaitForOK(cmds[i].Text); err == nil {
				break
			}
			exe.down.WaitForConnection()
		}
	}
	return nil
}
