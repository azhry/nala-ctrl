//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
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

type windowsProcessTreeEntry struct {
	pid        int32
	parentPID  int32
	createTime int64
	depth      int
}

func terminateProcessTree(pid int32, expectedCreateTime int64) error {
	if pid <= 4 || pid == int32(os.Getpid()) {
		return errors.New("refusing to terminate a system or app process")
	}

	processes, err := snapshotWindowsProcesses()
	if err != nil {
		return fmt.Errorf("inspect service process tree: %w", err)
	}
	root, exists := processes[pid]
	if !exists {
		return nil
	}
	if root.createTime <= 0 {
		return fmt.Errorf("cannot safely stop configured service process %d because its start time could not be verified", pid)
	}
	if expectedCreateTime > 0 && root.createTime != expectedCreateTime {
		return fmt.Errorf("configured service process %d changed before it could be stopped", pid)
	}

	known := map[int32]windowsProcessTreeEntry{pid: root}
	if err := addWindowsProcessDescendants(known, processes); err != nil {
		return err
	}
	if err := terminateWindowsProcessEntry(root); err != nil {
		return err
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		processes, err = snapshotWindowsProcesses()
		if err != nil {
			return fmt.Errorf("recheck service process tree: %w", err)
		}
		if err := addWindowsProcessDescendants(known, processes); err != nil {
			return err
		}

		descendants := make([]windowsProcessTreeEntry, 0, len(known)-1)
		for childPID, entry := range known {
			if childPID != pid {
				descendants = append(descendants, entry)
			}
		}
		sort.Slice(descendants, func(i, j int) bool {
			return descendants[i].depth > descendants[j].depth
		})
		var failures []error
		for _, entry := range descendants {
			current, exists := processes[entry.pid]
			if !exists || current.createTime != entry.createTime {
				continue
			}
			if err := terminateWindowsProcessEntry(entry); err != nil {
				failures = append(failures, fmt.Errorf("terminate service process %d: %w", entry.pid, err))
			}
		}

		active := make([]int32, 0, len(known))
		for processPID, entry := range known {
			current, exists := processes[processPID]
			if exists && current.createTime == entry.createTime {
				active = append(active, processPID)
			}
		}
		if len(active) == 0 {
			return nil
		}
		if len(failures) > 0 {
			return failures[0]
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service process tree rooted at %d did not stop", pid)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func snapshotWindowsProcesses() (map[int32]windowsProcessTreeEntry, error) {
	all, err := process.Processes()
	if err != nil {
		return nil, err
	}
	processes := make(map[int32]windowsProcessTreeEntry, len(all))
	for _, candidate := range all {
		parentPID, err := candidate.Ppid()
		if err != nil {
			continue
		}
		createTime, _ := candidate.CreateTime()
		processes[candidate.Pid] = windowsProcessTreeEntry{
			pid: candidate.Pid, parentPID: parentPID, createTime: createTime,
		}
	}
	return processes, nil
}

func addWindowsProcessDescendants(known, current map[int32]windowsProcessTreeEntry) error {
	children := make(map[int32][]windowsProcessTreeEntry, len(current))
	for _, entry := range current {
		children[entry.parentPID] = append(children[entry.parentPID], entry)
	}
	queue := make([]windowsProcessTreeEntry, 0, len(known))
	for pid, entry := range known {
		if process, exists := current[pid]; !exists || process.createTime == entry.createTime {
			queue = append(queue, entry)
		}
	}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent.pid] {
			if _, exists := known[child.pid]; exists {
				continue
			}
			if child.pid <= 4 || child.pid == int32(os.Getpid()) {
				continue
			}
			if child.createTime <= 0 {
				return fmt.Errorf("cannot safely stop configured service process %d because its start time could not be verified", child.pid)
			}
			child.depth = parent.depth + 1
			known[child.pid] = child
			queue = append(queue, child)
		}
	}
	return nil
}

func terminateWindowsProcessEntry(entry windowsProcessTreeEntry) error {
	identity := processIdentity(entry.pid)
	if identity.CreateTime != entry.createTime {
		return nil
	}
	if !identity.CanTerminate {
		if identity.TerminationNote == "" {
			identity.TerminationNote = "The process identity could not be verified, so it cannot be terminated safely."
		}
		return errors.New(identity.TerminationNote)
	}
	if err := terminatePID(entry.pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
		if processIdentityMatches(entry.pid, entry.createTime) {
			return err
		}
	}
	return nil
}
