//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func newShellCommand(command string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", command)
}

func prepareCommand(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func terminateProcessTree(pid int32) error {
	group := -int(pid)
	if err := syscall.Kill(group, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	deadline := time.Now().Add(1200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(group, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	if err := syscall.Kill(group, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
