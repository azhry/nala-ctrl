package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	dockerDesktopLogQueryTimeout = 12 * time.Second
	dockerDesktopLogTailBytes    = 1 << 20
	dockerWSLRepairTimeout       = 30 * time.Second
)

type dockerLogTail struct {
	data []byte
}

func (b *dockerLogTail) Write(data []byte) (int, error) {
	written := len(data)
	if len(data) >= dockerDesktopLogTailBytes {
		b.data = append(b.data[:0], data[len(data)-dockerDesktopLogTailBytes:]...)
		return written, nil
	}

	if overflow := len(b.data) + len(data) - dockerDesktopLogTailBytes; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, data...)
	return written, nil
}

func repairKnownDockerSecretsSocket(ctx context.Context, dockerPath string) (bool, error) {
	logsCtx, cancelLogs := context.WithTimeout(ctx, dockerDesktopLogQueryTimeout)
	defer cancelLogs()

	var logs dockerLogTail
	logCommand := exec.CommandContext(
		logsCtx,
		dockerPath,
		"desktop", "logs", "--boot", "0", "--since", "24h", "--no-color",
	)
	logCommand.Stdout = &logs
	logCommand.Stderr = io.Discard
	if err := logCommand.Run(); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	if !hasKnownDockerSecretsSocketError(string(logs.data)) {
		return false, nil
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" || !filepath.IsAbs(localAppData) {
		return false, errors.New("Docker's known startup failure was detected, but its local data folder could not be located.")
	}
	socketPath := filepath.Join(localAppData, "docker-secrets-engine", "engine.sock")
	if !isDockerSecretsSocketReparsePoint(socketPath) {
		return false, nil
	}
	if dockerDesktopEngineReady(ctx, dockerPath) {
		return false, nil
	}

	wslPath, err := exec.LookPath("wsl.exe")
	if err != nil {
		return false, errors.New("Docker's known startup failure was detected, but WSL is unavailable to move the blocked socket aside.")
	}
	wslCtx, cancelWSL := context.WithTimeout(ctx, dockerWSLRepairTimeout)
	defer cancelWSL()

	source, err := windowsPathToWSL(wslCtx, wslPath, socketPath)
	if err != nil {
		return false, errors.New("Docker's blocked secrets socket could not be prepared for repair. Check that WSL is available, then try again.")
	}
	backupPath := socketPath + ".codex-stale-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	destination, err := windowsPathToWSL(wslCtx, wslPath, backupPath)
	if err != nil {
		return false, errors.New("Docker's blocked secrets socket could not be prepared for repair. Check that WSL is available, then try again.")
	}
	if dockerDesktopEngineReady(ctx, dockerPath) {
		return false, nil
	}
	if !isDockerSecretsSocketReparsePoint(socketPath) {
		return false, nil
	}

	if err := exec.CommandContext(wslCtx, wslPath, "--exec", "mv", "-n", "--", source, destination).Run(); err != nil {
		return false, errors.New("Docker's blocked secrets socket could not be moved aside. Check that WSL is available, then try again.")
	}
	if err := exec.CommandContext(wslCtx, wslPath, "--exec", "test", "!", "-e", source).Run(); err != nil {
		return false, errors.New("Docker's blocked secrets socket move could not be verified. Check that WSL is available, then try again.")
	}
	if err := exec.CommandContext(wslCtx, wslPath, "--exec", "test", "-e", destination).Run(); err != nil {
		return false, errors.New("Docker's blocked secrets socket backup could not be verified. Check that WSL is available, then try again.")
	}

	return true, nil
}

func hasKnownDockerSecretsSocketError(logs string) bool {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "initializing Secrets Engine") &&
			strings.Contains(line, "docker-secrets-engine") &&
			strings.Contains(line, "engine.sock") &&
			strings.Contains(line, "The file cannot be accessed by the system") {
			return true
		}
	}
	return false
}

func isDockerSecretsSocketReparsePoint(socketPath string) bool {
	info, err := os.Lstat(socketPath)
	if err != nil || info.IsDir() || info.Size() != 0 {
		return false
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func windowsPathToWSL(ctx context.Context, wslPath, path string) (string, error) {
	output, err := exec.CommandContext(ctx, wslPath, "--exec", "wslpath", "-u", path).Output()
	if err != nil {
		return "", err
	}
	converted := strings.TrimRight(string(output), "\r\n")
	if converted == "" || !strings.HasPrefix(converted, "/") || strings.ContainsAny(converted, "\r\n") {
		return "", errors.New("WSL returned an invalid path")
	}
	return converted, nil
}
