package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func projectTestFolder(t *testing.T, name string) string {
	t.Helper()
	folder := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(folder, 0755); err != nil {
		t.Fatal(err)
	}
	return folder
}

func TestDecodeProjectFileAndResolveDefaults(t *testing.T) {
	folder := projectTestFolder(t, "backend")
	data := []byte("cwd: ./api\nrun: go run . --port=8081\n")
	config, err := decodeProjectFile(data)
	if err != nil {
		t.Fatal(err)
	}
	project, err := projectForFolder(folder, config)
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "backend" {
		t.Fatalf("project name = %q, want backend", project.Name)
	}
	if project.Port == nil || *project.Port != 8081 {
		t.Fatalf("project port = %v, want 8081", project.Port)
	}
	if got, want := project.LogPath, filepath.Join(folder, "api", "logs", "backend.log"); got != want {
		t.Fatalf("project log path = %q, want %q", got, want)
	}
}

func TestInferPort(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    int
	}{
		{name: "long flag", command: "go run . --port 8080", want: 8080},
		{name: "equals flag", command: "go run . -port=8081", want: 8081},
		{name: "short flag", command: "server -p 8082", want: 8082},
		{name: "environment", command: "PORT=8083 go run .", want: 8083},
		{name: "listener address", command: "go run server.go --addr 127.0.0.1:8080", want: 8080},
		{name: "listener address equals", command: "go run server.go --addr=:8084", want: 8084},
		{name: "quoted listener address", command: "go run server.go --addr \"localhost:8085\"", want: 8085},
		{name: "vite default", command: "vite --host 0.0.0.0", want: 5173},
		{name: "next default", command: "npx next dev", want: 3000},
		{name: "python default", command: "python -m http.server", want: 8000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := inferPort(test.command); got == nil || *got != test.want {
				t.Fatalf("inferPort(%q) = %v, want %d", test.command, got, test.want)
			}
		})
	}
	if got := inferPort("go run ."); got != nil {
		t.Fatalf("inferPort for an unknown Go server default = %d, want nil", *got)
	}
	if got := inferPort("go run . --port 99999"); got != nil {
		t.Fatalf("inferPort for an invalid port = %d, want nil", *got)
	}
	if got := inferPort("go run server.go --addr 127.0.0.1:99999"); got != nil {
		t.Fatalf("inferPort for an invalid listener port = %d, want nil", *got)
	}
}

func TestDecodeProjectFileRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "empty config", data: ""},
		{name: "missing cwd", data: "run: go run .\n"},
		{name: "missing run", data: "cwd: .\n"},
		{name: "invalid port", data: "cwd: .\nrun: go run .\nport: 65536\n"},
		{name: "projects map unsupported", data: "projects:\n  backend:\n    cwd: .\n    run: go run .\n"},
		{name: "unknown field", data: "cwd: .\nrun: go run .\nwatch: true\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeProjectFile([]byte(test.data)); err == nil {
				t.Fatal("expected invalid config to fail")
			}
		})
	}
}

func TestCreateConfigWritesOneProjectWithoutOverwriting(t *testing.T) {
	folder := projectTestFolder(t, "backend")
	manager := NewManager()
	state, err := manager.LoadFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	if !state.NeedsConfig {
		t.Fatal("missing config should enter first-run setup")
	}
	state, err = manager.CreateConfig(CreateProjectInput{CWD: ".", Run: "go run . --port 8090"})
	if err != nil {
		t.Fatal(err)
	}
	if state.NeedsConfig || len(state.Projects) != 1 || state.Projects[0].Name != "backend" || state.Projects[0].Port == nil || *state.Projects[0].Port != 8090 {
		t.Fatalf("created workspace state = %#v", state)
	}
	data, err := os.ReadFile(filepath.Join(folder, configFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "cwd: .") || !strings.Contains(string(data), "run: go run . --port 8090") || !strings.Contains(string(data), "port: 8090") || strings.Contains(string(data), "projects:") {
		t.Fatalf("created config does not contain the expected top-level fields: %s", data)
	}
	if err := createProjectFile(folder, CreateProjectInput{CWD: ".", Run: "npm run dev"}); err == nil {
		t.Fatal("expected existing nala.yml to be preserved")
	}
}

func TestCreateConfigInfersPortFromListenerAddress(t *testing.T) {
	folder := projectTestFolder(t, "setup")
	if err := createProjectFile(folder, CreateProjectInput{
		CWD: ".",
		Run: "go run server.go --name setup --addr 127.0.0.1:8080",
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(folder, configFilename))
	if err != nil {
		t.Fatal(err)
	}
	config, err := decodeProjectFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Port; got == nil || *got != 8080 {
		t.Fatalf("created config port = %v, want 8080", got)
	}
}
