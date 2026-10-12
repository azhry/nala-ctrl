package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxLogBytes = 512 * 1024
	maxLogLines = 250
)

type Manager struct {
	mu           sync.RWMutex
	folder       string
	needsConfig  bool
	projects     map[string]projectSpec
	running      map[string]*managedProcess
	starting     map[string]bool
	logHandler   func(ProjectLogEvent)
	stateHandler func(ProjectStateEvent)
}

type managedProcess struct {
	command *exec.Cmd
	pid     int32
	done    chan struct{}
}

type ProcessIdentity struct {
	PID             int32  `json:"pid"`
	Name            string `json:"name"`
	CreateTime      int64  `json:"createTime"`
	CanTerminate    bool   `json:"canTerminate"`
	ManagedProject  string `json:"managedProject"`
	TerminationNote string `json:"terminationNote"`
}

type RunResult struct {
	Started   bool             `json:"started"`
	PID       int32            `json:"pid"`
	PortOwner *ProcessIdentity `json:"portOwner"`
}

type ProjectDetails struct {
	Project
	Running   bool             `json:"running"`
	PID       int32            `json:"pid"`
	PortOwner *ProcessIdentity `json:"portOwner"`
	LogLines  []string         `json:"logLines"`
	LogSize   int64            `json:"logSize"`
}

func NewManager() *Manager {
	return &Manager{
		projects: make(map[string]projectSpec),
		running:  make(map[string]*managedProcess),
		starting: make(map[string]bool),
	}
}

func (m *Manager) SetLogHandler(handler func(ProjectLogEvent)) {
	m.mu.Lock()
	m.logHandler = handler
	m.mu.Unlock()
}

func (m *Manager) SetStateHandler(handler func(ProjectStateEvent)) {
	m.mu.Lock()
	m.stateHandler = handler
	m.mu.Unlock()
}

func (m *Manager) publishLog(event ProjectLogEvent) {
	m.mu.RLock()
	handler := m.logHandler
	m.mu.RUnlock()
	if handler != nil {
		handler(event)
	}
}

func (m *Manager) publishState(event ProjectStateEvent) {
	m.mu.RLock()
	handler := m.stateHandler
	m.mu.RUnlock()
	if handler != nil {
		handler(event)
	}
}

func (m *Manager) Workspace() *WorkspaceState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.workspaceLocked()
}

func (m *Manager) workspaceLocked() *WorkspaceState {
	state := &WorkspaceState{
		Folder: m.folder, NeedsConfig: m.needsConfig,
		Projects: make([]Project, 0, len(m.projects)),
	}
	ids := make([]string, 0, len(m.projects))
	for id := range m.projects {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := m.projects[ids[i]].Project, m.projects[ids[j]].Project
		if left.Name != right.Name {
			return strings.ToLower(left.Name) < strings.ToLower(right.Name)
		}
		return strings.ToLower(left.Folder) < strings.ToLower(right.Folder)
	})
	for _, id := range ids {
		state.Projects = append(state.Projects, m.projects[id].Project)
	}
	return state
}

