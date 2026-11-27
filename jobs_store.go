package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	jobsFileVersion       = 2
	legacyJobsFileVersion = 1
)

type jobsFile struct {
	Version int         `json:"version"`
	Jobs    []storedJob `json:"jobs"`
}

type storedJob struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	ProjectName  string            `json:"projectName,omitempty"`
	Enabled      bool              `json:"enabled"`
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
	if saved.Version != legacyJobsFileVersion && saved.Version != jobsFileVersion {
		return fmt.Errorf("unsupported saved jobs version %d", saved.Version)
	}
	if saved.Version == legacyJobsFileVersion {
		if err := migrateLegacyJobs(&saved); err != nil {
			return err
		}
	}
	jobsByID := make(map[string]storedJob, len(saved.Jobs))
	loaded := make([]storedJob, 0, len(saved.Jobs))
	for _, job := range saved.Jobs {
		if job.ID == "" {
			return errors.New("saved jobs contain a job without an identifier")
		}
		job.Name = strings.TrimSpace(job.Name)
		if job.Name == "" || utf8.RuneCountInString(job.Name) > jobNameLimit {
			return fmt.Errorf("saved job %q has an invalid name", job.ID)
		}
		job.Description = strings.TrimSpace(job.Description)
		if utf8.RuneCountInString(job.Description) > jobDescriptionLimit {
			return fmt.Errorf("saved job %q has an invalid description", job.ID)
		}
		if _, duplicate := jobsByID[job.ID]; duplicate {
			return fmt.Errorf("saved job %q is duplicated", job.ID)
		}
		if !knownJobKind(job.Kind) {
			return fmt.Errorf("saved job %q has an unsupported type", job.ID)
		}
		if job.Kind == jobKindProjectHealth {
			job.ProjectName = strings.TrimSpace(job.ProjectName)
			if job.ProjectName == "" || utf8.RuneCountInString(job.ProjectName) > jobProjectNameLimit {
				return fmt.Errorf("saved job %q has an invalid project name", job.ID)
			}
		} else {
			job.ProjectName = ""
		}
		if err := jobDefinitionConflict(loaded, job.Name, job.Kind, job.ProjectName, ""); err != nil {
			return fmt.Errorf("saved job %q conflicts with another job", job.ID)
		}
		if err := validateJobSchedule(job.Schedule); err != nil {
			return fmt.Errorf("saved job %q has an invalid schedule", job.ID)
		}
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
		loaded = append(loaded, job)
	}
	s.state = jobsFile{Version: jobsFileVersion, Jobs: loaded}
	return writeJobsFile(s.path, s.state)
}

func knownJobKind(kind string) bool {
	return kind == jobKindMinikubePortForward || kind == jobKindProjectHealth || kind == jobKindOpenChamberUsage
}

// migrateLegacyJobs upgrades the original fixed-definition file. Version 1
// stored only the six known identifiers, so their kind and project come from
// the starter definitions and every migrated job is enabled.
func migrateLegacyJobs(saved *jobsFile) error {
	for i := range saved.Jobs {
		job := &saved.Jobs[i]
		starter, known := starterJobForID(job.ID)
		if !known {
			return fmt.Errorf("saved job %q is not supported", job.ID)
		}
		job.Kind = starter.kind
		job.ProjectName = starter.projectName
		job.Enabled = true
		job.Name = starter.name
		job.Description = starter.description
	}
	saved.Version = jobsFileVersion
	return nil
}

func validJobRunStatus(status string) bool {
	return status == jobStatusRunning || status == jobStatusHealthy || status == jobStatusUnhealthy || status == jobStatusFailed
}

func seededJobsFile(now time.Time) jobsFile {
	state := jobsFile{Version: jobsFileVersion, Jobs: make([]storedJob, 0, len(starterJobDefinitions))}
	for _, definition := range starterJobDefinitions {
		state.Jobs = append(state.Jobs, storedJob{
			ID: definition.id, Kind: definition.kind, Name: definition.name, Description: definition.description,
			ProjectName: definition.projectName, Enabled: true,
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
