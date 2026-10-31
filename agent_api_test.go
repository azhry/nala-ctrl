package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
