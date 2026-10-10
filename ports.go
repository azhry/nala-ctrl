package main

import (
	"errors"
	"fmt"
	"os"
	"sort"

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
