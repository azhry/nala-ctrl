package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const jobsFileVersion = 1

type jobsFile struct {
	Version int         `json:"version"`
	Jobs    []storedJob `json:"jobs"`
}

type storedJob struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Schedule     JobSchedule       `json:"schedule"`
	NextRunAt    time.Time         `json:"nextRunAt"`
	Runs         []storedJobRun    `json:"runs"`
	ForwardSpecs []portForwardSpec `json:"forwardSpecs,omitempty"`
}

type storedJobRun struct {
	JobRun
	Output []string `json:"output"`
}

func defaultJobsStorePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config folder: %w", err)
	}
	if strings.TrimSpace(configDir) == "" {
		return "", errors.New("user config folder is empty")
	}
	return filepath.Join(configDir, "Nala Ctrl", "jobs.json"), nil
}

func (s *JobScheduler) loadOrSeedLocked(now time.Time) error {
	data, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read saved jobs: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		s.state = seededJobsFile(now)
		return writeJobsFile(s.path, s.state)
	}
	var saved jobsFile
	if err := json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("decode saved jobs: %w", err)
	}
	if saved.Version != jobsFileVersion {
		return fmt.Errorf("unsupported saved jobs version %d", saved.Version)
	}
	jobsByID := make(map[string]storedJob, len(saved.Jobs))
	for _, job := range saved.Jobs {
		definition, known := definitionForID(job.ID)
		if !known {
			return fmt.Errorf("saved job %q is not supported", job.ID)
		}
		if _, duplicate := jobsByID[job.ID]; duplicate {
			return fmt.Errorf("saved job %q is duplicated", job.ID)
		}
		if err := validateJobSchedule(job.Schedule); err != nil {
			return fmt.Errorf("saved job %q has an invalid schedule", job.ID)
		}
		job.Name = definition.name
		job.Description = definition.description
		if job.Runs == nil {
			job.Runs = []storedJobRun{}
		}
		runIDs := make(map[string]bool, len(job.Runs))
		for _, run := range job.Runs {
			if run.ID == "" || run.JobID != job.ID || runIDs[run.ID] || !validJobRunStatus(run.Status) {
				return fmt.Errorf("saved job %q contains an invalid run", job.ID)
			}
			runIDs[run.ID] = true
		}
		if len(job.Runs) > jobHistoryLimit {
			job.Runs = append([]storedJobRun(nil), job.Runs[len(job.Runs)-jobHistoryLimit:]...)
		}
		for i := range job.Runs {
			job.Runs[i].Output = boundJobOutput(job.Runs[i].Output)
			if job.Runs[i].Status == jobStatusRunning {
				job.Runs[i].Status = jobStatusFailed
				job.Runs[i].Summary = "The app closed before this job finished."
				finishedAt := now
				job.Runs[i].FinishedAt = &finishedAt
				job.Runs[i].Output = append(job.Runs[i].Output, "The app closed before the job finished.")
			}
		}
		if len(job.ForwardSpecs) > 128 {
			return fmt.Errorf("saved job %q has too many port-forward definitions", job.ID)
		}
		forwardPorts := make(map[int]bool, len(job.ForwardSpecs))
		forwardIDs := make(map[string]bool, len(job.ForwardSpecs))
		for _, spec := range job.ForwardSpecs {
			if !safeStoredPortForward(spec) || forwardPorts[spec.LocalPort] || forwardIDs[spec.ID] {
				return fmt.Errorf("saved job %q contains an unsafe port-forward definition", job.ID)
			}
			forwardPorts[spec.LocalPort] = true
			forwardIDs[spec.ID] = true
		}
		if job.NextRunAt.IsZero() {
			job.NextRunAt = nextJobRunAt(job.Schedule, now)
		} else if !job.NextRunAt.After(now) {
			job.NextRunAt = advanceJobRunAt(job.Schedule, job.NextRunAt, now)
		}
		jobsByID[job.ID] = job
	}
	s.state = jobsFile{Version: jobsFileVersion, Jobs: make([]storedJob, 0, len(defaultJobDefinitions))}
	for _, definition := range defaultJobDefinitions {
		job, exists := jobsByID[definition.id]
		if !exists {
			job = storedJob{
				ID: definition.id, Name: definition.name, Description: definition.description,
				Schedule: definition.schedule, NextRunAt: nextJobRunAt(definition.schedule, now),
				Runs: []storedJobRun{},
			}
		}
		s.state.Jobs = append(s.state.Jobs, job)
	}
	return writeJobsFile(s.path, s.state)
}

func validJobRunStatus(status string) bool {
	return status == jobStatusRunning || status == jobStatusHealthy || status == jobStatusUnhealthy || status == jobStatusFailed
}

func seededJobsFile(now time.Time) jobsFile {
	state := jobsFile{Version: jobsFileVersion, Jobs: make([]storedJob, 0, len(defaultJobDefinitions))}
	for _, definition := range defaultJobDefinitions {
		state.Jobs = append(state.Jobs, storedJob{
			ID: definition.id, Name: definition.name, Description: definition.description,
			Schedule: definition.schedule, NextRunAt: nextJobRunAt(definition.schedule, now),
			Runs: []storedJobRun{},
		})
	}
	return state
}

func writeJobsFile(path string, state jobsFile) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("saved jobs path is empty")
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode saved jobs: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create saved jobs folder: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".jobs-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary saved jobs file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set saved jobs permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write saved jobs: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync saved jobs: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close saved jobs: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace saved jobs: %w", err)
	}
	return nil
}