func (m *Manager) LoadFolder(folder string) (*WorkspaceState, error) {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return nil, errors.New("choose a project folder")
	}
	folder, err := filepath.Abs(folder)
	if err != nil {
		return nil, fmt.Errorf("resolve project folder: %w", err)
	}
	info, err := os.Stat(folder)
	if err != nil {
		return nil, fmt.Errorf("open project folder: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("project path must be a folder")
	}

	configPath := filepath.Join(folder, configFilename)
	data, readErr := os.ReadFile(configPath)
	var spec projectSpec
	needsConfig := false
	if readErr != nil {
		if !errors.Is(readErr, os.ErrNotExist) {
			return nil, fmt.Errorf("read %s: %w", configFilename, readErr)
		}
		needsConfig = true
	} else {
		file, err := decodeProjectFile(data)
		if err != nil {
			return nil, err
		}
		spec, err = projectForFolder(folder, file)
		if err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.folder = folder
	m.needsConfig = needsConfig
	if !needsConfig {
		// Loading a folder adds it to this app session. Re-adding a folder
		// selects its existing project without replacing its process or log state.
		if _, exists := m.projects[spec.ID]; !exists {
			m.projects[spec.ID] = spec
		}
	}
	return m.workspaceLocked(), nil
}

func (m *Manager) CreateConfig(input CreateProjectInput) (*WorkspaceState, error) {
	m.mu.RLock()
	folder := m.folder
	needsConfig := m.needsConfig
	m.mu.RUnlock()
	if folder == "" || !needsConfig {
		return nil, errors.New("choose a folder without nala.yml before creating a config")
	}
	if err := createProjectFile(folder, input); err != nil {
		return nil, err
	}
	return m.LoadFolder(folder)
}

func (m *Manager) resolveProjectLocked(idOrName string) (string, projectSpec, bool) {
	if spec, exists := m.projects[idOrName]; exists {
		return idOrName, spec, true
	}
	var matchID string
	var match projectSpec
	for id, spec := range m.projects {
		if spec.Name != idOrName {
			continue
		}
		if matchID != "" {
			return "", projectSpec{}, false
		}
		matchID, match = id, spec
	}
	return matchID, match, matchID != ""
}

func (m *Manager) GetProjectDetails(idOrName string) (*ProjectDetails, error) {
	m.mu.RLock()
	id, spec, exists := m.resolveProjectLocked(idOrName)
	managed := m.running[id]
	m.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("project %q is not loaded; choose its folder first or use its folder path when names repeat", idOrName)
	}

	details := &ProjectDetails{
		Project:  spec.Project,
		LogLines: []string{},
	}
	if managed != nil {
		select {
		case <-managed.done:
		default:
			details.Running = true
			details.PID = managed.pid
		}
	}
	if details.Port != nil {
		owner, err := findPortOwner(*details.Port)
		if err != nil {
			return nil, fmt.Errorf("inspect port %d: %w", *details.Port, err)
		}
		if owner != nil {
			m.mu.RLock()
			for projectID, process := range m.running {
				if process.pid == owner.PID {
					if managedSpec, ok := m.projects[projectID]; ok {
						owner.ManagedProject = managedSpec.Name
					}
					break
				}
			}
			m.mu.RUnlock()
			details.PortOwner = owner
		}
	}
	lines, size, err := readLogTail(spec.LogPath)
	if err != nil {
		return nil, fmt.Errorf("read project log: %w", err)
	}
	details.LogLines = lines
	details.LogSize = size
	return details, nil
}

func (m *Manager) beginStart(idOrName string) (string, projectSpec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, spec, exists := m.resolveProjectLocked(idOrName)
	if !exists {
		return "", projectSpec{}, fmt.Errorf("project %q is not loaded; choose its folder first or use its folder path when names repeat", idOrName)
	}
	if process := m.running[id]; process != nil {
		select {
		case <-process.done:
			delete(m.running, id)
		default:
			return "", projectSpec{}, fmt.Errorf("project %q is already running", spec.Name)
		}
	}
	if m.starting[id] {
		return "", projectSpec{}, fmt.Errorf("project %q is already starting", spec.Name)
	}
	m.starting[id] = true
	return id, spec, nil
}

func (m *Manager) finishStart(id string) {
	m.mu.Lock()
	delete(m.starting, id)
	m.mu.Unlock()
}

func (m *Manager) RunProject(idOrName string) (RunResult, error) {
	id, spec, err := m.beginStart(idOrName)
	if err != nil {
		return RunResult{}, err
	}
	defer m.finishStart(id)
	if spec.Port != nil {
		owner, err := findPortOwner(*spec.Port)
		if err != nil {
			return RunResult{}, fmt.Errorf("inspect port %d: %w", *spec.Port, err)
		}
		if owner != nil {
			m.markManagedOwner(owner)
			return RunResult{PortOwner: owner}, nil
		}
	}
	return m.startProject(id, spec)
}

func (m *Manager) TerminatePortOwnerAndRun(idOrName string, pid int32, createTime int64) (RunResult, error) {
	id, spec, err := m.beginStart(idOrName)
	if err != nil {
		return RunResult{}, err
	}
	defer m.finishStart(id)
	if spec.Port == nil {
		return RunResult{}, errors.New("this project has no configured or detected port")
	}
	owner, err := findPortOwner(*spec.Port)
	if err != nil {
		return RunResult{}, fmt.Errorf("recheck port %d: %w", *spec.Port, err)
	}
	if owner == nil || owner.PID != pid || owner.CreateTime != createTime {
		return RunResult{}, errors.New("the process using this port changed; refresh the project and try again")
	}
	if !owner.CanTerminate {
		if owner.TerminationNote == "" {
			owner.TerminationNote = "The process identity could not be verified, so it cannot be terminated safely."
		}
		return RunResult{}, errors.New(owner.TerminationNote)
	}
	if err := terminatePID(owner.PID); err != nil {
		return RunResult{}, fmt.Errorf("stop process %d: %w", owner.PID, err)
	}
	if err := waitForPortRelease(*spec.Port, owner); err != nil {
		return RunResult{}, err
	}
	return m.startProject(id, spec)
}

