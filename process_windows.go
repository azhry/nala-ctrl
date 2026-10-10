//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strconv"
)

func newShellCommand(command string) *exec.Cmd {
	return exec.Command("cmd.exe", "/d", "/s", "/c", command)
}

func prepareCommand(command *exec.Cmd) error {
	return nil
}

func terminateProcessTree(pid int32) error {
	output, err := exec.Command("taskkill.exe", "/PID", strconv.Itoa(int(pid)), "/T", "/F").CombinedOutput()
	if err != nil {
		message := string(output)
		if len(message) > 240 {
			message = message[:240]
		}
		return fmt.Errorf("taskkill failed: %w %s", err, message)
	}
	return nil
}
