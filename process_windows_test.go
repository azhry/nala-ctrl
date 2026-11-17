//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStopShellServiceClosesItsProcessTreeAndLog(t *testing.T) {
	folder := projectTestFolder(t, "backend")
	manager := newTestManager(t)
	if _, err := manager.LoadFolder(folder); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateConfig(CreateProjectInput{CWD: ".", Run: longRunningTestCommand()}); err != nil {
		t.Fatal(err)
	}

	result, err := manager.RunProject("backend")
	if err != nil {
		t.Fatal(err)
	}
	rootIdentity := processIdentity(result.PID)
	var tree []windowsProcessTreeEntry
	cleanup := func() {
		for _, entry := range tree {
			if processIdentityMatches(entry.pid, entry.createTime) {
				_ = terminatePID(entry.pid)
			}
		}
		if rootIdentity.CreateTime > 0 && processIdentityMatches(rootIdentity.PID, rootIdentity.CreateTime) {
			_ = terminatePID(rootIdentity.PID)
		}
	}
	t.Cleanup(cleanup)

	logPath := filepath.Join(folder, "logs", "backend.log")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		lines, _, err := readLogTail(logPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(lines, "\n"), "NALA_CTRL_PROCESS_READY") {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	lines, _, err := readLogTail(logPath)
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), "NALA_CTRL_PROCESS_READY") {
		t.Fatalf("service did not become ready: lines=%v error=%v", lines, err)
	}

	processes, err := snapshotWindowsProcesses()
	if err != nil {
		t.Fatal(err)
	}
	root, exists := processes[result.PID]
	if !exists {
		t.Fatalf("managed shell process %d was not found", result.PID)
	}
	known := map[int32]windowsProcessTreeEntry{result.PID: root}
	if err := addWindowsProcessDescendants(known, processes); err != nil {
		t.Fatal(err)
	}
	for _, entry := range known {
		tree = append(tree, entry)
	}
	if len(tree) < 2 {
		t.Fatalf("expected a shell process and child process, got %d processes", len(tree))
	}

	if err := manager.StopProject("backend"); err != nil {
		t.Fatal(err)
	}
	for _, entry := range tree {
		if processIdentityMatches(entry.pid, entry.createTime) {
			t.Fatalf("service process %d remains alive after StopProject", entry.pid)
		}
	}
	status, err := manager.GetProjectStatus("backend")
	if err != nil || status.Running {
		t.Fatalf("service status after StopProject = %#v, error = %v", status, err)
	}
	if err := os.Remove(logPath); err != nil {
		t.Fatalf("service log is still held after StopProject: %v", err)
	}
}
