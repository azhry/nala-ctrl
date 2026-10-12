package main

import (
	"bytes"
	"os"
	"strings"
	"sync"
)

const projectLogEventName = "project:log"
const projectStateEventName = "project:state"

type ProjectLogEvent struct {
	ProjectID string `json:"projectId"`
	Project   string `json:"project"`
	Line      string `json:"line"`
	LogSize   int64  `json:"logSize"`
}

type ProjectStateEvent struct {
	ProjectID string `json:"projectId"`
	Project   string `json:"project"`
	Running   bool   `json:"running"`
	PID       int32  `json:"pid"`
}

type processLogWriter struct {
	mu          sync.Mutex
	file        *os.File
	projectID   string
	project     string
	logSize     int64
	pending     []byte
	pendingFrom int64
	emit        func(ProjectLogEvent)
}

func newProcessLogWriter(file *os.File, projectID, project string, emit func(ProjectLogEvent)) (*processLogWriter, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	return &processLogWriter{file: file, projectID: projectID, project: project, logSize: size, pendingFrom: size, emit: emit}, nil
}

func (w *processLogWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	n, err := w.file.Write(data)
	if n == 0 {
		w.mu.Unlock()
		return n, err
	}
	w.pending = append(w.pending, data[:n]...)
	w.logSize += int64(n)
	events := w.takeCompleteLines()
	w.mu.Unlock()
	w.emitEvents(events)
	return n, err
}

func (w *processLogWriter) takeCompleteLines() []ProjectLogEvent {
	var events []ProjectLogEvent
	for {
		newline := bytes.IndexByte(w.pending, '\n')
		if newline < 0 {
			return events
		}
		line := strings.TrimSuffix(string(w.pending[:newline]), "\r")
		end := w.pendingFrom + int64(newline+1)
		events = append(events, ProjectLogEvent{ProjectID: w.projectID, Project: w.project, Line: line, LogSize: end})
		w.pending = w.pending[newline+1:]
		w.pendingFrom = end
	}
}

func (w *processLogWriter) Close() error {
	w.mu.Lock()
	var events []ProjectLogEvent
	if len(w.pending) > 0 {
		events = append(events, ProjectLogEvent{ProjectID: w.projectID, Project: w.project, Line: string(w.pending), LogSize: w.logSize})
		w.pending = nil
		w.pendingFrom = w.logSize
	}
	err := w.file.Close()
	w.mu.Unlock()
	w.emitEvents(events)
	return err
}

func (w *processLogWriter) emitEvents(events []ProjectLogEvent) {
	if w.emit == nil {
		return
	}
	for _, event := range events {
		w.emit(event)
	}
}
