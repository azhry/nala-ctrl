package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

func findPortOwner(port int) (*ProcessIdentity, error) {
	connections, err := net.Connections("tcp")
	if err != nil {
		return nil, err
	}
	seen := make(map[int32]struct{})
	var owners []int32
	for _, connection := range connections {
		if connection.Status != "LISTEN" || int(connection.Laddr.Port) != port || connection.Pid <= 0 {
			continue
		}
		if _, exists := seen[connection.Pid]; exists {
			continue
		}
		seen[connection.Pid] = struct{}{}
		owners = append(owners, connection.Pid)
	}
	if len(owners) == 0 {
		return nil, nil
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i] < owners[j] })
	return processIdentity(owners[0]), nil
}

func processIdentity(pid int32) *ProcessIdentity {
	identity := &ProcessIdentity{PID: pid, Name: fmt.Sprintf("Process %d", pid)}
	proc, err := process.NewProcess(pid)
	if err != nil {
		identity.TerminationNote = "The process could not be inspected, so it cannot be terminated safely."
		return identity
	}
	if name, err := proc.Name(); err == nil && name != "" {
		identity.Name = name
	}
	if created, err := proc.CreateTime(); err == nil && created > 0 {
		identity.CreateTime = created
		identity.CanTerminate = pid != int32(os.Getpid()) && pid > 4
	} else {
		identity.TerminationNote = "The process start time could not be verified, so it cannot be terminated safely."
	}
	if pid == int32(os.Getpid()) || pid <= 4 {
		identity.CanTerminate = false
		identity.TerminationNote = "System and app processes cannot be terminated from this prompt."
	}
	return identity
}

func findConfiguredServiceProcess(spec projectSpec, descendantOf int32) (*ProcessIdentity, bool) {
	if descendantOf > 0 {
		visited := make(map[int32]struct{})
		pid := descendantOf
		for depth := 0; pid > 4 && depth < 32; depth++ {
			if _, seen := visited[pid]; seen {
				break
			}
			visited[pid] = struct{}{}
			if configuredServiceProcessMatches(pid, spec) {
				return processIdentity(pid), true
			}
			proc, err := process.NewProcess(pid)
			if err != nil {
				break
			}
			parentPID, err := proc.Ppid()
			if err != nil || parentPID <= 4 || parentPID == pid {
				break
			}
			pid = parentPID
		}
		return nil, false
	}

	processes, err := process.Processes()
	if err != nil {
		return nil, false
	}
	var match *ProcessIdentity
	for _, proc := range processes {
		pid := proc.Pid
		if pid <= 4 || pid == int32(os.Getpid()) || !configuredServiceProcessMatches(pid, spec) {
			continue
		}
		if match != nil {
			return nil, false
		}
		match = processIdentity(pid)
	}
	return match, match != nil
}

func configuredServiceProcessMatches(pid int32, spec projectSpec) bool {
	proc, err := process.NewProcess(pid)
	if err != nil {
		return false
	}
	workingDir, err := proc.Cwd()
	if err != nil || !sameProcessPath(workingDir, spec.WorkingDir) {
		return false
	}
	commandLine, err := proc.Cmdline()
	if err != nil {
		return false
	}
	commandLine = strings.Join(strings.Fields(commandLine), " ")
	runCommand := strings.Join(strings.Fields(spec.Run), " ")
	if runtime.GOOS == "windows" {
		commandLine = strings.ToLower(commandLine)
		runCommand = strings.ToLower(runCommand)
	}
	return commandLineContainsRun(commandLine, runCommand)
}

func commandLineContainsRun(commandLine, runCommand string) bool {
	if runCommand == "" {
		return false
	}
	for offset := 0; offset < len(commandLine); {
		index := strings.Index(commandLine[offset:], runCommand)
		if index < 0 {
			return false
		}
		index += offset
		end := index + len(runCommand)
		startBoundary := index == 0 || isCommandBoundary(commandLine[index-1])
		endBoundary := end == len(commandLine) || isCommandBoundary(commandLine[end])
		if startBoundary && endBoundary {
			return true
		}
		offset = index + 1
	}
	return false
}

func isCommandBoundary(value byte) bool {
	return value == ' ' || value == '\t' || value == '"' || value == '\'' || value == '&' || value == '|' || value == ';' || value == ')'
}

func sameProcessPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func processIdentityMatches(pid int32, createTime int64) bool {
	if pid <= 4 || createTime <= 0 {
		return false
	}
	identity := processIdentity(pid)
	return identity.CreateTime == createTime
}

func terminatePID(pid int32) error {
	if pid <= 4 || pid == int32(os.Getpid()) {
		return errors.New("refusing to terminate a system or app process")
	}
	processHandle, err := os.FindProcess(int(pid))
	if err != nil {
		return err
	}
	return processHandle.Kill()
}
