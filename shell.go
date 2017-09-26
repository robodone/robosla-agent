package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/robodone/robosla-common/pkg/device_api"
)

type Shell struct {
	up           *Uplink
	exe          *Executor
	mu           sync.Mutex
	curJobCancel context.CancelFunc
}

func NewShell(up *Uplink, down Downlink, virtual bool) *Shell {
	return &Shell{
		up:  up,
		exe: NewExecutor(up, down, virtual),
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
		case "bash":
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			err := sh.Bash(ctx, parts[1:])
			cancel()
			if err != nil {
				sh.up.logf("Failed to run %q: %v", parts[1:], err)
			}
			continue
		case "cancel":
			sh.cancelJob()
			continue
		case "drop":
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			err := sh.exe.ExecuteFewCommands(ctx, "M106", "M107 P1", "G4 P400", "M106 P1")
			cancel()
			if err != nil {
				sh.up.logf("Failed to grip: %v", err)
			}
			continue
		case "grip":
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			err := sh.exe.ExecuteFewCommands(ctx, "M107")
			cancel()
			if err != nil {
				sh.up.logf("Failed to grip: %v", err)
			}
			continue
		case "cut":
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			err := sh.exe.ExecuteFewCommands(ctx, "M107", "G4 P400", "M106", "M107 P1", "G4 P400", "M106 P1")
			cancel()
			if err != nil {
				sh.up.logf("Failed to grip: %v", err)
			}
			continue
		case "fetch-and-print":
			// print <jobName> <archiveURL>
			ctx, err := sh.getNewJobContext()
			if err != nil {
				sh.up.NotifyJobDone(arg1, false, err.Error())
				return lastTS
			}
			go func(ctx context.Context, jobName, jobURL string) {
				var err error
				defer func() {
					var comment string
					if err == nil {
						comment = "OK"
					} else {
						comment = err.Error()
					}
					sh.clearCurrentJob()
					sh.up.NotifyJobDone(jobName, err == nil, comment)
				}()
				localGcodePath, err := sh.exe.FetchJob(ctx, jobURL)
				if err != nil {
					sh.up.logf("Failed to fetch %q: %v", jobURL, err)
					return
				}
				err = sh.exe.ExecuteGcode(ctx, jobName, localGcodePath)
				if err != nil {
					sh.up.logf("Failed to execute %q: %v", jobURL, err)
					return
				}
			}(ctx, arg1, arg2)
			continue
		case "realsense-train-pack":
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			packID := arg1
			graspID := arg2
			err := sh.RealSenseTrainPack(ctx, packID, graspID)
			cancel()
			if err != nil {
				sh.up.logf("Failed to make a RealSense train pack: %v", err)
				return lastTS
			}
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

		// Sending just a single g-code command to the printer. This is not cancelable yet.
		if err := sh.exe.down.WriteAndWaitForOK(context.TODO(), cmd); err != nil {
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

func (sh *Shell) getNewJobContext() (context.Context, error) {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.curJobCancel != nil {
		return nil, fmt.Errorf("job is already running")
	}
	var ctx context.Context
	ctx, sh.curJobCancel = context.WithCancel(context.Background())
	return ctx, nil
}

func (sh *Shell) clearCurrentJob() {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	sh.curJobCancel = nil
}

func (sh *Shell) cancelJob() {
	sh.mu.Lock()
	cancel := sh.curJobCancel
	sh.curJobCancel = nil
	sh.mu.Unlock()
	if cancel == nil {
		sh.up.logf("Nothing to cancel: no job is currently running.")
		return
	}
	cancel()
	sh.up.logf("Cancelation is requested.")
}

func (sh *Shell) Bash(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("empty command line")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	data, err := cmd.CombinedOutput()
	if len(data) > 0 {
		if len(data) > 8000 {
			data = data[:8000]
		}
		sh.up.logf("Output: %s", string(data))
	}
	return err
}

func (sh *Shell) RealSenseTrainPack(ctx context.Context, packID, graspID string) error {
	if packID == "" {
		return fmt.Errorf("RealSenseTrainPack(packID=%s, graspID=%s): packID not specified", packID, graspID)
	}
	if graspID == "" {
		return fmt.Errorf("RealSenseTrainPack(packID=%s, graspID=%s): graspID not specified", packID, graspID)
	}
	return fmt.Errorf("RealSenseTrainPack(packID=%s, graspID=%s): not implemented", packID, graspID)
}
