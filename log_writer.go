package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"time"
)

const projectLogEventName = "project:log"
const projectStateEventName = "project:state"
const workspaceChangedEventName = "workspace:changed"

type ProjectLogEvent struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ServiceID   string `json:"serviceId"`
	ServiceName string `json:"serviceName"`
	Line        string `json:"line"`
	LogSize     int64  `json:"logSize"`
}

type ProjectStateEvent struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ServiceID   string `json:"serviceId"`
	ServiceName string `json:"serviceName"`
	Running     bool   `json:"running"`
	PID         int32  `json:"pid"`
}

func (m *Manager) followServiceLog(spec projectSpec, offset int64, done <-chan struct{}) {
	const maxReadSize = 64 * 1024
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	pending := []byte{}
	pendingFrom := offset
	readAvailable := func() bool {
		file, err := os.Open(spec.LogPath)
		if err != nil {
			return false
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return false
		}
		if info.Size() < offset {
			offset = 0
			pending = nil
			pendingFrom = 0
		}
		if info.Size() <= offset {
			return false
		}
		readSize := info.Size() - offset
		if readSize > maxReadSize {
			readSize = maxReadSize
		}
		data := make([]byte, readSize)
		n, err := file.ReadAt(data, offset)
		if err != nil && err != io.EOF {
			return false
		}
		data = data[:n]
		pending = append(pending, data...)
		offset += int64(n)
		for {
			newline := bytes.IndexByte(pending, '\n')
			if newline < 0 {
				return n > 0
			}
			line := strings.TrimSuffix(string(pending[:newline]), "\r")
			end := pendingFrom + int64(newline+1)
			m.publishLog(ProjectLogEvent{ProjectID: spec.ProjectID, ProjectName: spec.ProjectName, ServiceID: spec.ID, ServiceName: spec.Name, Line: line, LogSize: end})
			pending = pending[newline+1:]
			pendingFrom = end
		}
	}
	for {
		select {
		case <-done:
			for readAvailable() {
			}
			if len(pending) > 0 {
				m.publishLog(ProjectLogEvent{ProjectID: spec.ProjectID, ProjectName: spec.ProjectName, ServiceID: spec.ID, ServiceName: spec.Name, Line: string(pending), LogSize: offset})
			}
			return
		case <-ticker.C:
			readAvailable()
		}
	}
}
