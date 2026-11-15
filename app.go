package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ProjectManager is the Wails-bound application service. It manages project
// folders explicitly added from the app, with separate services from each
// folder's nala.yml.
type ProjectManager struct {
	ctx                 context.Context
	manager             *Manager
	agentAPI            *http.Server
	headlessAPI         *headlessAPIController
	configRefreshCancel context.CancelFunc
	configRefreshDone   chan struct{}
	dockerFixMu         sync.Mutex
}

const projectConfigRefreshInterval = 2 * time.Second

func NewProjectManager() *ProjectManager {
	manager := &ProjectManager{manager: NewManager()}
	manager.headlessAPI = newHeadlessAPIControllerForManager(defaultAgentAPIListen, manager.manager, manager.emitWorkspaceChanged)
	return manager
}

func (p *ProjectManager) startup(ctx context.Context) {
	p.ctx = ctx
	refreshCtx, cancelRefresh := context.WithCancel(ctx)
	p.configRefreshCancel = cancelRefresh
	p.configRefreshDone = make(chan struct{})
	go func() {
		defer close(p.configRefreshDone)
		ticker := time.NewTicker(projectConfigRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
				p.refreshProjectConfigs()
			}
		}
	}()
	p.manager.SetLogHandler(func(event ProjectLogEvent) {
		runtime.EventsEmit(p.ctx, projectLogEventName, event)
	})
	p.manager.SetStateHandler(func(event ProjectStateEvent) {
		runtime.EventsEmit(p.ctx, projectStateEventName, event)
	})
	listener, err := listenAgentAPI(desktopAgentAPIListen)
	if err != nil {
		runtime.LogError(p.ctx, fmt.Sprintf("Nala Ctrl agent API could not start: %v", err))
	} else {
		p.agentAPI = newAgentAPIServerWithWorkspaceHandler(p.manager, agentAPIModeDesktop, p.emitWorkspaceChanged)
		go func() {
			if err := p.agentAPI.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				runtime.LogError(p.ctx, fmt.Sprintf("Nala Ctrl agent API stopped: %v", err))
			}
		}()
	}

	if os.Getenv(headlessAPIDevAutostartEnv) == "1" {
		if _, err := p.headlessAPI.Start(); err != nil {
			runtime.LogError(p.ctx, fmt.Sprintf("Nala Ctrl headless API could not start: %v", err))
		}
	}
}

func (p *ProjectManager) emitWorkspaceChanged(folder string) {
	if p.ctx != nil {
		runtime.EventsEmit(p.ctx, workspaceChangedEventName, folder)
	}
}

func (p *ProjectManager) refreshProjectConfigs() {
	for _, folder := range p.manager.RefreshProjects() {
		p.emitWorkspaceChanged(folder)
	}
}

func (p *ProjectManager) shutdown(ctx context.Context) {
	if p.configRefreshCancel != nil {
		p.configRefreshCancel()
		<-p.configRefreshDone
	}
	if p.agentAPI != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := p.agentAPI.Shutdown(shutdownCtx); err != nil {
			runtime.LogError(p.ctx, fmt.Sprintf("Nala Ctrl agent API shutdown failed: %v", err))
		}
		cancel()
	}
	if _, err := p.headlessAPI.Stop(); err != nil {
		runtime.LogError(p.ctx, fmt.Sprintf("Nala Ctrl headless API shutdown failed: %v", err))
	}
	if err := p.manager.StopAll(); err != nil {
		runtime.LogError(p.ctx, fmt.Sprintf("Nala Ctrl service shutdown failed: %v", err))
	}
}

func (p *ProjectManager) GetHeadlessAPIStatus() HeadlessAPIStatus {
	return p.headlessAPI.Status()
}

func (p *ProjectManager) StartHeadlessAPI() (HeadlessAPIStatus, error) {
	return p.headlessAPI.Start()
}

func (p *ProjectManager) StopHeadlessAPI() (HeadlessAPIStatus, error) {
	return p.headlessAPI.Stop()
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
	p.refreshProjectConfigs()
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

func (p *ProjectManager) GetProjectStatus(projectID string) (*ServiceRuntimeStatus, error) {
	return p.manager.GetProjectStatus(projectID)
}

func (p *ProjectManager) GetProjectGitBranchState(projectID string) (*GitBranchState, error) {
	return p.manager.GetProjectGitBranchState(projectID)
}

func (p *ProjectManager) SwitchProjectGitBranch(projectID, branchName string) (*GitBranchState, error) {
	return p.manager.SwitchProjectGitBranch(projectID, branchName)
}

func (p *ProjectManager) SyncProjectGitWithMain(projectID string) (*GitBranchState, error) {
	return p.manager.SyncProjectGitWithMain(projectID)
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
