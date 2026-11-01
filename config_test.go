package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func projectTestFolder(t *testing.T, name string) string {
	t.Helper()
	folder := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(folder, 0755); err != nil {
		t.Fatal(err)
	}
	return folder
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return newManagerWithRegistryPath(filepath.Join(t.TempDir(), "user-config", "project-folders.json"))
}

func TestDecodeProjectFileAndResolveDefaults(t *testing.T) {
	folder := projectTestFolder(t, "nala-labs")
	data := []byte(`projects:
  backend:
    cwd: ./api
    run: go run . --port=8081
    deployment:
      schemaVersion: 1
      app:
        name: nala-grow-backend
      build:
        command: [go, build, ./cmd/server]
      test:
        command: [go, test, ./...]
      runtime:
        command: [./server]
  frontend:
    cwd: ./web
    run: npm run dev -- --port 5173
    deployment:
      schemaVersion: 1
      app:
        name: nala-grow-frontend
      build:
        command: [npm, run, build]
      test:
        command: [npm, run, test]
      runtime:
        command: [npm, run, start]
`)
	config, err := decodeProjectFile(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"backend", "frontend"} {
		deployment := config.Projects[name].Deployment
		if deployment == nil || deployment.Kind != yaml.MappingNode {
			t.Fatalf("%s deployment node = %#v, want a YAML mapping", name, deployment)
		}
	}
	project, services, err := projectForFolder(folder, config)
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "nala-labs" || len(project.Services) != 2 {
		t.Fatalf("project group = %#v, want one nala-labs project with two services", project)
	}
	backend := project.Services[0]
	if backend.Name != "backend" || backend.Port == nil || *backend.Port != 8081 {
		t.Fatalf("backend service = %#v, want inferred port 8081", backend)
	}
	if got, want := backend.LogPath, filepath.Join(folder, "api", "logs", "backend.log"); got != want {
		t.Fatalf("backend log path = %q, want %q", got, want)
	}
	frontend := project.Services[1]
	if frontend.Name != "frontend" || frontend.Port == nil || *frontend.Port != 5173 {
		t.Fatalf("frontend service = %#v, want inferred port 5173", frontend)
	}
	if len(services) != 2 || services[backend.ID].ProjectID != project.ID || services[frontend.ID].ProjectID != project.ID {
		t.Fatalf("resolved service specs = %#v, want two independent services in one group", services)
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
		{name: "missing services", data: "cwd: .\nrun: go run .\n"},
		{name: "empty services", data: "projects: {}\n"},
		{name: "missing cwd", data: "projects:\n  backend:\n    run: go run .\n"},
		{name: "missing run", data: "projects:\n  backend:\n    cwd: .\n"},
		{name: "invalid port", data: "projects:\n  backend:\n    cwd: .\n    run: go run .\n    port: 65536\n"},
		{name: "non-mapping deployment", data: "projects:\n  backend:\n    cwd: .\n    run: go run .\n    deployment: false\n"},
		{name: "unknown service field", data: "projects:\n  backend:\n    cwd: .\n    run: go run .\n    watch: true\n"},
		{name: "unknown top level field", data: "projects:\n  backend:\n    cwd: .\n    run: go run .\nwatch: true\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeProjectFile([]byte(test.data)); err == nil {
				t.Fatal("expected invalid config to fail")
			}
		})
	}
}

func TestProjectServicesCannotShareLogFile(t *testing.T) {
	folder := projectTestFolder(t, "nala-labs")
	config, err := decodeProjectFile([]byte("projects:\n  backend:\n    cwd: .\n    run: go run ./backend\n    log: logs/shared.log\n  frontend:\n    cwd: .\n    run: npm run dev\n    log: logs/shared.log\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := projectForFolder(folder, config); err == nil || !strings.Contains(err.Error(), "same log path") {
		t.Fatalf("services sharing a log path should be rejected, got %v", err)
	}
}

func TestCreateConfigWritesOneServiceProjectWithoutOverwriting(t *testing.T) {
	folder := projectTestFolder(t, "nala-labs")
	registryPath := filepath.Join(t.TempDir(), "user-config", "project-folders.json")
	manager := newManagerWithRegistryPath(registryPath)
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
	if state.NeedsConfig || len(state.Projects) != 1 || state.Projects[0].Name != "nala-labs" || len(state.Projects[0].Services) != 1 || state.Projects[0].Services[0].Name != "nala-labs" || state.Projects[0].Services[0].Port == nil || *state.Projects[0].Services[0].Port != 8090 {
		t.Fatalf("created workspace state = %#v", state)
	}
	data, err := os.ReadFile(filepath.Join(folder, configFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "projects:") || !strings.Contains(string(data), "nala-labs:") || !strings.Contains(string(data), "cwd: .") || !strings.Contains(string(data), "run: go run . --port 8090") || !strings.Contains(string(data), "port: 8090") {
		t.Fatalf("created config does not contain the expected project service map: %s", data)
	}
	if strings.Contains(string(data), "deployment:") {
		t.Fatalf("created config unexpectedly contains a deployment extension: %s", data)
	}
	restored := newManagerWithRegistryPath(registryPath).Workspace()
	if len(restored.Projects) != 1 || restored.Projects[0].Folder != folder {
		t.Fatalf("project created during setup was not restored: %#v", restored.Projects)
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
	if got := config.Projects["setup"].Port; got == nil || *got != 8080 {
		t.Fatalf("created config port = %v, want 8080", got)
	}
}
