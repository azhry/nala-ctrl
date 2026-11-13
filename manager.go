package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	mu                sync.RWMutex
	gitMu             sync.Mutex
	restoreOnce       sync.Once
	folder            string
	needsConfig       bool
	projects          map[string]Project
	services          map[string]projectSpec
	running           map[string]*managedProcess
	starting          map[string]bool
	logHandler        func(ProjectLogEvent)
	stateHandler      func(ProjectStateEvent)
	registryPath      string
	registryFolders   []string
	registryReadable  bool
	registryPathError string
	workspaceWarnings []string
}

type managedProcess struct {
	command          *exec.Cmd
	pid              int32
	createTime       int64
	portOwner        *ProcessIdentity
	commandExited    bool
	recovered        bool
	startedByManager bool
	done             chan struct{}
}

type ProcessIdentity struct {
	PID              int32  `json:"pid"`
	Name             string `json:"name"`
	CreateTime       int64  `json:"createTime"`
	CanTerminate     bool   `json:"canTerminate"`
	ManagedProject   string `json:"managedProject"`
	ManagedService   string `json:"managedService"`
	ManagedProjectID string `json:"managedProjectId"`
	ManagedServiceID string `json:"managedServiceId"`
	TerminationNote  string `json:"terminationNote"`
}

type RunResult struct {
	Started   bool             `json:"started"`
	PID       int32            `json:"pid"`
	PortOwner *ProcessIdentity `json:"portOwner"`
}

type ProjectDetails struct {
	ProjectID   string           `json:"projectId"`
	ProjectName string           `json:"projectName"`
	ServiceID   string           `json:"serviceId"`
	ServiceName string           `json:"serviceName"`
	CWD         string           `json:"cwd"`
	Run         string           `json:"run"`
	Port        *int             `json:"port"`
	LogPath     string           `json:"logPath"`
	Running     bool             `json:"running"`
	PID         int32            `json:"pid"`
	PortOwner   *ProcessIdentity `json:"portOwner"`
	LogLines    []string         `json:"logLines"`
	LogSize     int64            `json:"logSize"`
}

type ServiceRuntimeStatus struct {
	Running   bool             `json:"running"`
	PID       int32            `json:"pid"`
	PortOwner *ProcessIdentity `json:"portOwner"`
}

func NewManager() *Manager {
	path, err := defaultProjectFolderRegistryPath()
	manager := newManagerWithRegistryPath(path)
	if err != nil {
		manager.registryPathError = err.Error()
	}
	return manager
}

