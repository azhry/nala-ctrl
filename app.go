package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ProjectManager is the Wails-bound application service. It only manages
// projects declared by the currently selected nala.yml.
type ProjectManager struct {
	ctx     context.Context
	manager *Manager
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
}

func (p *ProjectManager) shutdown(ctx context.Context) {
	_ = p.manager.StopAll()
}

func (p *ProjectManager) ChooseFolder() (*WorkspaceState, error) {
	folder, err := runtime.OpenDirectoryDialog(p.ctx, runtime.OpenDialogOptions{
		Title: "Choose a folder containing nala.yml",
	})
	if err != nil {
		return nil, err
	}
	if folder == "" {
		return p.manager.Workspace(), nil
	}
	return p.manager.LoadFolder(folder)
}

func (p *ProjectManager) LoadFolder(folder string) (*WorkspaceState, error) {
	return p.manager.LoadFolder(folder)
}

func (p *ProjectManager) CreateConfig(input CreateProjectInput) (*WorkspaceState, error) {
	return p.manager.CreateConfig(input)
}

func (p *ProjectManager) GetProjectDetails(name string) (*ProjectDetails, error) {
	return p.manager.GetProjectDetails(name)
}

func (p *ProjectManager) RunProject(name string) (RunResult, error) {
	return p.manager.RunProject(name)
}

func (p *ProjectManager) TerminatePortOwnerAndRun(name string, pid int32, createTime int64) (RunResult, error) {
	return p.manager.TerminatePortOwnerAndRun(name, pid, createTime)
}

func (p *ProjectManager) StopProject(name string) error {
	return p.manager.StopProject(name)
}
