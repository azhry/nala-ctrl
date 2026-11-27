package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedTestScheduler(t *testing.T, path string, now time.Time) *JobScheduler {
	t.Helper()
	scheduler := newJobScheduler(nil, path, nil)
	scheduler.mu.Lock()
	err := scheduler.loadOrSeedLocked(now)
	scheduler.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return scheduler
}

func readTestJobsFile(t *testing.T, path string) jobsFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved jobsFile
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestJobsFileMigratesLegacyDefinitionsAndKeepsDeletions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	legacy := `{
  "version": 1,
  "jobs": [
    {
      "id": "minikube-port-forwards",
      "name": "Old name",
      "description": "Old description",
      "schedule": {"intervalMinutes": 60, "offsetMinutes": 0},
      "nextRunAt": "2026-11-15T10:00:00Z",
      "runs": [
        {
          "id": "job-run-legacy",
          "jobId": "minikube-port-forwards",
          "startedAt": "2026-11-15T09:00:00Z",
          "finishedAt": "2026-11-15T09:00:10Z",
          "status": "healthy",
          "summary": "Legacy run",
          "output": ["Legacy output."]
        }
      ]
    }
  ]
}
`
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 15, 10, 5, 0, 0, time.UTC)
	scheduler := seedTestScheduler(t, path, now)

	jobs, err := scheduler.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("migrated %d jobs, want 1", len(jobs))
	}
	migrated := jobs[0]
	if migrated.Kind != jobKindMinikubePortForward || !migrated.Enabled {
		t.Fatalf("migrated definition = %#v, want an enabled Minikube job", migrated)
	}
	if migrated.Name != "Minikube port forwards" || migrated.Description == "Old description" {
		t.Fatalf("migrated name/description = %q/%q, want the starter copy", migrated.Name, migrated.Description)
	}
	if migrated.LastRun == nil || migrated.LastRun.ID != "job-run-legacy" {
		t.Fatalf("migrated last run = %#v", migrated.LastRun)
	}
	if saved := readTestJobsFile(t, path); saved.Version != jobsFileVersion {
		t.Fatalf("saved version = %d, want %d", saved.Version, jobsFileVersion)
	}

	if err := scheduler.DeleteJob("minikube-port-forwards"); err != nil {
		t.Fatal(err)
	}
	reloaded := seedTestScheduler(t, path, now.Add(time.Minute))
	jobs, err = reloaded.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("deleted job came back: %#v", jobs)
	}
}

func TestJobDefinitionsAreManagedByKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	now := time.Date(2026, time.November, 15, 10, 5, 0, 0, time.UTC)
	scheduler := seedTestScheduler(t, path, now)
	schedule := JobSchedule{IntervalMinutes: 30, OffsetMinutes: 5}

	if _, err := scheduler.CreateJob(JobDefinitionInput{
		Name: "Duplicate health", Kind: jobKindProjectHealth, ProjectName: "NALA-LABS", Enabled: true, Schedule: schedule,
	}); err == nil {
		t.Fatal("duplicate health job for one project was accepted")
	}
	if _, err := scheduler.CreateJob(JobDefinitionInput{
		Name: "Duplicate Minikube", Kind: jobKindMinikubePortForward, Enabled: true, Schedule: schedule,
	}); err == nil {
		t.Fatal("duplicate Minikube job was accepted")
	}
	if _, err := scheduler.CreateJob(JobDefinitionInput{
		Name: "Own health", Kind: jobKindProjectHealth, ProjectName: "nala-own", Enabled: true, Schedule: schedule,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.CreateJob(JobDefinitionInput{
		Name: "Own health", Kind: jobKindProjectHealth, ProjectName: "nala-other", Enabled: true, Schedule: schedule,
	}); err == nil {
		t.Fatal("duplicate health job name was accepted for a different project")
	}
	if _, err := scheduler.CreateJob(JobDefinitionInput{
		Name: "Second own health", Kind: jobKindProjectHealth, ProjectName: "nala-own", Enabled: true, Schedule: schedule,
	}); err == nil {
		t.Fatal("second health job for one project was accepted")
	}

	created, err := scheduler.CreateJob(JobDefinitionInput{
		Name: "Nala Fresh health", Description: "Check the fresh backend.", Kind: jobKindProjectHealth,
		ProjectName: "nala-fresh", Enabled: true, Schedule: schedule,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Kind != jobKindProjectHealth || !created.Enabled {
		t.Fatalf("created job = %#v", created)
	}
	if created.NextRunAt.IsZero() || !created.NextRunAt.After(now) {
		t.Fatalf("created next run = %s", created.NextRunAt)
	}

	updated, err := scheduler.UpdateJob(created.ID, JobDefinitionInput{
		Name: "Renamed health", Description: "Updated.", Kind: jobKindProjectHealth,
		ProjectName: "nala-fresh", Enabled: false, Schedule: JobSchedule{IntervalMinutes: 45, OffsetMinutes: 15},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Renamed health" || updated.Enabled || updated.Schedule.IntervalMinutes != 45 {
		t.Fatalf("updated job = %#v", updated)
	}
	if _, err := scheduler.UpdateJob(created.ID, JobDefinitionInput{
		Name: "Wrong kind", Kind: jobKindMinikubePortForward, Enabled: true, Schedule: schedule,
	}); err == nil {
		t.Fatal("changing a job type was accepted")
	}
	if _, err := scheduler.UpdateJob(created.ID, JobDefinitionInput{
		Name: "Bad schedule", Kind: jobKindProjectHealth, ProjectName: "nala-fresh", Enabled: true,
		Schedule: JobSchedule{IntervalMinutes: 0, OffsetMinutes: 0},
	}); err == nil {
		t.Fatal("invalid schedule was accepted")
	}

	reloaded := seedTestScheduler(t, path, now.Add(time.Minute))
	reloadedJobs, err := reloaded.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	persisted := false
	for _, job := range reloadedJobs {
		if job.ID != created.ID {
			continue
		}
		persisted = true
		if job.Name != "Renamed health" || job.Enabled || job.ProjectName != "nala-fresh" || job.Schedule.IntervalMinutes != 45 {
			t.Fatalf("reloaded job = %#v", job)
		}
	}
	if !persisted {
		t.Fatalf("created job %q was not persisted", created.ID)
	}

	if err := scheduler.DeleteJob(created.ID); err != nil {
		t.Fatal(err)
	}
	jobs, err := scheduler.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.ID == created.ID {
			t.Fatalf("deleted job %q is still listed", created.ID)
		}
	}
}

func TestSeededOpenChamberDefinitionCanBeEditedUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	scheduler := seedTestScheduler(t, path, time.Now())
	jobs, err := scheduler.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	var openChamberJob *Job
	for i := range jobs {
		if jobs[i].Kind == jobKindOpenChamberUsage {
			openChamberJob = &jobs[i]
			break
		}
	}
	if openChamberJob == nil {
		t.Fatal("seeded OpenChamber job was not found")
	}
	input := JobDefinitionInput{
		Name: "OpenChamber free usage monitor", Description: openChamberJob.Description,
		Kind: openChamberJob.Kind, ProjectName: openChamberJob.ProjectName,
		Enabled: openChamberJob.Enabled, Schedule: openChamberJob.Schedule,
	}
	updated, err := scheduler.UpdateJob(openChamberJob.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != input.Name || updated.Description != openChamberJob.Description {
		t.Fatalf("updated OpenChamber definition = %#v", updated)
	}
}

func TestPausedJobsAreSkippedButKeepHistoryAndNextRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		t.Errorf("paused job health check reached %s", request.URL.Path)
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	folder := projectTestFolder(t, "nala-labs")
	config := fmt.Sprintf("projects:\n  backend:\n    cwd: .\n    run: noop\n    port: %s\n", portText)
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	if _, err := manager.LoadFolder(folder); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jobs.json")
	now := time.Now().Add(-time.Hour)
	scheduler := newJobScheduler(manager, path, nil)
	scheduler.mu.Lock()
	if err := scheduler.loadOrSeedLocked(now); err != nil {
		scheduler.mu.Unlock()
		t.Fatal(err)
	}
	scheduler.mu.Unlock()

	due := time.Now().Add(time.Minute)
	scheduler.mu.Lock()
	for i := range scheduler.state.Jobs {
		job := &scheduler.state.Jobs[i]
		job.NextRunAt = now
		if job.ID != "health-nala-labs" {
			job.NextRunAt = due.Add(time.Hour)
		}
	}
	scheduler.mu.Unlock()

	paused := false
	jobs, err := scheduler.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.ID != "health-nala-labs" {
			continue
		}
		paused = true
		if _, err := scheduler.UpdateJob(job.ID, JobDefinitionInput{
			Name: job.Name, Description: job.Description, Kind: job.Kind, ProjectName: job.ProjectName,
			Enabled: false, Schedule: job.Schedule,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !paused {
		t.Fatal("Nala Labs health job was not seeded")
	}

	scheduler.runDue(context.Background(), due)

	jobs, err = scheduler.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.ID != "health-nala-labs" {
			continue
		}
		if !job.Enabled {
			if job.LastRun != nil {
				t.Fatalf("paused job ran: %#v", job.LastRun)
			}
			if !job.NextRunAt.After(now) {
				t.Fatalf("paused job next run = %s, want it advanced past %s", job.NextRunAt, now)
			}
		}
	}
}

func TestDeleteJobRefusesWhileRunIsActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	now := time.Date(2026, time.November, 15, 10, 5, 0, 0, time.UTC)
	scheduler := seedTestScheduler(t, path, now)

	scheduler.mu.Lock()
	saved := scheduler.findJobLocked("health-nala-labs")
	if saved == nil {
		scheduler.mu.Unlock()
		t.Fatal("Nala Labs health job was not seeded")
	}
	saved.Runs = append(saved.Runs, storedJobRun{JobRun: JobRun{
		ID: "job-run-active", JobID: saved.ID, StartedAt: now, Status: jobStatusRunning, Summary: "Running.",
	}})
	scheduler.mu.Unlock()

	if err := scheduler.DeleteJob("health-nala-labs"); err == nil {
		t.Fatal("deleting a job with an active run was accepted")
	}

	scheduler.mu.Lock()
	runs := scheduler.findJobLocked("health-nala-labs").Runs
	runs[len(runs)-1].Status = jobStatusFailed
	finished := now.Add(time.Second)
	runs[len(runs)-1].FinishedAt = &finished
	scheduler.mu.Unlock()

	if err := scheduler.DeleteJob("health-nala-labs"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteJobRefusesWhileRunIsPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	now := time.Date(2026, time.November, 15, 10, 5, 0, 0, time.UTC)
	scheduler := seedTestScheduler(t, path, now)

	scheduler.mu.Lock()
	scheduler.manualRunPending["health-nala-labs"] = true
	scheduler.mu.Unlock()

	if err := scheduler.DeleteJob("health-nala-labs"); err == nil {
		t.Fatal("deleting a job with a pending manual run was accepted")
	}
	jobs, err := scheduler.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range jobs {
		if job.ID == "health-nala-labs" {
			found = true
		}
	}
	if !found {
		t.Fatal("job with a pending manual run was deleted")
	}

	scheduler.mu.Lock()
	delete(scheduler.manualRunPending, "health-nala-labs")
	scheduler.mu.Unlock()
	scheduler.runMu.Lock()
	err = scheduler.DeleteJob("health-nala-labs")
	scheduler.runMu.Unlock()
	if err == nil {
		t.Fatal("deleting a job while another scheduler run is active was accepted")
	}
}