func newManagerWithRegistryPath(registryPath string) *Manager {
	return &Manager{
		projects:     make(map[string]Project),
		services:     make(map[string]projectSpec),
		running:      make(map[string]*managedProcess),
		starting:     make(map[string]bool),
		registryPath: registryPath,
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
	m.restoreSavedProjectsOnce()
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.workspaceLocked()
}

func (m *Manager) restoreSavedProjectsOnce() {
	m.restoreOnce.Do(m.restoreSavedProjects)
}

func (m *Manager) restoreSavedProjects() {
	if m.registryPathError != "" {
		m.mu.Lock()
		m.addWorkspaceWarningLocked("Saved projects could not be loaded: " + m.registryPathError)
		m.mu.Unlock()
		return
	}
	folders, err := readProjectFolderRegistry(m.registryPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		m.mu.Lock()
		m.addWorkspaceWarningLocked("Saved projects could not be loaded: " + err.Error())
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	m.registryReadable = true
	m.registryFolders = folders
	m.mu.Unlock()

	seen := make(map[string]struct{}, len(folders))
	for _, savedFolder := range folders {
		if !filepath.IsAbs(savedFolder) {
			m.mu.Lock()
			m.addWorkspaceWarningLocked(fmt.Sprintf("Saved project folder %q is not an absolute path and was skipped.", savedFolder))
			m.mu.Unlock()
			continue
		}
		folder := filepath.Clean(savedFolder)
		identity := projectIDForFolder(folder)
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}

		loaded, err := readProjectFolder(folder)
		if err != nil {
			m.mu.Lock()
			m.addWorkspaceWarningLocked(fmt.Sprintf("Could not restore project folder %q: %v", folder, err))
			m.mu.Unlock()
			continue
		}
		if loaded.needsConfig {
			m.mu.Lock()
			m.addWorkspaceWarningLocked(fmt.Sprintf("Could not restore project folder %q because it no longer contains %s.", folder, configFilename))
			m.mu.Unlock()
			continue
		}
		m.mu.Lock()
		m.addProjectLocked(loaded.project, loaded.specs)
		m.mu.Unlock()
	}
}

func (m *Manager) addWorkspaceWarningLocked(warning string) {
	for _, existing := range m.workspaceWarnings {
		if existing == warning {
			return
		}
	}
	m.workspaceWarnings = append(m.workspaceWarnings, warning)
}

func (m *Manager) workspaceLocked() *WorkspaceState {
	state := &WorkspaceState{
		Folder: m.folder, NeedsConfig: m.needsConfig,
		Projects: make([]Project, 0, len(m.projects)),
		Warnings: append([]string(nil), m.workspaceWarnings...),
	}
	ids := make([]string, 0, len(m.projects))
	for id := range m.projects {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := m.projects[ids[i]], m.projects[ids[j]]
		if left.Name != right.Name {
			return strings.ToLower(left.Name) < strings.ToLower(right.Name)
		}
		return strings.ToLower(left.Folder) < strings.ToLower(right.Folder)
	})
	for _, id := range ids {
		state.Projects = append(state.Projects, m.projects[id])
	}
	return state
}

func (m *Manager) LoadFolder(folder string) (*WorkspaceState, error) {
	m.restoreSavedProjectsOnce()
	loaded, err := readProjectFolder(folder)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.folder = loaded.folder
	m.needsConfig = loaded.needsConfig
	if !loaded.needsConfig {
		if _, exists := m.projects[loaded.project.ID]; !exists {
			if m.registryReadable {
				folders, added := appendProjectFolder(m.registryFolders, loaded.folder)
				if added {
					if err := writeProjectFolderRegistry(m.registryPath, folders); err != nil {
						m.addWorkspaceWarningLocked("This project is available for this session, but its folder could not be saved for the next launch: " + err.Error())
					} else {
						m.registryFolders = folders
					}
				}
			}
		}
		m.refreshProjectLocked(loaded)
	}
	return m.workspaceLocked(), nil
}

func (m *Manager) RefreshProjects() []string {
	m.restoreSavedProjectsOnce()

	m.mu.RLock()
	folders := make([]string, 0, len(m.projects))
	for _, project := range m.projects {
		folders = append(folders, project.Folder)
	}
	m.mu.RUnlock()
	sort.Strings(folders)

	changedFolders := make([]string, 0)
	for _, folder := range folders {
		loaded, err := readProjectFolder(folder)
		if err != nil || loaded.needsConfig {
			continue
		}

		m.mu.Lock()
		changed := m.refreshProjectLocked(loaded)
		m.mu.Unlock()
		if changed {
			changedFolders = append(changedFolders, loaded.folder)
		}
	}
	return changedFolders
}

