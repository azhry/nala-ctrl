package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAgentAPIIsUnauthenticatedAndLoopbackOnly(t *testing.T) {
	manager := newTestManager(t)
	handler := newAgentAPIHandler(manager, agentAPIModeDesktop)

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47654/v1/health", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"ok":true,"mode":"desktop"}` {
		t.Fatalf("unauthenticated loopback health status = %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47654/v1/health", nil)
	request.RemoteAddr = "192.0.2.10:12345"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d, want %d", response.Code, http.StatusForbidden)
	}

	request = httptest.NewRequest(http.MethodGet, "http://example.test/v1/health", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("external Host status = %d, want %d", response.Code, http.StatusForbidden)
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47654/v1/health", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Origin", "http://localhost")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("browser-origin status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestAgentAPIHealthReportsHeadlessMode(t *testing.T) {
	manager := newTestManager(t)
	response := agentAPIRequest(t, newAgentAPIHandler(manager, agentAPIModeHeadless), http.MethodGet, "/v1/health", "", nil)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"ok":true,"mode":"headless"}` {
		t.Fatalf("headless health = %d %s, want the headless API mode", response.Code, response.Body.String())
	}
}

func TestStandaloneHeadlessAPIUsesDesktopManagerWhenAvailable(t *testing.T) {
	folder := projectTestFolder(t, "agent-api-forwarded-manager")
	command := longRunningTestCommand()
	config := fmt.Sprintf("projects:\n  backend:\n    cwd: .\n    run: %q\n", command)
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}

	desktopManager := newTestManager(t)
	workspaceEvents := make(chan string, 1)
	desktopServer := httptest.NewServer(newAgentAPIHandlerWithWorkspaceHandler(desktopManager, agentAPIModeDesktop, func(folder string) {
		workspaceEvents <- folder
	}))
	defer desktopServer.Close()
	desktopAddress := strings.TrimPrefix(desktopServer.URL, "http://")

	headlessManager := newTestManager(t)
	headlessServer := httptest.NewServer(newHeadlessAgentAPIHandlerWithDesktopAddress(headlessManager, nil, desktopAddress))
	defer headlessServer.Close()
	client := headlessServer.Client()

	statusCode, body := doAgentAPIHTTP(t, client, headlessServer.URL, http.MethodGet, "/v1/health", "")
	if statusCode != http.StatusOK || strings.TrimSpace(string(body)) != `{"ok":true,"mode":"desktop"}` {
		t.Fatalf("forwarded headless health = %d %s, want desktop mode", statusCode, body)
	}

	loadBody, err := json.Marshal(map[string]string{"folder": folder})
	if err != nil {
		t.Fatal(err)
	}
	statusCode, body = doAgentAPIHTTP(t, client, headlessServer.URL, http.MethodPost, "/v1/projects/load", string(loadBody))
	if statusCode != http.StatusOK {
		t.Fatalf("forwarded project load = %d %s", statusCode, body)
	}
	select {
	case changedFolder := <-workspaceEvents:
		if filepath.Clean(changedFolder) != filepath.Clean(folder) {
			t.Fatalf("workspace change folder = %q, want %q", changedFolder, folder)
		}
	case <-time.After(time.Second):
		t.Fatal("desktop API did not emit a workspace change after loading the project")
	}

	service := desktopManager.Workspace().Projects[0].Services[0]
	servicePath := "/v1/services/" + url.PathEscape(service.ID)
	t.Cleanup(func() { _ = desktopManager.StopAll() })

	statusCode, body = doAgentAPIHTTP(t, client, headlessServer.URL, http.MethodPost, servicePath+"/run", "")
	if statusCode != http.StatusOK || !strings.Contains(string(body), `"started":true`) {
		t.Fatalf("forwarded service run = %d %s", statusCode, body)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		details, err := desktopManager.GetProjectDetails(service.ID)
		if err == nil && details.Running {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	details, err := desktopManager.GetProjectDetails(service.ID)
	if err != nil || !details.Running {
		t.Fatalf("desktop manager did not observe the headless API process: details=%+v error=%v", details, err)
	}
	headlessManager.mu.RLock()
	_, registeredInHeadlessManager := headlessManager.services[service.ID]
	headlessManager.mu.RUnlock()
	if registeredInHeadlessManager {
		t.Fatal("standalone headless manager unexpectedly registered the forwarded project")
	}

	statusCode, body = doAgentAPIHTTP(t, client, headlessServer.URL, http.MethodPost, servicePath+"/stop", "")
	if statusCode != http.StatusOK || !strings.Contains(string(body), `"stopped":true`) {
		t.Fatalf("forwarded service stop = %d %s", statusCode, body)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		details, err = desktopManager.GetProjectDetails(service.ID)
		if err == nil && !details.Running {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("desktop manager still reports the stopped headless API process as running: %+v", details)
}

func doAgentAPIHTTP(t *testing.T, client *http.Client, baseURL, method, path, body string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, baseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, responseBody
}

func TestAgentAPIWorkspaceOmitsConfiguredRunCommand(t *testing.T) {
	folder := projectTestFolder(t, "agent-api-workspace")
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte("projects:\n  backend:\n    cwd: .\n    run: echo API_PRIVATE_COMMAND_MARKER\n"), 0644); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	state, err := manager.LoadFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	handler := newAgentAPIHandler(manager, agentAPIModeHeadless)
	response := agentAPIRequest(t, handler, http.MethodGet, "/v1/workspace", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("workspace status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "API_PRIVATE_COMMAND_MARKER") || strings.Contains(body, `"run"`) {
		t.Fatal("workspace response exposed the configured run command")
	}
	var workspace agentWorkspaceDTO
	if err := json.Unmarshal(response.Body.Bytes(), &workspace); err != nil {
		t.Fatal(err)
	}
	if len(workspace.Projects) != 1 || len(workspace.Projects[0].Services) != 1 || workspace.Projects[0].ID != state.Projects[0].ID {
		t.Fatalf("safe workspace = %#v, want the loaded project and service", workspace)
	}
}

func TestAgentAPIWorkspaceRefreshesChangedConfigAndEmitsWorkspaceEvent(t *testing.T) {
	folder := projectTestFolder(t, "agent-api-refresh-config")
	writeConfig := func(serviceEntries string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(folder, configFilename), []byte("projects:\n"+serviceEntries), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig("  backend:\n    cwd: .\n    run: echo backend\n")
	manager := newTestManager(t)
	if _, err := manager.LoadFolder(folder); err != nil {
		t.Fatal(err)
	}
	writeConfig("  backend:\n    cwd: .\n    run: echo backend\n  worker:\n    cwd: .\n    run: echo worker\n")

	workspaceEvents := make(chan string, 1)
	handler := newAgentAPIHandlerWithWorkspaceHandler(manager, agentAPIModeHeadless, func(changedFolder string) {
		workspaceEvents <- changedFolder
	})
	response := agentAPIRequest(t, handler, http.MethodGet, "/v1/workspace", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("workspace status = %d: %s", response.Code, response.Body.String())
	}
	var workspace agentWorkspaceDTO
	if err := json.Unmarshal(response.Body.Bytes(), &workspace); err != nil {
		t.Fatal(err)
	}
	if len(workspace.Projects) != 1 || len(workspace.Projects[0].Services) != 2 {
		t.Fatalf("workspace after config edit = %#v, want backend and worker", workspace)
	}
	select {
	case changedFolder := <-workspaceEvents:
		if changedFolder != folder {
			t.Fatalf("workspace event folder = %q, want %q", changedFolder, folder)
		}
	case <-time.After(time.Second):
		t.Fatal("workspace refresh did not emit an event for the changed config")
	}
}

func TestAgentAPIProjectLoadRegistersOnlyExplicitFolder(t *testing.T) {
	folder := projectTestFolder(t, "agent-api-explicit-load")
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte("projects:\n  backend:\n    cwd: .\n    run: echo API_PRIVATE_COMMAND_MARKER\n"), 0644); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	handler := newAgentAPIHandler(manager, agentAPIModeHeadless)
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	requestBody, err := json.Marshal(map[string]string{"folder": folder})
	if err != nil {
		t.Fatal(err)
	}
	response := agentAPIRequest(t, handler, http.MethodPost, "/v1/projects/load", string(requestBody), headers)
	if response.Code != http.StatusOK {
		t.Fatalf("project load status = %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "API_PRIVATE_COMMAND_MARKER") || strings.Contains(response.Body.String(), `"run"`) {
		t.Fatal("project load response exposed the configured run command")
	}
	if len(manager.Workspace().Projects) != 1 {
		t.Fatal("the explicitly requested project folder was not registered")
	}

	invalidBody, err := json.Marshal(map[string]string{"folder": folder, "extra": "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	response = agentAPIRequest(t, handler, http.MethodPost, "/v1/projects/load", string(invalidBody), headers)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON field status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestAgentAPIFlagsWorkspacePersistenceWarningsWithoutExposingDetails(t *testing.T) {
	folder := projectTestFolder(t, "agent-api-warning")
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte("projects:\n  backend:\n    cwd: .\n    run: echo API_PRIVATE_COMMAND_MARKER\n"), 0644); err != nil {
		t.Fatal(err)
	}
	registryParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(registryParent, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := newManagerWithRegistryPath(filepath.Join(registryParent, "project-folders.json"))
	handler := newAgentAPIHandler(manager, agentAPIModeHeadless)
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	requestBody, err := json.Marshal(map[string]string{"folder": folder})
	if err != nil {
		t.Fatal(err)
	}
	response := agentAPIRequest(t, handler, http.MethodPost, "/v1/projects/load", string(requestBody), headers)
	if response.Code != http.StatusOK {
		t.Fatalf("project load status = %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"hasWarnings":true`) {
		t.Fatal("workspace response did not flag the registry warning")
	}
	if strings.Contains(response.Body.String(), "API_PRIVATE_COMMAND_MARKER") || strings.Contains(response.Body.String(), "not-a-directory") {
		t.Fatal("workspace response exposed a run command or registry diagnostic")
	}
}

