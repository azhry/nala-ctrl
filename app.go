package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ProjectManager is the Wails-bound application service. It manages project
// folders explicitly added from the app, with separate services from each
// folder's nala.yml.
type ProjectManager struct {
	ctx         context.Context
	manager     *Manager
	agentAPI    *http.Server
	dockerFixMu sync.Mutex
}

func NewProjectManager() *ProjectManager {
	return &ProjectManager{manager: NewManager()}
}

func (p *ProjectManager) startup(ctx context.Context) {
	p.ctx = ctx
	p.manager.SetLogHandler(func(event ProjectLogEvent) {
		runtime.EventsEmit(p.ctx, projectLogEventName, event)
	})
	p.manager.SetStateHandler(func(event ProjectStateEvent) {
		runtime.EventsEmit(p.ctx, projectStateEventName, event)
	})
	listener, err := listenAgentAPI(desktopAgentAPIListen)
	if err != nil {
		runtime.LogError(p.ctx, fmt.Sprintf("Nala Ctrl agent API could not start: %v", err))
		return
	}
	p.agentAPI = newAgentAPIServer(p.manager, agentAPIModeDesktop)
	go func() {
		if err := p.agentAPI.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			runtime.LogError(p.ctx, fmt.Sprintf("Nala Ctrl agent API stopped: %v", err))
		}
	}()
}

func (p *ProjectManager) shutdown(ctx context.Context) {
	if p.agentAPI != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := p.agentAPI.Shutdown(shutdownCtx); err != nil {
			runtime.LogError(p.ctx, fmt.Sprintf("Nala Ctrl agent API shutdown failed: %v", err))
		}
		cancel()
	}
	_ = p.manager.StopAll()
}

func (p *ProjectManager) ChooseFolder() (*WorkspaceState, error) {
	folder, err := runtime.OpenDirectoryDialog(p.ctx, runtime.OpenDialogOptions{
		Title: "Add a project folder containing nala.yml",
	})
	if err != nil {
		return nil, err
	}
	if folder == "" {
		return p.manager.Workspace(), nil
	}
	return p.manager.LoadFolder(folder)
}

func (p *ProjectManager) GetWorkspace() *WorkspaceState {
	return p.manager.Workspace()
}

func (p *ProjectManager) LoadFolder(folder string) (*WorkspaceState, error) {
	return p.manager.LoadFolder(folder)
}

func (p *ProjectManager) CreateConfig(input CreateProjectInput) (*WorkspaceState, error) {
	return p.manager.CreateConfig(input)
}

func (p *ProjectManager) GetProjectDetails(projectID string) (*ProjectDetails, error) {
	return p.manager.GetProjectDetails(projectID)
}

func (p *ProjectManager) GetProjectGitBranchState(projectID string) (*GitBranchState, error) {
	return p.manager.GetProjectGitBranchState(projectID)
}

func (p *ProjectManager) SwitchProjectGitBranch(projectID, branchName string) (*GitBranchState, error) {
	return p.manager.SwitchProjectGitBranch(projectID, branchName)
}

func (p *ProjectManager) RunProject(projectID string) (RunResult, error) {
	return p.manager.RunProject(projectID)
}

func (p *ProjectManager) TerminatePortOwnerAndRun(projectID string, pid int32, createTime int64) (RunResult, error) {
	return p.manager.TerminatePortOwnerAndRun(projectID, pid, createTime)
}

func (p *ProjectManager) StopProject(projectID string) error {
	return p.manager.StopProject(projectID)
}