func (m *Manager) refreshProjectLocked(loaded loadedProjectFolder) bool {
	projectID := loaded.project.ID
	currentProject, exists := m.projects[projectID]
	if !exists {
		m.addProjectLocked(loaded.project, loaded.specs)
		return true
	}

	currentSpecs := make(map[string]projectSpec)
	for serviceID, spec := range m.services {
		if spec.ProjectID == projectID {
			currentSpecs[serviceID] = spec
		}
	}

	mergedSpecs := make(map[string]projectSpec, len(loaded.specs)+len(currentSpecs))
	servicesByID := make(map[string]Service, len(loaded.specs)+len(currentSpecs))
	for _, service := range loaded.project.Services {
		spec := loaded.specs[service.ID]
		if current, alreadyLoaded := currentSpecs[service.ID]; alreadyLoaded && m.serviceActiveLocked(service.ID) {
			spec = current
		}
		mergedSpecs[service.ID] = spec
		servicesByID[service.ID] = spec.Service
	}

	var retainedRunningServices []Service
	for serviceID, spec := range currentSpecs {
		if _, stillConfigured := mergedSpecs[serviceID]; stillConfigured || !m.serviceActiveLocked(serviceID) {
			continue
		}
		mergedSpecs[serviceID] = spec
		servicesByID[serviceID] = spec.Service
		retainedRunningServices = append(retainedRunningServices, spec.Service)
	}
	sort.Slice(retainedRunningServices, func(i, j int) bool {
		return strings.ToLower(retainedRunningServices[i].Name) < strings.ToLower(retainedRunningServices[j].Name)
	})

	project := loaded.project
	project.Services = make([]Service, 0, len(servicesByID))
	for _, service := range loaded.project.Services {
		project.Services = append(project.Services, servicesByID[service.ID])
	}
	project.Services = append(project.Services, retainedRunningServices...)

	if reflect.DeepEqual(currentProject, project) && reflect.DeepEqual(currentSpecs, mergedSpecs) {
		return false
	}
	for serviceID := range currentSpecs {
		if _, stillLoaded := mergedSpecs[serviceID]; !stillLoaded {
			delete(m.services, serviceID)
		}
	}
	for serviceID, spec := range mergedSpecs {
		m.services[serviceID] = spec
	}
	m.projects[projectID] = project
	return true
}

func (m *Manager) serviceActiveLocked(serviceID string) bool {
	if m.starting[serviceID] {
		return true
	}
	process := m.running[serviceID]
	if process == nil {
		return false
	}
	select {
	case <-process.done:
		return false
	default:
		return true
	}
}

type loadedProjectFolder struct {
	folder      string
	project     Project
	specs       map[string]projectSpec
	needsConfig bool
}

func readProjectFolder(folder string) (loadedProjectFolder, error) {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return loadedProjectFolder{}, errors.New("choose a project folder")
	}
	folder, err := filepath.Abs(folder)
	if err != nil {
		return loadedProjectFolder{}, fmt.Errorf("resolve project folder: %w", err)
	}
	folder = filepath.Clean(folder)
	info, err := os.Stat(folder)
	if err != nil {
		return loadedProjectFolder{}, fmt.Errorf("open project folder: %w", err)
	}
	if !info.IsDir() {
		return loadedProjectFolder{}, errors.New("project path must be a folder")
	}

	configPath := filepath.Join(folder, configFilename)
	data, readErr := os.ReadFile(configPath)
	var project Project
	var specs map[string]projectSpec
	needsConfig := false
	if readErr != nil {
		if !errors.Is(readErr, os.ErrNotExist) {
			return loadedProjectFolder{}, fmt.Errorf("read %s: %w", configFilename, readErr)
		}
		needsConfig = true
	} else {
		file, err := decodeProjectFile(data)
		if err != nil {
			return loadedProjectFolder{}, err
		}
		project, specs, err = projectForFolder(folder, file)
		if err != nil {
			return loadedProjectFolder{}, err
		}
	}
	return loadedProjectFolder{folder: folder, project: project, specs: specs, needsConfig: needsConfig}, nil
}

