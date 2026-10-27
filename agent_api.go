package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultAgentAPIListen = "127.0.0.1:47653"
	maxAgentAPIRequest    = 1 << 20
	defaultAgentLogLimit  = 100
	maxAgentLogLimit      = 250
)

type agentWorkspaceDTO struct {
	Folder      string            `json:"folder"`
	NeedsConfig bool              `json:"needsConfig"`
	HasWarnings bool              `json:"hasWarnings"`
	Projects    []agentProjectDTO `json:"projects"`
}

type agentProjectDTO struct {
	ID       string                  `json:"id"`
	Folder   string                  `json:"folder"`
	Name     string                  `json:"name"`
	Services []agentServiceConfigDTO `json:"services"`
}

type agentServiceConfigDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	CWD     string `json:"cwd"`
	Port    *int   `json:"port"`
	LogPath string `json:"logPath"`
}

type agentServiceStatusDTO struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ServiceID   string `json:"serviceId"`
	ServiceName string `json:"serviceName"`
	CWD         string `json:"cwd"`
	Port        *int   `json:"port"`
	PortInUse   *bool  `json:"portInUse,omitempty"`
	LogPath     string `json:"logPath"`
	Configured  bool   `json:"configured"`
	Running     bool   `json:"running"`
	PID         int32  `json:"pid,omitempty"`
}

type agentServiceLogsDTO struct {
	ServiceID string   `json:"serviceId"`
	Lines     []string `json:"lines"`
	SizeBytes int64    `json:"sizeBytes"`
	Limit     int      `json:"limit"`
}

type agentAPIErrorDTO struct {
	Error agentAPIErrorDetail `json:"error"`
}

type agentAPIErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func runHeadlessCLI(args []string, stdout, stderr io.Writer) (handled bool, exitCode int) {
	if len(args) == 0 || args[0] != "api" {
		return false, 0
	}
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: nala-ctrl api serve")
		return true, 2
	}
	switch args[1] {
	case "serve":
		flags := flag.NewFlagSet("nala-ctrl api serve", flag.ContinueOnError)
		flags.SetOutput(stderr)
		listen := flags.String("listen", defaultAgentAPIListen, "loopback address for the local agent API")
		if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
			fmt.Fprintln(stderr, "usage: nala-ctrl api serve [--listen 127.0.0.1:47653]")
			return true, 2
		}
		if err := validateAgentAPIListenAddress(*listen); err != nil {
			fmt.Fprintln(stderr, "nala-ctrl: API listen address must use a loopback IP and a valid port")
			return true, 2
		}
		if err := serveAgentAPI(*listen, stdout); err != nil {
			fmt.Fprintf(stderr, "nala-ctrl: local API stopped: %v\n", err)
			return true, 1
		}
		return true, 0
	default:
		fmt.Fprintln(stderr, "usage: nala-ctrl api serve")
		return true, 2
	}
}

func validateAgentAPIListenAddress(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("invalid listen address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("invalid listen port")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return errors.New("listen address is not loopback")
		}
		return nil
	}
	if !strings.EqualFold(host, "localhost") {
		return errors.New("listen address is not loopback")
	}
	addresses, err := net.LookupIP(host)
	if err != nil || len(addresses) == 0 {
		return errors.New("localhost could not be resolved")
	}
	for _, ip := range addresses {
		if !ip.IsLoopback() {
			return errors.New("localhost resolves outside loopback")
		}
	}
	return nil
}

func serveAgentAPI(address string, stdout io.Writer) error {
	if err := validateAgentAPIListenAddress(address); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	manager := NewManager()
	manager.Workspace()
	server := &http.Server{
		Handler:           newAgentAPIHandler(manager),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	fmt.Fprintf(stdout, "Nala Ctrl agent API listening on http://%s\n", listener.Addr())

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		_ = manager.StopAll()
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		stopErr := manager.StopAll()
		if shutdownErr != nil {
			return shutdownErr
		}
		return stopErr
	}
}

var errAgentServiceNotFound = errors.New("service not found")

