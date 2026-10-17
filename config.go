package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const configFilename = "nala.yml"

type projectFile struct {
	Projects map[string]serviceFile `yaml:"projects"`
}

type serviceFile struct {
	CWD  string `yaml:"cwd"`
	Run  string `yaml:"run"`
	Port *int   `yaml:"port,omitempty"`
	Log  string `yaml:"log,omitempty"`
}

type CreateProjectInput struct {
	CWD  string `json:"cwd"`
	Run  string `json:"run"`
	Port string `json:"port"`
}

type Project struct {
	ID       string    `json:"id"`
	Folder   string    `json:"folder"`
	Name     string    `json:"name"`
	Services []Service `json:"services"`
}

type Service struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	CWD     string `json:"cwd"`
	Run     string `json:"run"`
	Port    *int   `json:"port"`
	LogPath string `json:"logPath"`
}

type WorkspaceState struct {
	Folder      string    `json:"folder"`
	NeedsConfig bool      `json:"needsConfig"`
	Projects    []Project `json:"projects"`
}

type projectSpec struct {
	Service
	ProjectID   string
	ProjectName string
	WorkingDir  string
}

var explicitPortPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:--port|-port)\s*(?:=\s*|\s+)(\d{1,5})\b`),
	regexp.MustCompile(`(?i)(?:^|\s)-p\s*(?:=\s*|\s+)?(\d{1,5})\b`),
	regexp.MustCompile(`(?i)(?:^|\s)PORT\s*=\s*(\d{1,5})\b`),
}
var listenerAddressPattern = regexp.MustCompile(`(?i)(?:^|\s)--addr(?:=|\s+)(?:"([^"]+)"|'([^']+)'|([^\s]+))`)
var unsafeLogNameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func decodeProjectFile(data []byte) (projectFile, error) {
	var file projectFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return projectFile{}, fmt.Errorf("read %s: %w", configFilename, err)
	}
	if len(file.Projects) == 0 {
		return projectFile{}, errors.New("nala.yml must define at least one service under projects")
	}
	for name, service := range file.Projects {
		if strings.TrimSpace(name) == "" {
			return projectFile{}, errors.New("nala.yml service names must not be empty")
		}
		if strings.TrimSpace(service.CWD) == "" {
			return projectFile{}, fmt.Errorf("nala.yml service %q is missing cwd", name)
		}
		if strings.TrimSpace(service.Run) == "" {
			return projectFile{}, fmt.Errorf("nala.yml service %q is missing run", name)
		}
		if service.Port != nil && (*service.Port < 1 || *service.Port > 65535) {
			return projectFile{}, fmt.Errorf("nala.yml service %q port must be between 1 and 65535", name)
		}
	}
	return file, nil
}

func projectForFolder(folder string, config projectFile) (Project, map[string]projectSpec, error) {
	folder, err := filepath.Abs(folder)
	if err != nil {
		return Project{}, nil, fmt.Errorf("resolve project folder: %w", err)
	}
	folder = filepath.Clean(folder)
	name := filepath.Base(folder)
	if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
		return Project{}, nil, errors.New("project folder must have a name")
	}

	projectID := projectIDForFolder(folder)
	project := Project{ID: projectID, Folder: folder, Name: name, Services: make([]Service, 0, len(config.Projects))}
	specs := make(map[string]projectSpec, len(config.Projects))
	logPaths := make(map[string]string, len(config.Projects))
	serviceNames := make([]string, 0, len(config.Projects))
	for serviceName := range config.Projects {
		serviceNames = append(serviceNames, serviceName)
	}
	sort.Slice(serviceNames, func(i, j int) bool { return strings.ToLower(serviceNames[i]) < strings.ToLower(serviceNames[j]) })
	for _, serviceName := range serviceNames {
		serviceConfig := config.Projects[serviceName]
		workingDir := serviceConfig.CWD
		if !filepath.IsAbs(workingDir) {
			workingDir = filepath.Join(folder, workingDir)
		}
		workingDir, err = filepath.Abs(workingDir)
		if err != nil {
			return Project{}, nil, fmt.Errorf("resolve cwd for %s/%s: %w", name, serviceName, err)
		}

		port := serviceConfig.Port
		if port == nil {
			port = inferPort(serviceConfig.Run)
		}
		logPath := serviceConfig.Log
		if strings.TrimSpace(logPath) == "" {
			logPath = filepath.Join("logs", defaultServiceLogName(serviceName))
		}
		if !filepath.IsAbs(logPath) {
			logPath = filepath.Join(workingDir, logPath)
		}
		logPath, err = filepath.Abs(logPath)
		if err != nil {
			return Project{}, nil, fmt.Errorf("resolve log path for %s/%s: %w", name, serviceName, err)
		}
		logIdentity := projectIDForFolder(logPath)
		if previous, exists := logPaths[logIdentity]; exists {
			return Project{}, nil, fmt.Errorf("services %q and %q use the same log path", previous, serviceName)
		}
		logPaths[logIdentity] = serviceName

		serviceID := serviceIDFor(projectID, serviceName)
		service := Service{ID: serviceID, Name: serviceName, CWD: workingDir, Run: serviceConfig.Run, Port: port, LogPath: logPath}
		project.Services = append(project.Services, service)
		specs[serviceID] = projectSpec{Service: service, ProjectID: projectID, ProjectName: name, WorkingDir: workingDir}
	}
	return project, specs, nil
}