func TestAgentAPIRunStopAndBoundedLogs(t *testing.T) {
	folder := projectTestFolder(t, "agent-api-lifecycle")
	command := longRunningTestCommand()
	config := fmt.Sprintf("projects:\n  backend:\n    cwd: .\n    run: %q\n", command)
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	state, err := manager.LoadFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	service := state.Projects[0].Services[0]
	if err := os.MkdirAll(filepath.Dir(service.LogPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(service.LogPath, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	managerHandler := newAgentAPIHandler(manager, agentAPIModeHeadless)
	t.Cleanup(func() { _ = manager.StopAll() })

	path := "/v1/services/" + url.PathEscape(service.ID)
	response := agentAPIRequest(t, managerHandler, http.MethodGet, path+"/logs?limit=2", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("logs status = %d: %s", response.Code, response.Body.String())
	}
	var logs agentServiceLogsDTO
	if err := json.Unmarshal(response.Body.Bytes(), &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs.Lines) != 2 || logs.Lines[0] != "two" || logs.Lines[1] != "three" || logs.Limit != 2 {
		t.Fatalf("bounded log response = %#v", logs)
	}

	response = agentAPIRequest(t, managerHandler, http.MethodPost, path+"/run", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("run status = %d: %s", response.Code, response.Body.String())
	}
	var runResult struct {
		Started bool  `json:"started"`
		PID     int32 `json:"pid"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &runResult); err != nil {
		t.Fatal(err)
	}
	if !runResult.Started || runResult.PID <= 0 {
		t.Fatalf("run response = %#v", runResult)
	}

	statusPath := "/v1/services/" + url.PathEscape(service.ID)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response = agentAPIRequest(t, managerHandler, http.MethodGet, statusPath, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("service status = %d: %s", response.Code, response.Body.String())
		}
		var status agentServiceStatusDTO
		if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.Running {
			if strings.Contains(response.Body.String(), command) || strings.Contains(response.Body.String(), `"run"`) {
				t.Fatal("service status exposed the configured run command")
			}
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	var status agentServiceStatusDTO
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || !status.Running {
		t.Fatalf("service did not report API-managed running state: response=%s error=%v", response.Body.String(), err)
	}

	response = agentAPIRequest(t, managerHandler, http.MethodPost, statusPath+"/stop", "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stopped":true`) {
		t.Fatalf("stop status = %d: %s", response.Code, response.Body.String())
	}
	response = agentAPIRequest(t, managerHandler, http.MethodGet, statusPath, "", nil)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"running":true`) {
		t.Fatalf("service status after stop = %d: %s", response.Code, response.Body.String())
	}
}

func TestAgentAPIRunTakesOverConfiguredPortAndStopStopsService(t *testing.T) {
	port := freeAgentAPITestPort(t)
	oldOwner := startAgentAPITestPortOwner(t, port)
	manager, handler, servicePath := newAgentAPITestService(t, port, longRunningTestCommand())
	t.Cleanup(func() { _ = manager.StopAll() })

	response := agentAPIRequest(t, handler, http.MethodPost, servicePath+"/run", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("run status = %d: %s", response.Code, response.Body.String())
	}
	var runResult struct {
		Started           bool  `json:"started"`
		PID               int32 `json:"pid"`
		ReplacedPortOwner bool  `json:"replacedPortOwner"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &runResult); err != nil {
		t.Fatal(err)
	}
	if !runResult.Started || runResult.PID <= 0 || !runResult.ReplacedPortOwner {
		t.Fatalf("run result = %#v", runResult)
	}
	if err := oldOwner.Wait(); err == nil {
		t.Fatal("the original port owner exited without being terminated")
	}
	waitForAgentAPITestPortRelease(t, port)
	response = agentAPIRequest(t, handler, http.MethodGet, servicePath, "", nil)
	var status agentServiceStatusDTO
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || !status.Running {
		t.Fatalf("service status after takeover = %d: %s", response.Code, response.Body.String())
	}

	response = agentAPIRequest(t, handler, http.MethodPost, servicePath+"/stop", "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stopped":true`) {
		t.Fatalf("stop status = %d: %s", response.Code, response.Body.String())
	}
	waitForAgentAPITestPortRelease(t, port)
}

func TestAgentAPIStopTerminatesConfiguredExternalPortOwner(t *testing.T) {
	port := freeAgentAPITestPort(t)
	owner := startAgentAPITestPortOwner(t, port)
	manager, handler, servicePath := newAgentAPITestService(t, port, longRunningTestCommand())
	t.Cleanup(func() { _ = manager.StopAll() })

	response := agentAPIRequest(t, handler, http.MethodPost, servicePath+"/stop", "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"portOwnerStopped":true`) {
		t.Fatalf("stop status = %d: %s", response.Code, response.Body.String())
	}
	if err := owner.Wait(); err == nil {
		t.Fatal("the configured port owner exited without being terminated")
	}
	waitForAgentAPITestPortRelease(t, port)
}

func TestAgentAPIRunAndStopRefuseUnsafePortOwner(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	manager, handler, servicePath := newAgentAPITestService(t, port, longRunningTestCommand())
	t.Cleanup(func() { _ = manager.StopAll() })

	response := agentAPIRequest(t, handler, http.MethodPost, servicePath+"/run", "", nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"conflict":true`) {
		t.Fatalf("run status = %d: %s", response.Code, response.Body.String())
	}
	response = agentAPIRequest(t, handler, http.MethodPost, servicePath+"/stop", "", nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("stop status = %d: %s", response.Code, response.Body.String())
	}

	connection, err := net.DialTimeout("tcp", listener.Addr().String(), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("unsafe port owner was stopped: %v", err)
	}
	_ = connection.Close()
}

func freeAgentAPITestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func startAgentAPITestPortOwner(t *testing.T, port int) *exec.Cmd {
	t.Helper()
	t.Setenv("NALA_CTRL_TEST_LISTENER_PORT", strconv.Itoa(port))
	owner := exec.Command(os.Args[0], "-test.run=^TestPortListenerChild$")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if owner.Process != nil {
			_ = owner.Process.Kill()
			_ = owner.Wait()
		}
	})
	waitForAgentAPITestPortOwner(t, port, int32(owner.Process.Pid))
	return owner
}

func waitForAgentAPITestPortOwner(t *testing.T, port int, pid int32) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		owner, err := findPortOwner(port)
		if err != nil {
			t.Fatal(err)
		}
		if owner != nil && owner.PID == pid && owner.CanTerminate {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	owner, _ := findPortOwner(port)
	t.Fatalf("port %d owner = %#v; want safely terminable PID %d", port, owner, pid)
}

func waitForAgentAPITestPortRelease(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		owner, err := findPortOwner(port)
		if err != nil {
			t.Fatal(err)
		}
		if owner == nil {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	owner, _ := findPortOwner(port)
	t.Fatalf("port %d still has owner %#v", port, owner)
}

func newAgentAPITestService(t *testing.T, port int, command string) (*Manager, http.Handler, string) {
	t.Helper()
	folder := projectTestFolder(t, "agent-api-port-owner")
	config := fmt.Sprintf("projects:\n  backend:\n    cwd: .\n    run: %q\n    port: %d\n", command, port)
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	state, err := manager.LoadFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	service := state.Projects[0].Services[0]
	return manager, newAgentAPIHandler(manager, agentAPIModeHeadless), "/v1/services/" + url.PathEscape(service.ID)
}

func TestAgentAPIBranchLookupUsesRegisteredProjectPath(t *testing.T) {
	manager, project, _ := gitTestProject(t)
	handler := newAgentAPIHandler(manager, agentAPIModeHeadless)
	query := url.Values{"projectId": []string{project.ID}}
	response := agentAPIRequest(t, handler, http.MethodGet, "/v1/projects/git-branch?"+query.Encode(), "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("branch status = %d: %s", response.Code, response.Body.String())
	}
	var state GitBranchState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Available || state.CurrentBranch != "main" {
		t.Fatalf("branch response = %#v, want registered project root on main", state)
	}
}

func TestAgentAPIListenAddressMustBeLoopback(t *testing.T) {
	for _, address := range []string{"0.0.0.0:47653", ":47653", "192.0.2.10:47653", "127.0.0.1:0"} {
		if err := validateAgentAPIListenAddress(address); err == nil {
			t.Errorf("validateAgentAPIListenAddress(%q) succeeded; want rejection", address)
		}
	}
	if err := validateAgentAPIListenAddress("127.0.0.1:47653"); err != nil {
		t.Fatalf("loopback listen address was rejected: %v", err)
	}
}

func agentAPIRequest(t *testing.T, handler http.Handler, method, target, body string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	var requestBody *strings.Reader
	if body == "" {
		requestBody = strings.NewReader("")
	} else {
		requestBody = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, "http://127.0.0.1:47653"+target, requestBody)
	request.RemoteAddr = "127.0.0.1:12345"
	if header != nil {
		for key, values := range header {
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
