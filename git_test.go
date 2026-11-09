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

func TestSwitchProjectGitBranchAllowsBranchCheckedOutInAnotherWorktree(t *testing.T) {
	manager, project, gitPath := gitTestProject(t)
	otherWorktree := filepath.Join(filepath.Dir(project.Folder), "git-project-other-worktree")
	gitTestCommand(t, gitPath, project.Folder, "worktree", "add", otherWorktree, "feature/azh-523")

	state, err := manager.SwitchProjectGitBranch(project.ID, "feature/azh-523")
	if err != nil {
		t.Fatalf("switch to branch checked out in another worktree: %v", err)
	}
	if state.CurrentBranch != "feature/azh-523" {
		t.Fatalf("current branch after switch = %q, want feature/azh-523", state.CurrentBranch)
	}
	otherBranch, err := runGit(gitPath, otherWorktree, "branch", "--show-current")
	if err != nil {
		t.Fatal(err)
	}
	if otherBranch != "feature/azh-523" {
		t.Fatalf("branch in other worktree = %q, want feature/azh-523", otherBranch)
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
	manager := newTestManager(t)
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

func TestSyncProjectGitWithMainFastForwardsWithoutSwitchingBranches(t *testing.T) {
	manager, project, gitPath, _, upstream := gitSyncTestProject(t)
	gitTestCommitFile(t, gitPath, upstream, "from-main.txt", "main update\n", "main update")
	gitTestCommand(t, gitPath, upstream, "push", "origin", "main")

	state, err := manager.SyncProjectGitWithMain(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentBranch != "main" {
		t.Fatalf("current branch after sync = %q, want main", state.CurrentBranch)
	}
	contents, err := os.ReadFile(filepath.Join(project.Folder, "from-main.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(contents)) != "main update" {
		t.Fatalf("synced file = %q, want main update", contents)
	}
}

func TestSyncProjectGitWithMainMergesDivergedCurrentBranch(t *testing.T) {
	manager, project, gitPath, _, upstream := gitSyncTestProject(t)
	gitTestCommand(t, gitPath, project.Folder, "switch", "feature/azh-523")
	gitTestCommitFile(t, gitPath, project.Folder, "from-feature.txt", "feature update\n", "feature update")
	gitTestCommitFile(t, gitPath, upstream, "from-main.txt", "main update\n", "main update")
	gitTestCommand(t, gitPath, upstream, "push", "origin", "main")

	state, err := manager.SyncProjectGitWithMain(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentBranch != "feature/azh-523" {
		t.Fatalf("current branch after sync = %q, want feature/azh-523", state.CurrentBranch)
	}
	for _, name := range []string{"from-feature.txt", "from-main.txt"} {
		if _, err := os.Stat(filepath.Join(project.Folder, name)); err != nil {
			t.Fatalf("merged file %q is unavailable: %v", name, err)
		}
	}
	parents, err := runGit(gitPath, project.Folder, "rev-list", "--parents", "-n", "1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Fields(parents)) != 3 {
		t.Fatalf("merge commit parents = %q, want one commit with two parents", parents)
	}
}

func TestSyncProjectGitWithMainRefusesDirtyTreeBeforeFetch(t *testing.T) {
	manager, project, gitPath, _, upstream := gitSyncTestProject(t)
	gitTestCommitFile(t, gitPath, upstream, "from-main.txt", "main update\n", "main update")
	gitTestCommand(t, gitPath, upstream, "push", "origin", "main")
	changedFile := filepath.Join(project.Folder, "branch.txt")
	if err := os.WriteFile(changedFile, []byte("uncommitted edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	untrackedFile := filepath.Join(project.Folder, "untracked.txt")
	if err := os.WriteFile(untrackedFile, []byte("untracked content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fetchHead := filepath.Join(project.Folder, ".git", "FETCH_HEAD")
	if _, err := os.Stat(fetchHead); !os.IsNotExist(err) {
		t.Fatalf("FETCH_HEAD before sync error = %v, want absent file", err)
	}

	if _, err := manager.SyncProjectGitWithMain(project.ID); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("sync error = %v, want dirty working tree refusal", err)
	}
	if _, err := os.Stat(fetchHead); !os.IsNotExist(err) {
		t.Fatalf("FETCH_HEAD after refused sync error = %v, want absent file", err)
	}
	contents, err := os.ReadFile(changedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "uncommitted edit\n" {
		t.Fatalf("dirty file after refused sync = %q, want the original edit", contents)
	}
	untrackedContents, err := os.ReadFile(untrackedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(untrackedContents) != "untracked content\n" {
		t.Fatalf("untracked file after refused sync = %q, want the original content", untrackedContents)
	}
}

func TestSyncProjectGitWithMainAbortsConflictAndPreservesCurrentBranch(t *testing.T) {
	manager, project, gitPath, _, upstream := gitSyncTestProject(t)
	gitTestCommitFile(t, gitPath, upstream, "branch.txt", "upstream main edit\n", "upstream main edit")
	gitTestCommand(t, gitPath, upstream, "push", "origin", "main")
	gitTestCommitFile(t, gitPath, project.Folder, "branch.txt", "local branch edit\n", "local branch edit")

	if _, err := manager.SyncProjectGitWithMain(project.ID); err == nil || !strings.Contains(err.Error(), "conflicted") || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("sync error = %v, want reported and aborted conflict", err)
	}
	state, err := manager.GetProjectGitBranchState(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentBranch != "main" {
		t.Fatalf("current branch after aborted conflict = %q, want main", state.CurrentBranch)
	}
	status, err := runGit(gitPath, project.Folder, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		t.Fatalf("working tree after aborted conflict = %q, want clean", status)
	}
	contents, err := os.ReadFile(filepath.Join(project.Folder, "branch.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(contents)) != "local branch edit" {
		t.Fatalf("file after aborted conflict = %q, want local branch contents", contents)
	}
}

func TestSyncProjectGitWithMainReportsFetchFailureWithoutChangingBranch(t *testing.T) {
	manager, project, gitPath, _, _ := gitSyncTestProject(t)
	missingRemote := filepath.Join(filepath.Dir(project.Folder), "missing-origin.git")
	gitTestCommand(t, gitPath, project.Folder, "remote", "set-url", "origin", missingRemote)

	if _, err := manager.SyncProjectGitWithMain(project.ID); err == nil || !strings.Contains(err.Error(), "fetch origin/main failed") {
		t.Fatalf("sync error = %v, want a visible fetch failure", err)
	}
	state, err := manager.GetProjectGitBranchState(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentBranch != "main" {
		t.Fatalf("current branch after failed fetch = %q, want main", state.CurrentBranch)
	}
	status, err := runGit(gitPath, project.Folder, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		t.Fatalf("working tree after failed fetch = %q, want clean", status)
	}
}

func TestSyncProjectGitWithMainRefusesDetachedHead(t *testing.T) {
	manager, project, gitPath, _, _ := gitSyncTestProject(t)
	gitTestCommand(t, gitPath, project.Folder, "checkout", "--detach")

	if _, err := manager.SyncProjectGitWithMain(project.ID); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Fatalf("sync error = %v, want detached HEAD refusal", err)
	}
}

func TestSyncProjectGitWithMainIgnoresStaleRebaseHead(t *testing.T) {
	manager, project, gitPath, _, upstream := gitSyncTestProject(t)
	gitTestCommitFile(t, gitPath, upstream, "from-main.txt", "main update\n", "main update")
	gitTestCommand(t, gitPath, upstream, "push", "origin", "main")
	gitTestCommand(t, gitPath, project.Folder, "update-ref", "REBASE_HEAD", "HEAD")

	state, err := manager.SyncProjectGitWithMain(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentBranch != "main" {
		t.Fatalf("current branch after sync = %q, want main", state.CurrentBranch)
	}
	if _, err := os.Stat(filepath.Join(project.Folder, "from-main.txt")); err != nil {
		t.Fatalf("main update was not merged with stale REBASE_HEAD present: %v", err)
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

	manager := newTestManager(t)
	workspace, err := manager.LoadFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	return manager, workspace.Projects[0], gitPath
}

func gitSyncTestProject(t *testing.T) (*Manager, Project, string, string, string) {
	t.Helper()
	manager, project, gitPath := gitTestProject(t)
	parent := filepath.Dir(project.Folder)
	remote := filepath.Join(parent, "git-project-origin.git")
	upstream := filepath.Join(parent, "git-project-upstream")
	if err := os.MkdirAll(remote, 0755); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, gitPath, parent, "init", "--bare", "--initial-branch=main", remote)
	gitTestCommand(t, gitPath, project.Folder, "config", "user.name", "Nala Ctrl Test")
	gitTestCommand(t, gitPath, project.Folder, "config", "user.email", "nala-ctrl@example.test")
	gitTestCommand(t, gitPath, project.Folder, "config", "commit.gpgsign", "false")
	gitTestCommand(t, gitPath, project.Folder, "remote", "add", "origin", remote)
	gitTestCommand(t, gitPath, project.Folder, "push", "--set-upstream", "origin", "main")
	gitTestCommand(t, gitPath, parent, "clone", remote, upstream)
	gitTestCommand(t, gitPath, upstream, "config", "user.name", "Nala Ctrl Test")
	gitTestCommand(t, gitPath, upstream, "config", "user.email", "nala-ctrl@example.test")
	gitTestCommand(t, gitPath, upstream, "config", "commit.gpgsign", "false")
	return manager, project, gitPath, remote, upstream
}

func gitTestCommitFile(t *testing.T, gitPath, folder, name, contents, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(folder, name), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, gitPath, folder, "add", name)
	gitTestCommand(t, gitPath, folder, "commit", "-m", message)
}

func gitTestCommand(t *testing.T, gitPath, folder string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", folder}, args...)
	output, err := exec.Command(gitPath, commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}