func (m *Manager) markManagedOwner(owner *ProcessIdentity) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for projectID, process := range m.running {
		if process.pid == owner.PID {
			if spec, ok := m.projects[projectID]; ok {
				owner.ManagedProject = spec.Name
			}
			return
		}
	}
}

func (m *Manager) startProject(projectID string, spec projectSpec) (RunResult, error) {
	info, err := os.Stat(spec.WorkingDir)
	if err != nil {
		return RunResult{}, fmt.Errorf("project folder %q: %w", spec.WorkingDir, err)
	}
	if !info.IsDir() {
		return RunResult{}, fmt.Errorf("project cwd %q is not a folder", spec.WorkingDir)
	}
	if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0755); err != nil {
		return RunResult{}, fmt.Errorf("create log folder: %w", err)
	}
	logFile, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return RunResult{}, fmt.Errorf("open log file: %w", err)
	}
	logOutput, err := newProcessLogWriter(logFile, projectID, spec.Name, m.publishLog)
	if err != nil {
		_ = logFile.Close()
		return RunResult{}, fmt.Errorf("inspect project log: %w", err)
	}
	command := newShellCommand(spec.Run)
	command.Dir = spec.WorkingDir
	command.Stdout = logOutput
	command.Stderr = logOutput
	if err := prepareCommand(command); err != nil {
		_ = logOutput.Close()
		return RunResult{}, err
	}
	if err := command.Start(); err != nil {
		_ = logOutput.Close()
		return RunResult{}, fmt.Errorf("start project: %w", err)
	}
	process := &managedProcess{command: command, pid: int32(command.Process.Pid), done: make(chan struct{})}
	m.mu.Lock()
	m.running[projectID] = process
	m.mu.Unlock()
	m.publishState(ProjectStateEvent{ProjectID: projectID, Project: spec.Name, Running: true, PID: process.pid})
	go func() {
		_ = command.Wait()
		_ = logOutput.Close()
		m.mu.Lock()
		if m.running[projectID] == process {
			delete(m.running, projectID)
		}
		m.mu.Unlock()
		close(process.done)
		m.publishState(ProjectStateEvent{ProjectID: projectID, Project: spec.Name})
	}()
	return RunResult{Started: true, PID: process.pid}, nil
}

func (m *Manager) StopProject(idOrName string) error {
	m.mu.RLock()
	id, _, exists := m.resolveProjectLocked(idOrName)
	var process *managedProcess
	if exists {
		process = m.running[id]
	}
	m.mu.RUnlock()
	if process == nil {
		return nil
	}
	select {
	case <-process.done:
		return nil
	default:
	}
	if err := terminateProcessTree(process.pid); err != nil {
		select {
		case <-process.done:
			return nil
		default:
			return fmt.Errorf("stop project process %d: %w", process.pid, err)
		}
	}
	select {
	case <-process.done:
		return nil
	case <-time.After(6 * time.Second):
		return fmt.Errorf("project process %d did not stop", process.pid)
	}
}

func (m *Manager) StopAll() error {
	m.mu.RLock()
	names := make([]string, 0, len(m.running))
	for name := range m.running {
		names = append(names, name)
	}
	m.mu.RUnlock()
	var failures []string
	for _, name := range names {
		if err := m.StopProject(name); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func waitForPortRelease(port int, original *ProcessIdentity) error {
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		owner, err := findPortOwner(port)
		if err != nil {
			return fmt.Errorf("check port %d after stopping process: %w", port, err)
		}
		if owner == nil {
			return nil
		}
		if owner.PID != original.PID || (owner.CreateTime != 0 && owner.CreateTime != original.CreateTime) {
			return fmt.Errorf("port %d was taken by process %d before the project could start", port, owner.PID)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("port %d is still in use by process %d", port, original.PID)
}

func readLogTail(path string) ([]string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, 0, nil
		}
		return nil, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := info.Size()
	start := int64(0)
	if size > maxLogBytes {
		start = size - maxLogBytes
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, 0, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, err
	}
	if start > 0 {
		if newline := strings.IndexByte(string(data), '\n'); newline >= 0 {
			data = data[newline+1:]
		}
	}
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, size, nil
	}
	if len(lines) > maxLogLines {
		lines = lines[len(lines)-maxLogLines:]
	}
	return lines, size, nil
}