func (m *Manager) addProjectLocked(project Project, specs map[string]projectSpec) {
	if _, exists := m.projects[project.ID]; exists {
		return
	}
	m.projects[project.ID] = project
	for serviceID, spec := range specs {
		m.services[serviceID] = spec
	}
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

func (m *Manager) resolveServiceLocked(idOrName string) (string, projectSpec, bool) {
	if spec, exists := m.services[idOrName]; exists {
		return idOrName, spec, true
	}
	var matchID string
	var match projectSpec
	for id, spec := range m.services {
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
	_, spec, exists := m.resolveServiceLocked(idOrName)
	m.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("service %q is not loaded; choose its folder first or use its service ID when names repeat", idOrName)
	}
	status, err := m.GetProjectStatus(idOrName)
	if err != nil {
		return nil, err
	}

	details := &ProjectDetails{
		ProjectID: spec.ProjectID, ProjectName: spec.ProjectName,
		ServiceID: spec.ID, ServiceName: spec.Name,
		CWD: spec.CWD, Run: spec.Run, Port: spec.Port, LogPath: spec.LogPath,
		Running: status.Running, PID: status.PID, PortOwner: status.PortOwner,
		LogLines: []string{},
	}
	lines, size, err := readLogTail(spec.LogPath)
	if err != nil {
		return nil, fmt.Errorf("read service log: %w", err)
	}
	details.LogLines = lines
	details.LogSize = size
	return details, nil
}

func (m *Manager) GetProjectStatus(idOrName string) (*ServiceRuntimeStatus, error) {
	m.mu.RLock()
	id, spec, exists := m.resolveServiceLocked(idOrName)
	managed := m.running[id]
	starting := m.starting[id]
	m.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("service %q is not loaded; choose its folder first or use its service ID when names repeat", idOrName)
	}
	status := &ServiceRuntimeStatus{}
	if managed != nil && !processIsDone(managed.done) {
		m.mu.RLock()
		if current := m.running[id]; current == managed {
			status.Running = true
			status.PID = managed.statusPIDLocked()
		}
		m.mu.RUnlock()
	}
	var owner *ProcessIdentity
	if spec.Port != nil {
		var err error
		owner, err = findPortOwner(*spec.Port)
		if err != nil {
			return nil, fmt.Errorf("inspect port %d: %w", *spec.Port, err)
		}
		if owner != nil {
			status.PortOwner = owner
		}
	}
	var root *ProcessIdentity
	var matched, ownerMatched bool
	if owner != nil {
		root, matched = findConfiguredServiceProcess(spec, owner.PID)
		ownerMatched = matched
		if matched && root != nil && status.Running && managed != nil && root.PID == managed.pid &&
			root.CreateTime == managed.createTime {
			m.setManagedPortOwner(id, managed, owner)
			status.PID = owner.PID
		}
	}
	if !status.Running && !starting {
		if !matched {
			root, matched = findConfiguredServiceProcess(spec, 0)
		}
		if matched && root != nil && root.PID > 4 {
			var matchedOwner *ProcessIdentity
			if ownerMatched {
				matchedOwner = owner
			}
			managed = m.adoptConfiguredProcess(id, spec, root, matchedOwner)
			if managed != nil && !processIsDone(managed.done) {
				status.Running = true
				m.mu.RLock()
				status.PID = managed.statusPIDLocked()
				m.mu.RUnlock()
			}
		}
	}
	if status.PortOwner != nil {
		m.markManagedOwner(status.PortOwner)
	}
	return status, nil
}

