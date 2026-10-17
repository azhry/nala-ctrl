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
	manager := NewManager()
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

func TestServicesInOneConfigKeepIndependentProcessesAndLogs(t *testing.T) {
	folder := projectTestFolder(t, "nala-labs")
	command := longRunningTestCommand()
	config := fmt.Sprintf("projects:\n  backend:\n    cwd: .\n    run: %q\n  frontend:\n    cwd: .\n    run: %q\n", command, command)
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}

	manager := NewManager()
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
	manager := NewManager()
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
	manager := NewManager()
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
	manager := NewManager()
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
