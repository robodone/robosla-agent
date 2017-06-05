package main

import (
	"fmt"
	"time"
)

type Executor struct {
	up   *Uplink
	down *Downlink
}

func NewExecutor(up *Uplink, down *Downlink) *Executor {
	return &Executor{up: up, down: down}
}

func (exe *Executor) Run() error {
	// TODO(krasin): implement
	return nil
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