func processIsDone(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func (p *managedProcess) statusPIDLocked() int32 {
	if p.portOwner != nil {
		return p.portOwner.PID
	}
	return p.pid
}

func (m *Manager) adoptConfiguredProcess(serviceID string, spec projectSpec, root, owner *ProcessIdentity) *managedProcess {
	var managedOwner *ProcessIdentity
	if owner != nil {
		ownerCopy := *owner
		managedOwner = &ownerCopy
	}
	process := &managedProcess{
		pid: root.PID, createTime: root.CreateTime, portOwner: managedOwner,
		recovered: true, commandExited: true, done: make(chan struct{}),
	}
	m.mu.Lock()
	if m.starting[serviceID] {
		m.mu.Unlock()
		return nil
	}
	if existing := m.running[serviceID]; existing != nil && !processIsDone(existing.done) {
		m.mu.Unlock()
		return existing
	}
	m.running[serviceID] = process
	m.mu.Unlock()
	m.publishState(ProjectStateEvent{ProjectID: spec.ProjectID, ProjectName: spec.ProjectName, ServiceID: spec.ID, ServiceName: spec.Name, Running: true, PID: process.statusPIDLocked()})

	logOffset := int64(0)
	if info, err := os.Stat(spec.LogPath); err == nil {
		logOffset = info.Size()
	}
	go m.followServiceLog(spec, logOffset, process.done)
	go m.monitorManagedProcess(serviceID, spec, process)
	return process
}

func (m *Manager) beginStart(idOrName string) (string, projectSpec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, spec, exists := m.resolveServiceLocked(idOrName)
	if !exists {
		return "", projectSpec{}, fmt.Errorf("service %q is not loaded; choose its folder first or use its service ID when names repeat", idOrName)
	}
	if process := m.running[id]; process != nil {
		select {
		case <-process.done:
			delete(m.running, id)
		default:
			return "", projectSpec{}, fmt.Errorf("service %q is already running", spec.Name)
		}
	}
	if m.starting[id] {
		return "", projectSpec{}, fmt.Errorf("service %q is already starting", spec.Name)
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
	if _, err := m.GetProjectStatus(idOrName); err != nil {
		return RunResult{}, err
	}
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
	if _, err := m.GetProjectStatus(idOrName); err != nil {
		return RunResult{}, err
	}
	id, spec, err := m.beginStart(idOrName)
	if err != nil {
		return RunResult{}, err
	}
	defer m.finishStart(id)
	if spec.Port == nil {
		return RunResult{}, errors.New("this service has no configured or detected port")
	}
	if err := terminatePortOwner(*spec.Port, pid, createTime); err != nil {
		return RunResult{}, err
	}
	return m.startProject(id, spec)
}

func (m *Manager) StopPortOwner(idOrName string) (bool, error) {
	id, spec, err := m.beginStart(idOrName)
	if err != nil {
		return false, err
	}
	defer m.finishStart(id)
	if spec.Port == nil {
		return false, nil
	}
	owner, err := findPortOwner(*spec.Port)
	if err != nil {
		return false, fmt.Errorf("inspect port %d: %w", *spec.Port, err)
	}
	if owner == nil {
		return false, nil
	}
	m.markManagedOwner(owner)
	if err := terminatePortOwner(*spec.Port, owner.PID, owner.CreateTime); err != nil {
		return false, err
	}
	return true, nil
}

func terminatePortOwner(port int, pid int32, createTime int64) error {
	owner, err := findPortOwner(port)
	if err != nil {
		return fmt.Errorf("recheck port %d: %w", port, err)
	}
	if owner == nil || owner.PID != pid || owner.CreateTime != createTime {
		return errors.New("the process using this port changed; refresh the project and try again")
	}
	if !owner.CanTerminate {
		if owner.TerminationNote == "" {
			owner.TerminationNote = "The process identity could not be verified, so it cannot be terminated safely."
		}
		return errors.New(owner.TerminationNote)
	}
	if err := terminatePID(owner.PID); err != nil {
		return fmt.Errorf("stop process %d: %w", owner.PID, err)
	}
	if err := waitForPortRelease(port, owner); err != nil {
		return err
	}
	return nil
}

func (m *Manager) markManagedOwner(owner *ProcessIdentity) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for serviceID, process := range m.running {
		if process.pid == owner.PID || (process.portOwner != nil && process.portOwner.PID == owner.PID) {
			if spec, ok := m.services[serviceID]; ok {
				owner.ManagedProject = spec.ProjectName
				owner.ManagedService = spec.Name
				owner.ManagedProjectID = spec.ProjectID
				owner.ManagedServiceID = spec.ID
			}
			return
		}
	}
}

func (m *Manager) startProject(serviceID string, spec projectSpec) (RunResult, error) {
	info, err := os.Stat(spec.WorkingDir)
	if err != nil {
		return RunResult{}, fmt.Errorf("service working directory %q: %w", spec.WorkingDir, err)
	}
	if !info.IsDir() {
		return RunResult{}, fmt.Errorf("service cwd %q is not a folder", spec.WorkingDir)
	}
	if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0755); err != nil {
		return RunResult{}, fmt.Errorf("create log folder: %w", err)
	}
	logFile, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return RunResult{}, fmt.Errorf("open log file: %w", err)
	}
	logInfo, err := logFile.Stat()
	if err != nil {
		_ = logFile.Close()
		return RunResult{}, fmt.Errorf("inspect project log: %w", err)
	}
	command := newShellCommand(spec.Run)
	command.Dir = spec.WorkingDir
	command.Stdout = logFile
	command.Stderr = logFile
	if err := prepareCommand(command); err != nil {
		_ = logFile.Close()
		return RunResult{}, err
	}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return RunResult{}, fmt.Errorf("start service: %w", err)
	}
	processID := int32(command.Process.Pid)
	processInfo := processIdentity(processID)
	process := &managedProcess{command: command, pid: processID, createTime: processInfo.CreateTime, startedByManager: true, done: make(chan struct{})}
	_ = logFile.Close()
	m.mu.Lock()
	m.running[serviceID] = process
	m.mu.Unlock()
	m.publishState(ProjectStateEvent{ProjectID: spec.ProjectID, ProjectName: spec.ProjectName, ServiceID: spec.ID, ServiceName: spec.Name, Running: true, PID: process.pid})
	go m.followServiceLog(spec, logInfo.Size(), process.done)
	go m.monitorManagedProcess(serviceID, spec, process)
	go func() {
		_ = command.Wait()
		m.mu.Lock()
		if m.running[serviceID] == process {
			process.commandExited = true
		}
		m.mu.Unlock()
	}()
	return RunResult{Started: true, PID: process.pid}, nil
}