func defaultServiceLogName(serviceName string) string {
	name := unsafeLogNameChars.ReplaceAllString(serviceName, "_")
	name = strings.Trim(name, ".")
	if name == "" {
		name = "service"
	}
	if len(name) > 100 {
		name = name[:100]
	}
	deviceName := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	reserved := map[string]bool{
		"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
		"COM6": true, "COM7": true, "COM8": true, "COM9": true,
		"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
		"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	}
	if reserved[deviceName] {
		name += "_service"
	}
	return name + ".log"
}

func projectIDForFolder(folder string) string {
	identity := filepath.Clean(folder)
	if runtime.GOOS == "windows" {
		identity = strings.ToLower(identity)
	}
	return identity
}

func serviceIDFor(projectID, serviceName string) string {
	digest := sha256.Sum256([]byte(projectID + "\x00" + serviceName))
	return "service-" + hex.EncodeToString(digest[:])
}
func inferPort(command string) *int {
	for _, pattern := range explicitPortPatterns {
		match := pattern.FindStringSubmatch(command)
		if len(match) > 1 {
			if port := validPort(match[1]); port != nil {
				return port
			}
		}
	}
	if match := listenerAddressPattern.FindStringSubmatch(command); len(match) > 0 {
		address := ""
		for _, group := range match[1:] {
			if group != "" {
				address = group
				break
			}
		}
		if _, portText, err := net.SplitHostPort(address); err == nil {
			if port := validPort(portText); port != nil {
				return port
			}
		}
	}

	command = strings.ToLower(command)
	knownDefaults := []struct {
		pattern *regexp.Regexp
		port    int
	}{
		{regexp.MustCompile(`(^|\s|[/\\])vite(\s|$)`), 5173},
		{regexp.MustCompile(`(^|\s|[/\\])next\s+dev(\s|$)`), 3000},
		{regexp.MustCompile(`python(?:3)?\s+-m\s+http\.server(\s|$)`), 8000},
		{regexp.MustCompile(`webpack\s+serve(\s|$)`), 8080},
	}
	for _, candidate := range knownDefaults {
		if candidate.pattern.MatchString(command) {
			port := candidate.port
			return &port
		}
	}
	return nil
}

func validPort(value string) *int {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return nil
	}
	return &port
}

func createProjectFile(folder string, input CreateProjectInput) error {
	cwd := strings.TrimSpace(input.CWD)
	run := strings.TrimSpace(input.Run)
	if cwd == "" {
		return errors.New("working directory is required")
	}
	if run == "" {
		return errors.New("run command is required")
	}

	var port *int
	if strings.TrimSpace(input.Port) != "" {
		port = validPort(strings.TrimSpace(input.Port))
		if port == nil {
			return errors.New("port must be a number between 1 and 65535")
		}
	} else {
		port = inferPort(run)
	}

	if !filepath.IsAbs(cwd) {
		cwd = filepath.Clean(cwd)
	}
	serviceName := filepath.Base(filepath.Clean(folder))
	if strings.TrimSpace(serviceName) == "" || serviceName == "." || serviceName == string(filepath.Separator) {
		return errors.New("project folder must have a name")
	}
	file := projectFile{Projects: map[string]serviceFile{
		serviceName: {CWD: cwd, Run: run, Port: port},
	}}
	data, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("encode %s: %w", configFilename, err)
	}
	path := filepath.Join(folder, configFilename)
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists", configFilename)
		}
		return fmt.Errorf("create %s: %w", configFilename, err)
	}
	if _, err := output.Write(data); err != nil {
		_ = output.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", configFilename, err)
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		_ = os.Remove(path)
		return fmt.Errorf("save %s: %w", configFilename, err)
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close %s: %w", configFilename, err)
	}
	return nil
}
