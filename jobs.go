package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	jobStatusHealthy          = "healthy"
	jobStatusUnhealthy        = "unhealthy"
	jobStatusFailed           = "failed"
	jobStatusRunning          = "running"
	jobHistoryLimit           = 50
	jobRecentRunsLimit        = 15
	jobOutputLineLimit        = 200
	jobOutputByteLimit        = 24 * 1024
	openChamberFreeUsageJobID = "openchamber-free-usage"
)

type JobSchedule struct {
	IntervalMinutes int `json:"intervalMinutes"`
	OffsetMinutes   int `json:"offsetMinutes"`
}

type Job struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Schedule    JobSchedule `json:"schedule"`
	NextRunAt   time.Time   `json:"nextRunAt"`
	LastRun     *JobRun     `json:"lastRun,omitempty"`
}

type JobRun struct {
	ID         string     `json:"id"`
	JobID      string     `json:"jobId"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Status     string     `json:"status"`
	Summary    string     `json:"summary"`
}

type jobDefinition struct {
	id          string
	name        string
	description string
	projectName string
	schedule    JobSchedule
}

type manualJobRequest struct {
	definition jobDefinition
	result     chan manualJobResult
}

type manualJobResult struct {
	run JobRun
	err error
}

var defaultJobDefinitions = []jobDefinition{
	{id: "minikube-port-forwards", name: "Minikube port forwards", description: "Check and safely recover discovered kubectl port-forwards.", schedule: JobSchedule{IntervalMinutes: 60, OffsetMinutes: 0}},
	{id: "health-nala-labs", name: "Nala Labs health", description: "Check the loaded backend service at /healthz.", projectName: "nala-labs", schedule: JobSchedule{IntervalMinutes: 60, OffsetMinutes: 10}},
	{id: "health-nala-svc", name: "Nala SVC health", description: "Check the loaded backend service at /healthz.", projectName: "nala-svc", schedule: JobSchedule{IntervalMinutes: 60, OffsetMinutes: 20}},
	{id: "health-nala-trace", name: "Nala Trace health", description: "Check the loaded backend service at /healthz.", projectName: "nala-trace", schedule: JobSchedule{IntervalMinutes: 60, OffsetMinutes: 30}},
	{id: "health-nala-grow", name: "Nala Grow health", description: "Check the loaded backend service at /healthz.", projectName: "nala-grow", schedule: JobSchedule{IntervalMinutes: 60, OffsetMinutes: 40}},
	{id: openChamberFreeUsageJobID, name: "OpenChamber free usage reset", description: "Send one stateless request to check whether OpenCode Zen's DeepSeek V4.1 Flash Free model is available. This can consume one free request when the model responds.", schedule: JobSchedule{IntervalMinutes: 60, OffsetMinutes: 50}},
}

type JobScheduler struct {
	mu               sync.RWMutex
	runMu            sync.Mutex
	manager          *Manager
	path             string
	state            jobsFile
	loadErr          error
	errorHandler     func(string)
	ctx              context.Context
	cancel           context.CancelFunc
	done             chan struct{}
	manualRequests   chan manualJobRequest
	manualRunPending map[string]bool
	forwardsMu       sync.Mutex
	forwards         map[string]*managedForward
}

func newJobScheduler(manager *Manager, path string, pathErr error) *JobScheduler {
	return &JobScheduler{
		manager: manager, path: path, loadErr: pathErr, forwards: make(map[string]*managedForward),
		manualRequests: make(chan manualJobRequest), manualRunPending: make(map[string]bool),
	}
}

func (s *JobScheduler) SetErrorHandler(handler func(string)) {
	s.mu.Lock()
	s.errorHandler = handler
	s.mu.Unlock()
}

func (s *JobScheduler) reportError(message string) {
	s.mu.RLock()
	handler := s.errorHandler
	s.mu.RUnlock()
	if handler != nil {
		handler(message)
	}
}

func (s *JobScheduler) Start(parent context.Context) error {
	s.mu.Lock()
	if s.loadErr != nil {
		err := s.loadErr
		s.mu.Unlock()
		return err
	}
	if s.cancel != nil {
		s.mu.Unlock()
		return errors.New("job scheduler is already started")
	}
	if err := s.loadOrSeedLocked(time.Now()); err != nil {
		s.loadErr = err
		s.mu.Unlock()
		return err
	}
	s.ctx, s.cancel = context.WithCancel(parent)
	s.done = make(chan struct{})
	ctx := s.ctx
	done := s.done
	s.mu.Unlock()
	go s.loop(ctx, done)
	return nil
}

func (s *JobScheduler) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		if done != nil {
			<-done
		}
	}
	s.stopManagedForwards()
}

func (s *JobScheduler) GetJobs() ([]Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.loadErr != nil {
		return nil, errors.New("saved jobs could not be loaded")
	}
	jobs := make([]Job, 0, len(s.state.Jobs))
	for _, saved := range s.state.Jobs {
		job := Job{
			ID: saved.ID, Name: saved.Name, Description: saved.Description,
			Schedule: saved.Schedule, NextRunAt: saved.NextRunAt,
		}
		if len(saved.Runs) > 0 {
			last := saved.Runs[len(saved.Runs)-1].JobRun
			job.LastRun = &last
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *JobScheduler) RunJobNow(jobID string) (JobRun, error) {
	definition, exists := definitionForID(jobID)
	if !exists {
		return JobRun{}, errors.New("job was not found")
	}

	s.mu.Lock()
	if s.loadErr != nil {
		s.mu.Unlock()
		return JobRun{}, errors.New("saved jobs could not be run")
	}
	if s.cancel == nil || s.ctx == nil || s.ctx.Err() != nil {
		s.mu.Unlock()
		return JobRun{}, errors.New("job scheduler is not running")
	}
	if s.manualRunPending[jobID] {
		s.mu.Unlock()
		return JobRun{}, errors.New("this job already has a manual run pending or running")
	}
	s.manualRunPending[jobID] = true
	ctx := s.ctx
	s.mu.Unlock()

	request := manualJobRequest{definition: definition, result: make(chan manualJobResult, 1)}
	select {
	case s.manualRequests <- request:
	case <-ctx.Done():
		s.clearManualRunPending(jobID)
		return JobRun{}, errors.New("job scheduler stopped before the manual run could start")
	}

	result := <-request.result
	return result.run, result.err
}

func (s *JobScheduler) clearManualRunPending(jobID string) {
	s.mu.Lock()
	delete(s.manualRunPending, jobID)
	s.mu.Unlock()
}

func (s *JobScheduler) GetJobRuns(jobID string) ([]JobRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.loadErr != nil {
		return nil, errors.New("saved job runs could not be loaded")
	}
	saved := s.findJobLocked(jobID)
	if saved == nil {
		return nil, errors.New("job was not found")
	}
	start := len(saved.Runs) - jobRecentRunsLimit
	if start < 0 {
		start = 0
	}
	runs := make([]JobRun, 0, len(saved.Runs)-start)
	for i := len(saved.Runs) - 1; i >= start; i-- {
		runs = append(runs, saved.Runs[i].JobRun)
	}
	return runs, nil
}

func (s *JobScheduler) GetJobRunLog(jobID, runID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.loadErr != nil {
		return nil, errors.New("saved job output could not be loaded")
	}
	saved := s.findJobLocked(jobID)
	if saved == nil {
		return nil, errors.New("job was not found")
	}
	for _, run := range saved.Runs {
		if run.ID == runID {
			return append([]string{}, run.Output...), nil
		}
	}
	return nil, errors.New("job run was not found")
}

func (s *JobScheduler) UpdateJobSchedule(jobID string, schedule JobSchedule) (Job, error) {
	if err := validateJobSchedule(schedule); err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return Job{}, errors.New("saved jobs could not be updated")
	}
	saved := s.findJobLocked(jobID)
	if saved == nil {
		return Job{}, errors.New("job was not found")
	}
	previousSchedule := saved.Schedule
	previousNextRun := saved.NextRunAt
	saved.Schedule = schedule
	saved.NextRunAt = nextJobRunAt(schedule, time.Now())
	if err := writeJobsFile(s.path, s.state); err != nil {
		saved.Schedule = previousSchedule
		saved.NextRunAt = previousNextRun
		return Job{}, errors.New("the job schedule could not be saved")
	}
	job := Job{ID: saved.ID, Name: saved.Name, Description: saved.Description, Schedule: saved.Schedule, NextRunAt: saved.NextRunAt}
	if len(saved.Runs) > 0 {
		last := saved.Runs[len(saved.Runs)-1].JobRun
		job.LastRun = &last
	}
	return job, nil
}

func validateJobSchedule(schedule JobSchedule) error {
	if schedule.IntervalMinutes < 1 || schedule.IntervalMinutes > 1440 {
		return errors.New("interval must be between 1 and 1440 whole minutes")
	}
	if schedule.OffsetMinutes < 0 || schedule.OffsetMinutes >= schedule.IntervalMinutes {
		return errors.New("offset must be within the selected interval")
	}
	return nil
}

func nextJobRunAt(schedule JobSchedule, after time.Time) time.Time {
	if validateJobSchedule(schedule) != nil {
		return time.Time{}
	}
	location := after.Location()
	anchor := time.Date(after.Year(), after.Month(), after.Day(), 0, schedule.OffsetMinutes, 0, 0, location)
	if anchor.After(after) {
		return anchor
	}
	interval := time.Duration(schedule.IntervalMinutes) * time.Minute
	steps := after.Sub(anchor)/interval + 1
	return anchor.Add(steps * interval)
}

func advanceJobRunAt(schedule JobSchedule, previous, after time.Time) time.Time {
	if previous.IsZero() || validateJobSchedule(schedule) != nil {
		return nextJobRunAt(schedule, after)
	}
	if previous.After(after) {
		return previous
	}
	interval := time.Duration(schedule.IntervalMinutes) * time.Minute
	steps := after.Sub(previous)/interval + 1
	return previous.Add(steps * interval)
}

func (s *JobScheduler) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.runDue(ctx, now)
		case request := <-s.manualRequests:
			if ctx.Err() != nil {
				s.clearManualRunPending(request.definition.id)
				request.result <- manualJobResult{err: errors.New("job scheduler stopped before the manual run could start")}
				return
			}
			s.runMu.Lock()
			run, err := s.executeJob(ctx, request.definition)
			s.runMu.Unlock()
			s.clearManualRunPending(request.definition.id)
			request.result <- manualJobResult{run: run, err: err}
		}
	}
}

func (s *JobScheduler) runDue(ctx context.Context, now time.Time) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	var due []jobDefinition
	s.mu.Lock()
	if s.loadErr != nil {
		s.mu.Unlock()
		return
	}
	changed := false
	previousNextRuns := make(map[string]time.Time)
	for i := range s.state.Jobs {
		job := &s.state.Jobs[i]
		if job.NextRunAt.IsZero() || job.NextRunAt.After(now) {
			continue
		}
		definition, ok := definitionForID(job.ID)
		if !ok {
			continue
		}
		previousNextRuns[job.ID] = job.NextRunAt
		job.NextRunAt = advanceJobRunAt(job.Schedule, job.NextRunAt, now)
		due = append(due, definition)
		changed = true
	}
	if changed {
		if err := writeJobsFile(s.path, s.state); err != nil {
			for i := range s.state.Jobs {
				if previous, exists := previousNextRuns[s.state.Jobs[i].ID]; exists {
					s.state.Jobs[i].NextRunAt = previous
				}
			}
			s.mu.Unlock()
			s.reportError("Nala Ctrl could not save the next scheduled job times.")
			return
		}
	}
	s.mu.Unlock()
	sortJobDefinitions(due)
	for _, definition := range due {
		if ctx.Err() != nil {
			return
		}
		_, _ = s.executeJob(ctx, definition)
	}
}

func (s *JobScheduler) executeJob(ctx context.Context, definition jobDefinition) (JobRun, error) {
	started := time.Now()
	run := storedJobRun{JobRun: JobRun{ID: newJobRunID(), JobID: definition.id, StartedAt: started, Status: jobStatusRunning, Summary: "The check is running."}}
	s.mu.Lock()
	saved := s.findJobLocked(definition.id)
	if saved == nil {
		s.mu.Unlock()
		return JobRun{}, errors.New("job was not found")
	}
	previousRuns := saved.Runs
	saved.Runs = append(saved.Runs, run)
	if len(saved.Runs) > jobHistoryLimit {
		saved.Runs = append([]storedJobRun(nil), saved.Runs[len(saved.Runs)-jobHistoryLimit:]...)
	}
	if err := writeJobsFile(s.path, s.state); err != nil {
		saved.Runs = previousRuns
		s.mu.Unlock()
		s.reportError("Nala Ctrl could not save a job run, so that run was skipped.")
		return JobRun{}, errors.New("the manual job run could not be saved")
	}
	s.mu.Unlock()
	var output []string
	if definition.id == openChamberFreeUsageJobID {
		run.Status, run.Summary, output = runOpenChamberFreeUsageCheck(ctx)
	} else if definition.projectName != "" {
		run.Status, run.Summary, output = s.runProjectHealthCheck(ctx, definition.projectName)
	} else {
		run.Status, run.Summary, output = s.runMinikubePortForwardCheck(ctx)
	}
	finished := time.Now()
	run.FinishedAt = &finished
	run.Output = boundJobOutput(output)
	s.mu.Lock()
	var saveErr error
	if saved := s.findJobLocked(definition.id); saved != nil {
		for i := range saved.Runs {
			if saved.Runs[i].ID == run.ID {
				saved.Runs[i] = run
				break
			}
		}
		saveErr = writeJobsFile(s.path, s.state)
	}
	s.mu.Unlock()
	if saveErr != nil {
		s.reportError("Nala Ctrl could not save the completed job run.")
		return run.JobRun, errors.New("the completed manual job run could not be saved")
	}
	return run.JobRun, nil
}

func (s *JobScheduler) runProjectHealthCheck(ctx context.Context, projectName string) (string, string, []string) {
	spec, err := s.manager.loadedBackendService(projectName)
	if err != nil || spec.Port == nil || *spec.Port < 1 || *spec.Port > 65535 {
		return jobStatusFailed, "The loaded backend service or its configured port could not be resolved.", []string{"Backend service unavailable or missing a valid configured port."}
	}
	address := "http://127.0.0.1:" + strconv.Itoa(*spec.Port) + "/healthz"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return jobStatusFailed, "The local health check could not be created.", []string{"Could not create the local health check request."}
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Do(request)
	if err != nil {
		return jobStatusUnhealthy, "The backend health endpoint did not respond.", []string{"GET /healthz could not connect or timed out."}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return jobStatusUnhealthy, fmt.Sprintf("The backend health endpoint returned HTTP %d.", response.StatusCode), []string{fmt.Sprintf("GET /healthz returned HTTP %d.", response.StatusCode)}
	}
	return jobStatusHealthy, fmt.Sprintf("The backend health endpoint returned HTTP %d.", response.StatusCode), []string{fmt.Sprintf("GET /healthz returned HTTP %d.", response.StatusCode)}
}

func (m *Manager) loadedBackendService(projectName string) (projectSpec, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var projectID string
	for id, project := range m.projects {
		if strings.EqualFold(project.Name, projectName) {
			if projectID != "" {
				return projectSpec{}, errors.New("project name is ambiguous")
			}
			projectID = id
		}
	}
	if projectID == "" {
		return projectSpec{}, errors.New("project is not loaded")
	}
	var backend projectSpec
	for _, spec := range m.services {
		if spec.ProjectID == projectID && strings.EqualFold(spec.Name, "backend") {
			if backend.ID != "" {
				return projectSpec{}, errors.New("backend service is ambiguous")
			}
			backend = spec
		}
	}
	if backend.ID == "" {
		return projectSpec{}, errors.New("backend service is not configured")
	}
	return backend, nil
}

func (s *JobScheduler) findJobLocked(id string) *storedJob {
	for i := range s.state.Jobs {
		if s.state.Jobs[i].ID == id {
			return &s.state.Jobs[i]
		}
	}
	return nil
}

func definitionForID(id string) (jobDefinition, bool) {
	for _, definition := range defaultJobDefinitions {
		if definition.id == id {
			return definition, true
		}
	}
	return jobDefinition{}, false
}

func sortJobDefinitions(definitions []jobDefinition) {
	for i := 0; i < len(definitions); i++ {
		for j := i + 1; j < len(definitions); j++ {
			if definitions[j].schedule.OffsetMinutes < definitions[i].schedule.OffsetMinutes {
				definitions[i], definitions[j] = definitions[j], definitions[i]
			}
		}
	}
}

func newJobRunID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err == nil {
		return "job-run-" + hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("job-run-%d", time.Now().UnixNano())
}

func boundJobOutput(lines []string) []string {
	if len(lines) > jobOutputLineLimit {
		lines = lines[len(lines)-jobOutputLineLimit:]
	}
	bounded := make([]string, 0, len(lines))
	used := 0
	for _, line := range lines {
		line = strings.ToValidUTF8(strings.TrimSpace(line), "�")
		line = strings.ReplaceAll(line, "\x00", "")
		if line == "" {
			continue
		}
		remaining := jobOutputByteLimit - used
		if remaining <= 0 {
			break
		}
		if len(line) > remaining {
			line = line[:remaining]
			for len(line) > 0 && !utf8.ValidString(line) {
				line = line[:len(line)-1]
			}
		}
		bounded = append(bounded, line)
		used += len(line)
	}
	return bounded
}