func (m *Manager) monitorManagedProcess(serviceID string, spec projectSpec, managed *managedProcess) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-managed.done:
			return
		case <-ticker.C:
		}

		m.mu.RLock()
		if m.running[serviceID] != managed {
			m.mu.RUnlock()
			return
		}
		commandExited := managed.commandExited
		recovered := managed.recovered
		rootPID, rootCreateTime := managed.pid, managed.createTime
		owner := managed.portOwner
		m.mu.RUnlock()

		if !commandExited {
			if spec.Port != nil {
				currentOwner, err := findPortOwner(*spec.Port)
				if err == nil && currentOwner != nil {
					root, matches := findConfiguredServiceProcess(spec, currentOwner.PID)
					rootMatches := currentOwner.PID == rootPID
					if matches && root != nil && root.PID == rootPID && (rootCreateTime == 0 || root.CreateTime == rootCreateTime) {
						rootMatches = true
					}
					if rootMatches {
						m.setManagedPortOwner(serviceID, managed, currentOwner)
					}
				}
			}
			continue
		}

		if recovered {
			currentIdentity := processIdentity(rootPID)
			identityMatches := rootCreateTime > 0 && currentIdentity.CreateTime == rootCreateTime
			commandMatches := configuredServiceProcessMatches(rootPID, spec)
			if identityMatches || (commandMatches && (rootCreateTime <= 0 || currentIdentity.CreateTime <= 0 || currentIdentity.CreateTime == rootCreateTime)) {
				continue
			}
		}
		if owner != nil && spec.Port != nil && portStillOwnedBy(*spec.Port, owner) {
			continue
		}
		m.finishManagedProcess(serviceID, spec, managed)
		return
	}
}

