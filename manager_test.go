package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestManagedProcessLifecycleAndLogCapture(t *testing.T) {
	folder := projectTestFolder(t, "backend")
	manager := newTestManager(t)
	logEvents := make(chan ProjectLogEvent, 16)
	manager.SetLogHandler(func(event ProjectLogEvent) { logEvents <- event })
	if _, err := manager.LoadFolder(folder); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateConfig(CreateProjectInput{
		CWD: ".", Run: longRunningTestCommand(),
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.StopAll() })

	result, err := manager.RunProject("backend")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Started || result.PID <= 0 {
		t.Fatalf("run result = %#v", result)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "NALA_CTRL_PROCESS_READY") {
		t.Fatalf("process output was not captured in %s: %v", logPath, lines)
	}
	select {
	case event := <-logEvents:
		if event.ServiceName != "backend" || event.ProjectName != "backend" || strings.TrimSpace(event.Line) != "NALA_CTRL_PROCESS_READY" || event.LogSize <= 0 {
			t.Fatalf("project log event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("process output was not emitted as a project log event")
	}
	details, err := manager.GetProjectDetails("backend")
	if err != nil {
		t.Fatal(err)
	}
	if !details.Running {
		t.Fatal("managed command should be running")
	}
	if err := manager.StopProject("backend"); err != nil {
		t.Fatal(err)
	}
	details, err = manager.GetProjectDetails("backend")
	if err != nil {
		t.Fatal(err)
	}
	if details.Running {
		t.Fatal("managed command should be stopped")
	}
}

func TestAddedConfiguredProjectsRestoreAfterRestart(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "user-config", "project-folders.json")
	firstFolder := projectTestFolder(t, "first-project")
	latestFolder := projectTestFolder(t, "latest-project")
	for _, folder := range []string{firstFolder, latestFolder} {
		if err := os.WriteFile(filepath.Join(folder, configFilename), []byte("projects:\n  backend:\n    cwd: .\n    run: go run .\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	manager := newManagerWithRegistryPath(registryPath)
	if _, err := manager.LoadFolder(firstFolder); err != nil {
		t.Fatal(err)
	}
	state, err := manager.LoadFolder(latestFolder)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Projects) != 2 {
		t.Fatalf("projects before restart = %d, want both configured folders", len(state.Projects))
	}
	if _, err := manager.LoadFolder(latestFolder); err != nil {
		t.Fatal(err)
	}
	folders, err := readProjectFolderRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 {
		t.Fatalf("saved folders = %v, want each configured folder once", folders)
	}

	restarted := newManagerWithRegistryPath(registryPath)
	state = restarted.Workspace()
	if len(state.Projects) != 2 {
		t.Fatalf("projects after restart = %d, want 2: %#v", len(state.Projects), state)
	}
	if len(restarted.running) != 0 {
		t.Fatalf("restoring saved projects started processes: %#v", restarted.running)
	}
	loadedFolders := map[string]bool{}
	for _, project := range state.Projects {
		loadedFolders[project.Folder] = true
		if len(project.Services) != 1 || project.Services[0].Name != "backend" {
			t.Fatalf("restored project services = %#v", project.Services)
		}
	}
	if !loadedFolders[firstFolder] || !loadedFolders[latestFolder] {
		t.Fatalf("restored folders = %v, want %q and %q", loadedFolders, firstFolder, latestFolder)
	}
}

func TestProjectManagerPersistsConfiguredProjectAcrossRestarts(t *testing.T) {
	userConfigDir := t.TempDir()
	t.Setenv("APPDATA", userConfigDir)

	folder := projectTestFolder(t, "default-registry-project")
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte("projects:\n  backend:\n    cwd: .\n    run: go run .\n"), 0644); err != nil {
		t.Fatal(err)
	}

	firstLaunch := NewProjectManager()
	state, err := firstLaunch.LoadFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Projects) != 1 || state.Projects[0].Folder != folder {
		t.Fatalf("projects after adding folder = %#v, want %q", state.Projects, folder)
	}

	registryPath, err := defaultProjectFolderRegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	savedFolders, err := readProjectFolderRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(savedFolders) != 1 || savedFolders[0] != folder {
		t.Fatalf("saved folders = %v, want only %q", savedFolders, folder)
	}

	secondLaunch := NewProjectManager()
	state = secondLaunch.GetWorkspace()
	if len(state.Projects) != 1 || state.Projects[0].Folder != folder {
		t.Fatalf("projects after restarting manager = %#v, want %q", state.Projects, folder)
	}
	if len(state.Projects[0].Services) != 1 || state.Projects[0].Services[0].Name != "backend" {
		t.Fatalf("restored services = %#v, want backend", state.Projects[0].Services)
	}
	if len(secondLaunch.manager.running) != 0 {
		t.Fatalf("restoring saved project started processes: %#v", secondLaunch.manager.running)
	}
}

func TestRestoreSkipsMissingProjectAndKeepsItsSavedPath(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "user-config", "project-folders.json")
	validFolder := projectTestFolder(t, "valid-project")
	missingFolder := projectTestFolder(t, "removed-project")
	for _, folder := range []string{validFolder, missingFolder} {
		if err := os.WriteFile(filepath.Join(folder, configFilename), []byte("projects:\n  backend:\n    cwd: .\n    run: go run .\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	manager := newManagerWithRegistryPath(registryPath)
	if _, err := manager.LoadFolder(validFolder); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.LoadFolder(missingFolder); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(missingFolder); err != nil {
		t.Fatal(err)
	}

	state := newManagerWithRegistryPath(registryPath).Workspace()
	if len(state.Projects) != 1 || state.Projects[0].Folder != validFolder {
		t.Fatalf("restored projects = %#v, want only %q", state.Projects, validFolder)
	}
	if len(state.Warnings) != 1 || !strings.Contains(state.Warnings[0], missingFolder) {
		t.Fatalf("restore warnings = %v, want warning for %q", state.Warnings, missingFolder)
	}
	folders, err := readProjectFolderRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 || folders[0] != validFolder || folders[1] != missingFolder {
		t.Fatalf("saved folders after skipped restore = %v, want both original paths", folders)
	}
}

func TestServicesInOneConfigKeepIndependentProcessesAndLogs(t *testing.T) {
	folder := projectTestFolder(t, "nala-labs")
	command := longRunningTestCommand()
	config := fmt.Sprintf("projects:\n  backend:\n    cwd: .\n    run: %q\n  frontend:\n    cwd: .\n    run: %q\n", command, command)
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}

	manager := newTestManager(t)
	state, err := manager.LoadFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Projects) != 1 || state.Projects[0].Name != "nala-labs" || len(state.Projects[0].Services) != 2 {
		t.Fatalf("loaded workspace = %#v, want one project with two services", state)
	}
	backend := state.Projects[0].Services[0]
	frontend := state.Projects[0].Services[1]
	if backend.Name != "backend" || frontend.Name != "frontend" || backend.ID == frontend.ID || backend.LogPath == frontend.LogPath {
		t.Fatalf("service identities/log paths are not independent: backend=%#v frontend=%#v", backend, frontend)
	}
	t.Cleanup(func() { _ = manager.StopAll() })

	if result, err := manager.RunProject(backend.ID); err != nil || !result.Started {
		t.Fatalf("start backend = %#v, error = %v", result, err)
	}
	if result, err := manager.RunProject(frontend.ID); err != nil || !result.Started {
		t.Fatalf("start frontend = %#v, error = %v", result, err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		backendLines, _, backendErr := readLogTail(backend.LogPath)
		frontendLines, _, frontendErr := readLogTail(frontend.LogPath)
		if backendErr != nil || frontendErr != nil {
			t.Fatalf("read service logs: backend=%v frontend=%v", backendErr, frontendErr)
		}
		if strings.Contains(strings.Join(backendLines, "\n"), "NALA_CTRL_PROCESS_READY") && strings.Contains(strings.Join(frontendLines, "\n"), "NALA_CTRL_PROCESS_READY") {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	backendDetails, err := manager.GetProjectDetails(backend.ID)
	if err != nil {
		t.Fatal(err)
	}
	frontendDetails, err := manager.GetProjectDetails(frontend.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !backendDetails.Running || !frontendDetails.Running || backendDetails.ServiceID != backend.ID || frontendDetails.ServiceID != frontend.ID {
		t.Fatalf("service details do not preserve independent running state: backend=%#v frontend=%#v", backendDetails, frontendDetails)
	}
	if !strings.Contains(strings.Join(backendDetails.LogLines, "\n"), "NALA_CTRL_PROCESS_READY") || !strings.Contains(strings.Join(frontendDetails.LogLines, "\n"), "NALA_CTRL_PROCESS_READY") {
		t.Fatalf("service log snapshots missing output: backend=%v frontend=%v", backendDetails.LogLines, frontendDetails.LogLines)
	}

	state, err = manager.LoadFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Projects) != 1 || len(state.Projects[0].Services) != 2 {
		t.Fatalf("reselecting the folder changed the project group: %#v", state)
	}
	if err := manager.StopProject(backend.ID); err != nil {
		t.Fatal(err)
	}
	backendDetails, err = manager.GetProjectDetails(backend.ID)
	if err != nil {
		t.Fatal(err)
	}
	frontendDetails, err = manager.GetProjectDetails(frontend.ID)
	if err != nil {
		t.Fatal(err)
	}
	if backendDetails.Running || !frontendDetails.Running {
		t.Fatalf("stopping backend affected the other service: backend running=%t frontend running=%t", backendDetails.Running, frontendDetails.Running)
	}
}

func TestProjectStateEventsCoverSilentExit(t *testing.T) {
	folder := projectTestFolder(t, "backend")
	manager := newTestManager(t)
	stateEvents := make(chan ProjectStateEvent, 2)
	manager.SetStateHandler(func(event ProjectStateEvent) { stateEvents <- event })
	if _, err := manager.LoadFolder(folder); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateConfig(CreateProjectInput{
		CWD: ".", Run: successfulExitTestCommand(),
	}); err != nil {
		t.Fatal(err)
	}

	result, err := manager.RunProject("backend")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Started || result.PID <= 0 {
		t.Fatalf("run result = %#v", result)
	}

	select {
	case event := <-stateEvents:
		if event.ServiceName != "backend" || event.ProjectName != "backend" || !event.Running || event.PID != result.PID {
			t.Fatalf("project start event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("project start state was not emitted")
	}
	select {
	case event := <-stateEvents:
		if event.ServiceName != "backend" || event.ProjectName != "backend" || event.Running || event.PID != 0 {
			t.Fatalf("silent project exit event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("silent project exit state was not emitted")
	}
}

func TestPortConflictReportsCurrentProcessAndRefusesToStopIt(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	folder := projectTestFolder(t, "backend")
	if err := os.WriteFile(filepath.Join(folder, configFilename), fmt.Appendf(nil, "projects:\n  backend:\n    cwd: .\n    run: %q\n    port: %d\n", "echo should-not-run", port), 0644); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	if _, err := manager.LoadFolder(folder); err != nil {
		t.Fatal(err)
	}
	result, err := manager.RunProject("backend")
	if err != nil {
		t.Fatal(err)
	}
	if result.Started || result.PortOwner == nil {
		t.Fatalf("expected an occupied-port result, got %#v", result)
	}
	if result.PortOwner.PID != int32(os.Getpid()) {
		t.Fatalf("port owner PID = %d, want test process %d", result.PortOwner.PID, os.Getpid())
	}
	if result.PortOwner.CanTerminate {
		t.Fatal("the app must never offer to kill itself")
	}
	if _, err := manager.TerminatePortOwnerAndRun("backend", result.PortOwner.PID, result.PortOwner.CreateTime); err == nil {
		t.Fatal("expected app-process termination to be refused")
	}
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("listener was stopped: %v", err)
	}
	_ = connection.Close()
}

func TestConfirmedPortTakeoverStopsExactOwnerAndRunsProject(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	child := exec.Command(os.Args[0], "-test.run=^TestPortListenerChild$")
	child.Env = append(os.Environ(), fmt.Sprintf("NALA_CTRL_TEST_LISTENER_PORT=%d", port))
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	var childOwner *ProcessIdentity
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		childOwner, err = findPortOwner(port)
		if err != nil {
			t.Fatal(err)
		}
		if childOwner != nil && childOwner.PID == int32(child.Process.Pid) {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if childOwner == nil || childOwner.PID != int32(child.Process.Pid) || !childOwner.CanTerminate {
		t.Fatalf("listener process identity = %#v, child PID = %d", childOwner, child.Process.Pid)
	}

	folder := projectTestFolder(t, "backend")
	config := fmt.Sprintf("projects:\n  backend:\n    cwd: .\n    run: %q\n    port: %d\n", "echo NALA_CTRL_TAKEOVER_STARTED", port)
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	if _, err := manager.LoadFolder(folder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.StopAll() })

	conflict, err := manager.RunProject("backend")
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Started || conflict.PortOwner == nil || conflict.PortOwner.PID != childOwner.PID {
		t.Fatalf("port conflict = %#v, want child owner PID %d", conflict, childOwner.PID)
	}
	if !conflict.PortOwner.CanTerminate {
		t.Fatalf("test listener should be safe to terminate: %#v", conflict.PortOwner)
	}

	result, err := manager.TerminatePortOwnerAndRun("backend", conflict.PortOwner.PID, conflict.PortOwner.CreateTime)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Started || result.PID <= 0 {
		t.Fatalf("takeover run result = %#v", result)
	}

	logPath := filepath.Join(folder, "logs", "backend.log")
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		lines, _, err := readLogTail(logPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(lines, "\n"), "NALA_CTRL_TAKEOVER_STARTED") {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	lines, _, err := readLogTail(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("takeover project output was not captured in %s: %v", logPath, lines)
}

func TestPortListenerChild(t *testing.T) {
	port := os.Getenv("NALA_CTRL_TEST_LISTENER_PORT")
	if port == "" {
		return
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for {
		time.Sleep(time.Hour)
	}
}

func longRunningTestCommand() string {
	if runtime.GOOS == "windows" {
		return "echo NALA_CTRL_PROCESS_READY & ping -n 20 127.0.0.1 > nul"
	}
	return "printf 'NALA_CTRL_PROCESS_READY\\n'; while :; do sleep 1; done"
}

func successfulExitTestCommand() string {
	if runtime.GOOS == "windows" {
		return "exit /b 0"
	}
	return "true"
}
