package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/robodone/robosla-common/pkg/device_api"
)

type Shell struct {
	up  *Uplink
	exe *Executor
}

func NewShell(up *Uplink, down *Downlink) *Shell {
	return &Shell{
		up:  up,
		exe: NewExecutor(up, down),
	}
}

func (sh *Shell) Run() error {
	sub, err := sh.up.Sub("ts.gcode")
	if err != nil {
		return fmt.Errorf("Failed to subscribe to ts.gcode: %v", err)
	}
	var lastTS int64
	for reqJson := range sub.C() {
		lastTS = sh.processGcodeUpdates(reqJson, lastTS)
	}
	return nil
}

func (sh *Shell) processGcodeUpdates(reqJson string, lastTS int64) int64 {
	var resp device_api.Response
	if err := json.Unmarshal([]byte(reqJson), &resp); err != nil {
		sh.up.logf("Failed to parse json with gcode: %v", err)
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
			err := sh.exe.ExecuteGcode(arg1)
			if err != nil {
				sh.up.logf("Failed to execute %q: %v", arg1, err)
				return lastTS
			}
			continue
		case "fetch":
			localGcodePath, err := sh.exe.FetchJob(arg1)
			if err != nil {
				sh.up.logf("Failed to fetch %q: %v", arg1, err)
				return lastTS
			}
			sh.up.logf("Success. Job fetched into %s", localGcodePath)
			continue
		case "fetch-and-print":
			// fetch-and-print <jobName> <archiveURL>
			localGcodePath, err := sh.exe.FetchJob(arg2)
			if err != nil {
				sh.up.logf("Failed to fetch %q: %v", arg2, err)
				return lastTS
			}
			err = sh.exe.ExecuteGcode(localGcodePath)
			if err != nil {
				sh.up.logf("Failed to execute %q: %v", arg2, err)
			}
			var comment string
			if err == nil {
				comment = "OK"
			} else {
				comment = err.Error()
			}
			sh.up.NotifyJobDone(arg1, err == nil, comment)
			continue
		case "reboot", "restart":
			err := sh.Reboot()
			if err != nil {
				sh.up.logf("Failed to reboot: %v", err)
				return lastTS
			}
			continue
		case "version":
			sh.up.PrintVersion()
			continue
		}

		if err := sh.exe.down.WriteAndWaitForOK(cmd); err != nil {
			sh.up.logf("Error while sending gcode: %v", err)
			return lastTS
		}
	}
	return lastTS
}

func (sh *Shell) Reboot() error {
	sh.up.logf("Rebooting Raspberry Pi...")
	// Allow the delivery of the message above.
	time.Sleep(time.Second)
	data, err := exec.Command("reboot").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to reboot: %v\nOutput:\n%s", err, string(data))
	}
	return nil
}