func newAgentAPIHandler(manager *Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeAgentJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /v1/workspace", func(w http.ResponseWriter, _ *http.Request) {
		writeAgentJSON(w, http.StatusOK, safeAgentWorkspace(manager.Workspace()))
	})
	mux.HandleFunc("POST /v1/projects/load", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Folder string `json:"folder"`
		}
		if !decodeAgentJSON(w, r, &request) {
			return
		}
		if strings.TrimSpace(request.Folder) == "" {
			writeAgentAPIError(w, http.StatusBadRequest, "invalid_folder", "folder is required")
			return
		}
		state, err := manager.LoadFolder(request.Folder)
		if err != nil {
			writeAgentAPIError(w, http.StatusUnprocessableEntity, "project_load_failed", "folder could not be loaded as a Nala Ctrl project")
			return
		}
		writeAgentJSON(w, http.StatusOK, safeAgentWorkspace(state))
	})
	mux.HandleFunc("GET /v1/services/{serviceID}", func(w http.ResponseWriter, r *http.Request) {
		status, err := getAgentServiceStatus(manager, r.PathValue("serviceID"))
		if err != nil {
			writeAgentServiceStatusError(w, err)
			return
		}
		writeAgentJSON(w, http.StatusOK, status)
	})
	mux.HandleFunc("GET /v1/services/{serviceID}/logs", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("serviceID")
		spec, exists := getAgentServiceSpec(manager, id)
		if !exists {
			writeAgentAPIError(w, http.StatusNotFound, "service_not_found", "service is not registered with this API process")
			return
		}
		limit, ok := parseAgentLogLimit(r.URL.Query())
		if !ok {
			writeAgentAPIError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		lines, size, err := readLogTail(spec.LogPath)
		if err != nil {
			writeAgentAPIError(w, http.StatusInternalServerError, "logs_unavailable", "service logs could not be read")
			return
		}
		if len(lines) > limit {
			lines = lines[len(lines)-limit:]
		}
		writeAgentJSON(w, http.StatusOK, agentServiceLogsDTO{ServiceID: id, Lines: lines, SizeBytes: size, Limit: limit})
	})
	mux.HandleFunc("POST /v1/services/{serviceID}/run", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("serviceID")
		status, err := getAgentServiceStatus(manager, id)
		if err != nil {
			writeAgentServiceStatusError(w, err)
			return
		}
		result, err := manager.RunProject(id)
		if err != nil {
			if status.Running {
				writeAgentAPIError(w, http.StatusConflict, "already_running", "service is already running under this API process")
				return
			}
			writeAgentAPIError(w, http.StatusInternalServerError, "start_failed", "service could not be started; inspect its configured logs")
			return
		}
		if result.PortOwner != nil {
			writeAgentJSON(w, http.StatusConflict, map[string]any{
				"serviceId": id, "started": false, "conflict": true,
				"port":    status.Port,
				"message": "the configured service port is already in use; this API cannot stop its owner",
			})
			return
		}
		writeAgentJSON(w, http.StatusOK, map[string]any{"serviceId": id, "started": result.Started, "pid": result.PID})
	})
	mux.HandleFunc("POST /v1/services/{serviceID}/stop", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("serviceID")
		status, err := getAgentServiceStatus(manager, id)
		if err != nil {
			writeAgentServiceStatusError(w, err)
			return
		}
		if !status.Running {
			writeAgentJSON(w, http.StatusOK, map[string]any{"serviceId": id, "stopped": false, "alreadyStopped": true})
			return
		}
		if err := manager.StopProject(id); err != nil {
			writeAgentAPIError(w, http.StatusInternalServerError, "stop_failed", "service could not be stopped by this API process")
			return
		}
		writeAgentJSON(w, http.StatusOK, map[string]any{"serviceId": id, "stopped": true})
	})
	mux.HandleFunc("GET /v1/projects/git-branch", func(w http.ResponseWriter, r *http.Request) {
		values := r.URL.Query()["projectId"]
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			writeAgentAPIError(w, http.StatusBadRequest, "invalid_project_id", "one registered projectId is required")
			return
		}
		state, err := manager.GetProjectGitBranchState(values[0])
		if err != nil {
			writeAgentAPIError(w, http.StatusNotFound, "branch_state_unavailable", "project branch state is unavailable")
			return
		}
		writeAgentJSON(w, http.StatusOK, state)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			writeAgentAPIError(w, http.StatusForbidden, "browser_origin_denied", "browser-origin requests are not supported")
			return
		}
		if !isLoopbackAddress(r.RemoteAddr) || !isLoopbackHost(r.Host) {
			writeAgentAPIError(w, http.StatusForbidden, "loopback_only", "the API accepts loopback clients only")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func safeAgentWorkspace(state *WorkspaceState) agentWorkspaceDTO {
	result := agentWorkspaceDTO{
		Folder: state.Folder, NeedsConfig: state.NeedsConfig,
		HasWarnings: len(state.Warnings) > 0, Projects: []agentProjectDTO{},
	}
	for _, project := range state.Projects {
		projectDTO := agentProjectDTO{ID: project.ID, Folder: project.Folder, Name: project.Name, Services: []agentServiceConfigDTO{}}
		for _, service := range project.Services {
			projectDTO.Services = append(projectDTO.Services, agentServiceConfigDTO{
				ID: service.ID, Name: service.Name, CWD: service.CWD, Port: service.Port, LogPath: service.LogPath,
			})
		}
		result.Projects = append(result.Projects, projectDTO)
	}
	return result
}

func getAgentServiceSpec(manager *Manager, serviceID string) (projectSpec, bool) {
	manager.mu.RLock()
	spec, exists := manager.services[serviceID]
	manager.mu.RUnlock()
	return spec, exists
}

func getAgentServiceStatus(manager *Manager, serviceID string) (agentServiceStatusDTO, error) {
	manager.mu.RLock()
	spec, exists := manager.services[serviceID]
	process := manager.running[serviceID]
	manager.mu.RUnlock()
	if !exists {
		return agentServiceStatusDTO{}, errAgentServiceNotFound
	}

	status := agentServiceStatusDTO{
		ProjectID: spec.ProjectID, ProjectName: spec.ProjectName,
		ServiceID: spec.ID, ServiceName: spec.Name, CWD: spec.CWD,
		Port: spec.Port, LogPath: spec.LogPath, Configured: true,
	}
	if process != nil {
		select {
		case <-process.done:
		default:
			status.Running = true
			status.PID = process.pid
		}
	}
	if spec.Port != nil {
		owner, err := findPortOwner(*spec.Port)
		if err != nil {
			return agentServiceStatusDTO{}, err
		}
		inUse := owner != nil
		status.PortInUse = &inUse
	}
	return status, nil
}

func parseAgentLogLimit(query map[string][]string) (int, bool) {
	values, exists := query["limit"]
	if !exists {
		return defaultAgentLogLimit, true
	}
	if len(values) != 1 {
		return 0, false
	}
	limit, err := strconv.Atoi(values[0])
	if err != nil || limit < 1 {
		return 0, false
	}
	if limit > maxAgentLogLimit {
		limit = maxAgentLogLimit
	}
	return limit, true
}

func decodeAgentJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]))
	if contentType != "application/json" {
		writeAgentAPIError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentAPIRequest)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAgentAPIError(w, http.StatusBadRequest, "invalid_json", "request body must contain one valid JSON object")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAgentAPIError(w, http.StatusBadRequest, "invalid_json", "request body must contain one valid JSON object")
		return false
	}
	return true
}

func writeAgentServiceStatusError(w http.ResponseWriter, err error) {
	if errors.Is(err, errAgentServiceNotFound) {
		writeAgentAPIError(w, http.StatusNotFound, "service_not_found", "service is not registered with this API process")
		return
	}
	writeAgentAPIError(w, http.StatusInternalServerError, "service_status_unavailable", "service status could not be read")
}

func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if name, _, err := net.SplitHostPort(hostport); err == nil {
		host = name
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeAgentJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAgentAPIError(w http.ResponseWriter, status int, code, message string) {
	writeAgentJSON(w, status, agentAPIErrorDTO{Error: agentAPIErrorDetail{Code: code, Message: message}})
}
