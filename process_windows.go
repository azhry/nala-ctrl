//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
)

const createNoWindow = 0x08000000

func newShellCommand(command string) *exec.Cmd {
	return exec.Command("cmd.exe", "/d", "/s", "/c", command)
}

func prepareCommand(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}
	return nil
}

func terminateProcessTree(pid int32) error {
	command := exec.Command("taskkill.exe", "/PID", strconv.Itoa(int(pid)), "/T", "/F")
	if err := prepareCommand(command); err != nil {
		return err
	}
	output, err := command.CombinedOutput()
	if err != nil {
		message := string(output)
		if len(message) > 240 {
			message = message[:240]
		}
		return fmt.Errorf("taskkill failed: %w %s", err, message)
	}
	return nil
}
