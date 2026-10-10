package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const configFilename = "nala.yml"

type projectFile struct {
	Projects map[string]ProjectConfig `yaml:"projects"`
}

type ProjectConfig struct {
	CWD  string `yaml:"cwd"`
	Run  string `yaml:"run"`
	Port *int   `yaml:"port,omitempty"`
	Log  string `yaml:"log,omitempty"`
}

type CreateProjectInput struct {
	Name string `json:"name"`
	CWD  string `json:"cwd"`
	Run  string `json:"run"`
	Port string `json:"port"`
}

type Project struct {
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
	Project
	WorkingDir string
}

var projectNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var explicitPortPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:--port|-port)\s*(?:=\s*|\s+)(\d{1,5})\b`),
	regexp.MustCompile(`(?i)(?:^|\s)-p\s*(?:=\s*|\s+)?(\d{1,5})\b`),
	regexp.MustCompile(`(?i)(?:^|\s)PORT\s*=\s*(\d{1,5})\b`),
}
var listenerAddressPattern = regexp.MustCompile(`(?i)(?:^|\s)--addr(?:=|\s+)(?:"([^"]+)"|'([^']+)'|([^\s]+))`)

func decodeProjectFile(data []byte) (projectFile, error) {
	var file projectFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return projectFile{}, fmt.Errorf("read %s: %w", configFilename, err)
	}
	if file.Projects == nil {
		return projectFile{}, errors.New("nala.yml must contain a projects map")
	}
	if len(file.Projects) == 0 {
		return projectFile{}, errors.New("nala.yml must contain at least one project")
	}
	for name, config := range file.Projects {
		if !projectNamePattern.MatchString(name) {
			return projectFile{}, fmt.Errorf("project name %q must start with a letter or number and use only letters, numbers, underscores, or hyphens", name)
		}
		if strings.TrimSpace(config.CWD) == "" {
			return projectFile{}, fmt.Errorf("project %q is missing cwd", name)
		}
		if strings.TrimSpace(config.Run) == "" {
			return projectFile{}, fmt.Errorf("project %q is missing run", name)
		}
		if config.Port != nil && (*config.Port < 1 || *config.Port > 65535) {
			return projectFile{}, fmt.Errorf("project %q port must be between 1 and 65535", name)
		}
	}
	return file, nil
}

func projectsForFolder(folder string, file projectFile) ([]projectSpec, error) {
	names := make([]string, 0, len(file.Projects))
	for name := range file.Projects {
		names = append(names, name)
	}
	sort.Strings(names)

	projects := make([]projectSpec, 0, len(names))
	for _, name := range names {
		config := file.Projects[name]
		workingDir := config.CWD
		if !filepath.IsAbs(workingDir) {
			workingDir = filepath.Join(folder, workingDir)
		}
		workingDir, err := filepath.Abs(workingDir)
		if err != nil {
			return nil, fmt.Errorf("resolve cwd for %s: %w", name, err)
		}

		port := config.Port
		if port == nil {
			port = inferPort(config.Run)
		}
		logPath := config.Log
		if strings.TrimSpace(logPath) == "" {
			logPath = filepath.Join("logs", name+".log")
		}
		if !filepath.IsAbs(logPath) {
			logPath = filepath.Join(workingDir, logPath)
		}
		logPath, err = filepath.Abs(logPath)
		if err != nil {
			return nil, fmt.Errorf("resolve log path for %s: %w", name, err)
		}

		projects = append(projects, projectSpec{
			Project: Project{
				Name: name, CWD: workingDir, Run: config.Run, Port: port, LogPath: logPath,
			},
			WorkingDir: workingDir,
		})
	}
	return projects, nil
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
	name := strings.TrimSpace(input.Name)
	cwd := strings.TrimSpace(input.CWD)
	run := strings.TrimSpace(input.Run)
	if !projectNamePattern.MatchString(name) {
		return errors.New("project name must start with a letter or number and use only letters, numbers, underscores, or hyphens")
	}
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
	file := projectFile{Projects: map[string]ProjectConfig{
		name: {CWD: cwd, Run: run, Port: port},
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
