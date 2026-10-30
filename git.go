package main

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type GitBranchState struct {
	Available      bool     `json:"available"`
	RepositoryRoot string   `json:"repositoryRoot"`
	CurrentBranch  string   `json:"currentBranch"`
	Branches       []string `json:"branches"`
}

func (m *Manager) GetProjectGitBranchState(projectID string) (*GitBranchState, error) {
	m.gitMu.Lock()
	defer m.gitMu.Unlock()

	folder, err := m.projectFolder(projectID)
	if err != nil {
		return nil, err
	}
	return readGitBranchState(folder)
}

func (m *Manager) SwitchProjectGitBranch(projectID, branchName string) (*GitBranchState, error) {
	m.gitMu.Lock()
	defer m.gitMu.Unlock()

	folder, err := m.projectFolder(projectID)
	if err != nil {
		return nil, err
	}
	state, err := readGitBranchState(folder)
	if err != nil {
		return nil, err
	}
	if !state.Available {
		return nil, errors.New("project folder is not inside a Git working tree")
	}
	if branchName == "" {
		return nil, errors.New("choose a local branch")
	}
	if state.CurrentBranch == branchName {
		return state, nil
	}
	if !containsBranch(state.Branches, branchName) {
		return nil, fmt.Errorf("branch %q is not an existing local branch", branchName)
	}

	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("Git is not available on PATH: %w", err)
	}
	status, err := runGit(gitPath, state.RepositoryRoot, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("check the Git working tree before switching branches: %w", err)
	}
	if status != "" {
		return nil, errors.New("working tree has uncommitted changes; commit or move them before switching branches")
	}
	if _, err := runGit(gitPath, state.RepositoryRoot, "switch", "--ignore-other-worktrees", "--no-guess", "--", branchName); err != nil {
		return nil, fmt.Errorf("switch to branch %q: %w", branchName, err)
	}
	return readGitBranchState(folder)
}

func (m *Manager) projectFolder(projectID string) (string, error) {
	m.mu.RLock()
	project, exists := m.projects[projectID]
	m.mu.RUnlock()
	if !exists {
		return "", fmt.Errorf("project %q is not loaded; add its folder first", projectID)
	}
	return project.Folder, nil
}

func readGitBranchState(folder string) (*GitBranchState, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("Git is not available on PATH: %w", err)
	}
	repositoryRoot, err := runGit(gitPath, folder, "rev-parse", "--show-toplevel")
	if err != nil {
		return &GitBranchState{Branches: []string{}}, nil
	}
	currentBranch, err := runGit(gitPath, repositoryRoot, "branch", "--show-current")
	if err != nil {
		return nil, fmt.Errorf("read current Git branch: %w", err)
	}
	branchOutput, err := runGit(gitPath, repositoryRoot, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, fmt.Errorf("list local Git branches: %w", err)
	}
	branches := []string{}
	for _, branch := range strings.Split(branchOutput, "\n") {
		if branch = strings.TrimSpace(branch); branch != "" {
			branches = append(branches, branch)
		}
	}
	sort.Strings(branches)
	return &GitBranchState{
		Available:      true,
		RepositoryRoot: repositoryRoot,
		CurrentBranch:  currentBranch,
		Branches:       branches,
	}, nil
}

func containsBranch(branches []string, candidate string) bool {
	for _, branch := range branches {
		if branch == candidate {
			return true
		}
	}
	return false
}

func runGit(gitPath, folder string, args ...string) (string, error) {
	commandArgs := []string{"-C", folder}
	commandArgs = append(commandArgs, args...)
	output, err := exec.Command(gitPath, commandArgs...).CombinedOutput()
	if err != nil {
		details := strings.TrimSpace(string(output))
		if details == "" {
			return "", fmt.Errorf("%s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, details)
	}
	return strings.TrimSpace(string(output)), nil
}