func (m *Manager) setManagedPortOwner(serviceID string, managed *managedProcess, owner *ProcessIdentity) {
	m.mu.Lock()
	if m.running[serviceID] != managed || processIsDone(managed.done) {
		m.mu.Unlock()
		return
	}
	if managed.portOwner != nil && managed.portOwner.PID == owner.PID && managed.portOwner.CreateTime == owner.CreateTime {
		m.mu.Unlock()
		return
	}
	managedOwner := *owner
	service := m.services[serviceID]
	managedOwner.ManagedProject = service.ProjectName
	managedOwner.ManagedService = service.Name
	managedOwner.ManagedProjectID = service.ProjectID
	managedOwner.ManagedServiceID = service.ID
	managed.portOwner = &managedOwner
	managed.recovered = true
	m.mu.Unlock()
	m.publishState(ProjectStateEvent{ProjectID: service.ProjectID, ProjectName: service.ProjectName, ServiceID: service.ID, ServiceName: service.Name, Running: true, PID: owner.PID})
}

func (m *Manager) finishManagedProcess(serviceID string, spec projectSpec, managed *managedProcess) {
	m.mu.Lock()
	if m.running[serviceID] != managed || processIsDone(managed.done) {
		m.mu.Unlock()
		return
	}
	delete(m.running, serviceID)
	close(managed.done)
	m.mu.Unlock()
	m.publishState(ProjectStateEvent{ProjectID: spec.ProjectID, ProjectName: spec.ProjectName, ServiceID: spec.ID, ServiceName: spec.Name})
}

func portStillOwnedBy(port int, expected *ProcessIdentity) bool {
	owner, err := findPortOwner(port)
	return err == nil && owner != nil && owner.PID == expected.PID && owner.CreateTime > 0 && owner.CreateTime == expected.CreateTime
}

func (m *Manager) StopProject(idOrName string) error {
	if _, err := m.GetProjectStatus(idOrName); err != nil {
		return err
	}
	m.mu.RLock()
	id, spec, exists := m.resolveServiceLocked(idOrName)
	var process *managedProcess
	if exists {
		process = m.running[id]
	}
	rootPID, rootCreateTime := int32(0), int64(0)
	commandActive := false
	var owner *ProcessIdentity
	if process != nil {
		rootPID, rootCreateTime = process.pid, process.createTime
		commandActive = !process.commandExited && process.command != nil
		if process.portOwner != nil {
			ownerCopy := *process.portOwner
			owner = &ownerCopy
		}
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
	rootIdentity := processIdentity(rootPID)
	rootAlive := (commandActive && rootCreateTime == 0) || (rootCreateTime > 0 && rootIdentity.CreateTime == rootCreateTime)
	if !rootAlive && rootCreateTime <= 0 {
		rootAlive = configuredServiceProcessMatches(rootPID, spec)
	}
	if rootAlive {
		if rootCreateTime > 0 && !rootIdentity.CanTerminate {
			if rootIdentity.TerminationNote != "" {
				return fmt.Errorf("cannot safely stop configured service process %d: %s", rootPID, rootIdentity.TerminationNote)
			}
			return fmt.Errorf("cannot safely stop configured service process %d", rootPID)
		}
		if !commandActive && rootCreateTime <= 0 {
			return fmt.Errorf("cannot safely stop configured service process %d because its start time could not be verified", rootPID)
		}
		if err := terminateProcessTree(rootPID); err != nil {
			select {
			case <-process.done:
				return nil
			default:
				return fmt.Errorf("stop service process %d: %w", rootPID, err)
			}
		}
	} else if owner != nil && spec.Port != nil {
		if err := terminatePortOwner(*spec.Port, owner.PID, owner.CreateTime); err != nil {
			return err
		}
	} else {
		return nil
	}
	select {
	case <-process.done:
		return nil
	case <-time.After(6 * time.Second):
		return fmt.Errorf("service process %d did not stop", rootPID)
	}
}

func (m *Manager) StopAll() error {
	m.mu.RLock()
	names := make([]string, 0, len(m.running))
	for name, process := range m.running {
		if process.startedByManager {
			names = append(names, name)
		}
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
