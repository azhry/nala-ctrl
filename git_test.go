package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProjectGitBranchStateAndSwitch(t *testing.T) {
	manager, project, gitPath := gitTestProject(t)
	gitTestCommand(t, gitPath, project.Folder, "switch", "feature/azh-523")
	if err := os.WriteFile(filepath.Join(project.Folder, "branch.txt"), []byte("feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, gitPath, project.Folder, "add", "branch.txt")
	gitTestCommand(t, gitPath, project.Folder, "-c", "user.name=Nala Ctrl Test", "-c", "user.email=nala-ctrl@example.test", "commit", "-m", "feature branch")
	gitTestCommand(t, gitPath, project.Folder, "switch", "main")

	state, err := manager.GetProjectGitBranchState(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Available || state.CurrentBranch != "main" {
		t.Fatalf("branch state = %#v, want an available main branch", state)
	}
	if !reflect.DeepEqual(state.Branches, []string{"feature/azh-523", "main"}) {
		t.Fatalf("local branches = %#v", state.Branches)
	}

	state, err = manager.SwitchProjectGitBranch(project.ID, "feature/azh-523")
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentBranch != "feature/azh-523" {
		t.Fatalf("current branch after switch = %q, want feature/azh-523", state.CurrentBranch)
	}
	contents, err := os.ReadFile(filepath.Join(project.Folder, "branch.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(contents)) != "feature" {
		t.Fatalf("checked-out file = %q, want feature branch contents", contents)
	}
}

func TestSwitchProjectGitBranchRefusesDirtyWorkingTree(t *testing.T) {
	manager, project, _ := gitTestProject(t)
	changedFile := filepath.Join(project.Folder, "branch.txt")
	if err := os.WriteFile(changedFile, []byte("uncommitted edit\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.SwitchProjectGitBranch(project.ID, "feature/azh-523"); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("switch error = %v, want dirty working tree refusal", err)
	}
	state, err := manager.GetProjectGitBranchState(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentBranch != "main" {
		t.Fatalf("current branch after refused switch = %q, want main", state.CurrentBranch)
	}
	contents, err := os.ReadFile(changedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "uncommitted edit\n" {
		t.Fatalf("dirty file after refused switch = %q, want the original edit", contents)
	}
}

func TestProjectGitBranchStateUnavailableOutsideGit(t *testing.T) {
	folder := projectTestFolder(t, "plain-project")
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte("projects:\n  api:\n    cwd: .\n    run: go run .\n"), 0644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	workspace, err := manager.LoadFolder(folder)
	if err != nil {
		t.Fatal(err)
	}

	state, err := manager.GetProjectGitBranchState(workspace.Projects[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Available || len(state.Branches) != 0 {
		t.Fatalf("branch state outside Git = %#v, want unavailable with no branches", state)
	}
}

func gitTestProject(t *testing.T) (*Manager, Project, string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git CLI is required for the repository integration test")
	}

	folder := projectTestFolder(t, "git-project")
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte("projects:\n  api:\n    cwd: .\n    run: go run .\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "branch.txt"), []byte("main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, gitPath, folder, "init")
	gitTestCommand(t, gitPath, folder, "symbolic-ref", "HEAD", "refs/heads/main")
	gitTestCommand(t, gitPath, folder, "add", "nala.yml", "branch.txt")
	gitTestCommand(t, gitPath, folder, "-c", "user.name=Nala Ctrl Test", "-c", "user.email=nala-ctrl@example.test", "commit", "-m", "initial project")
	gitTestCommand(t, gitPath, folder, "branch", "feature/azh-523")

	manager := NewManager()
	workspace, err := manager.LoadFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	return manager, workspace.Projects[0], gitPath
}

func gitTestCommand(t *testing.T, gitPath, folder string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", folder}, args...)
	output, err := exec.Command(gitPath, commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}
