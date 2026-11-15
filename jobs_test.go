package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNextJobRunAtUsesLocalOffsetAndInterval(t *testing.T) {
	location := time.FixedZone("task-zone", 7*60*60)
	current := time.Date(2026, time.November, 15, 10, 5, 0, 0, location)
	got := nextJobRunAt(JobSchedule{IntervalMinutes: 60, OffsetMinutes: 10}, current)
	want := time.Date(2026, time.November, 15, 10, 10, 0, 0, location)
	if !got.Equal(want) {
		t.Fatalf("next hourly run = %s, want %s", got, want)
	}

	seventyMinute := nextJobRunAt(JobSchedule{IntervalMinutes: 70, OffsetMinutes: 10}, current)
	wantSeventyMinute := time.Date(2026, time.November, 15, 10, 40, 0, 0, location)
	if !seventyMinute.Equal(wantSeventyMinute) {
		t.Fatalf("next 70-minute run = %s, want %s", seventyMinute, wantSeventyMinute)
	}
	advanced := advanceJobRunAt(JobSchedule{IntervalMinutes: 70, OffsetMinutes: 10}, seventyMinute, time.Date(2026, time.November, 15, 12, 20, 0, 0, location))
	wantAdvanced := time.Date(2026, time.November, 15, 13, 0, 0, 0, location)
	if !advanced.Equal(wantAdvanced) {
		t.Fatalf("advanced run = %s, want %s", advanced, wantAdvanced)
	}
}

func TestJobSchedulerSeedsFiveStaggeredJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "jobs.json")
	scheduler := newJobScheduler(nil, path, nil)
	now := time.Date(2026, time.November, 15, 10, 5, 0, 0, time.Local)
	scheduler.mu.Lock()
	err := scheduler.loadOrSeedLocked(now)
	scheduler.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := scheduler.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 5 {
		t.Fatalf("seeded %d jobs, want 5", len(jobs))
	}
	ids := make([]string, len(jobs))
	offsets := make([]int, len(jobs))
	for i, job := range jobs {
		ids[i] = job.ID
		offsets[i] = job.Schedule.OffsetMinutes
		if job.Schedule.IntervalMinutes != 60 {
			t.Errorf("job %q interval = %d, want 60", job.ID, job.Schedule.IntervalMinutes)
		}
	}
	if !reflect.DeepEqual(ids, []string{"minikube-port-forwards", "health-nala-labs", "health-nala-svc", "health-nala-trace", "health-nala-grow"}) {
		t.Fatalf("seeded job IDs = %v", ids)
	}
	if !reflect.DeepEqual(offsets, []int{0, 10, 20, 30, 40}) {
		t.Fatalf("seeded offsets = %v", offsets)
	}
}

func TestManualJobRunIsPersistedWithoutAdvancingSchedule(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			t.Errorf("health path = %q, want /healthz", request.URL.Path)
		}
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
	scheduler := newJobScheduler(manager, filepath.Join(t.TempDir(), "jobs.json"), nil)
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer scheduler.Stop()

	before, err := scheduler.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	var original Job
	for _, job := range before {
		if job.ID == "health-nala-labs" {
			original = job
			break
		}
	}
	if original.ID == "" {
		t.Fatal("Nala Labs health job was not seeded")
	}

	run, err := scheduler.RunJobNow(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != jobStatusHealthy || run.FinishedAt == nil {
		t.Fatalf("manual run = %#v, want a finished healthy run", run)
	}
	after, err := scheduler.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	var updated Job
	for _, job := range after {
		if job.ID == original.ID {
			updated = job
			break
		}
	}
	if !updated.NextRunAt.Equal(original.NextRunAt) || updated.Schedule != original.Schedule {
		t.Fatalf("manual run changed the schedule: before %#v, after %#v", original, updated)
	}
	runs, err := scheduler.GetJobRuns(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("persisted runs = %#v, want manual run %q", runs, run.ID)
	}
	output, err := scheduler.GetJobRunLog(original.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(output, []string{"GET /healthz returned HTTP 204."}) {
		t.Fatalf("manual run output = %v", output)
	}
}

func TestManualJobRequestsShareOneWorkerAndRejectDuplicates(t *testing.T) {
	var active atomic.Int32
	var maximumActive atomic.Int32
	var requestCount atomic.Int32
	started := make(chan int32, 2)
	release := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current := active.Add(1)
		for observed := maximumActive.Load(); current > observed; observed = maximumActive.Load() {
			if maximumActive.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- requestCount.Add(1)
		select {
		case <-release:
		case <-request.Context().Done():
		}
		active.Add(-1)
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	for _, projectName := range []string{"nala-labs", "nala-svc"} {
		folder := projectTestFolder(t, projectName)
		config := fmt.Sprintf("projects:\n  backend:\n    cwd: .\n    run: noop\n    port: %s\n", portText)
		if err := os.WriteFile(filepath.Join(folder, configFilename), []byte(config), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.LoadFolder(folder); err != nil {
			t.Fatal(err)
		}
	}
	scheduler := newJobScheduler(manager, filepath.Join(t.TempDir(), "jobs.json"), nil)
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer scheduler.Stop()

	scheduler.mu.Lock()
	for i := range scheduler.state.Jobs {
		scheduler.state.Jobs[i].NextRunAt = time.Now().Add(time.Hour)
	}
	if err := writeJobsFile(scheduler.path, scheduler.state); err != nil {
		scheduler.mu.Unlock()
		t.Fatal(err)
	}
	scheduler.mu.Unlock()
	before, err := scheduler.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan manualJobResult, 2)
	go func() {
		run, err := scheduler.RunJobNow("health-nala-labs")
		result <- manualJobResult{run: run, err: err}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first manual job did not start")
	}
	go func() {
		run, err := scheduler.RunJobNow("health-nala-svc")
		result <- manualJobResult{run: run, err: err}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		scheduler.mu.RLock()
		pending := scheduler.manualRunPending["health-nala-svc"]
		scheduler.mu.RUnlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second manual job did not enter the queue")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := scheduler.RunJobNow("health-nala-svc"); err == nil {
		t.Fatal("duplicate manual request succeeded while the job was pending")
	}
	select {
	case <-started:
		t.Fatal("second manual job started before the first one finished")
	case <-time.After(50 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("second manual job did not start after the first one finished")
	}
	release <- struct{}{}
	for range 2 {
		select {
		case completed := <-result:
			if completed.err != nil || completed.run.Status != jobStatusHealthy {
				t.Fatalf("manual run result = %#v, %v", completed.run, completed.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("manual job did not complete")
		}
	}
	if got := maximumActive.Load(); got != 1 {
		t.Fatalf("maximum simultaneous health requests = %d, want 1", got)
	}
	after, err := scheduler.GetJobs()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"health-nala-labs", "health-nala-svc"} {
		var beforeNext, afterNext time.Time
		for _, job := range before {
			if job.ID == name {
				beforeNext = job.NextRunAt
			}
		}
		for _, job := range after {
			if job.ID == name {
				afterNext = job.NextRunAt
			}
		}
		if beforeNext.IsZero() || !afterNext.Equal(beforeNext) {
			t.Errorf("manual run changed %s next run: before %s, after %s", name, beforeNext, afterNext)
		}
	}
}

func TestParseKubectlPortForwardRequiresOneLoopbackMapping(t *testing.T) {
	workingDir := t.TempDir()
	executable := filepath.Join(workingDir, "kubectl")
	spec, err := parseKubectlPortForward(executable, []string{"--context", "minikube", "port-forward", "svc/api", "8100:80", "--address=127.0.0.1,::1"}, workingDir)
	if err != nil {
		t.Fatal(err)
	}
	if spec.LocalPort != 8100 || spec.RemotePort != 80 || !reflect.DeepEqual(spec.Addresses, []string{"127.0.0.1", "::1"}) {
		t.Fatalf("parsed port-forward = %#v", spec)
	}

	for _, args := range [][]string{
		{"port-forward", "svc/api", "8100:80", "--address=0.0.0.0"},
		{"port-forward", "svc/api", "8100:80", "--token", "secret"},
		{"port-forward", "svc/api", "8100:80", "8101:81"},
	} {
		if _, err := parseKubectlPortForward(executable, args, workingDir); err == nil {
			t.Errorf("parseKubectlPortForward(%v) succeeded, want safety rejection", args)
		}
	}
}

func TestProjectHealthCheckUsesLoadedBackendPortAndOnlyStatus(t *testing.T) {
	for _, test := range []struct {
		name       string
		statusCode int
		wantStatus string
	}{
		{name: "success", statusCode: http.StatusNoContent, wantStatus: jobStatusHealthy},
		{name: "failure", statusCode: http.StatusServiceUnavailable, wantStatus: jobStatusUnhealthy},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/healthz" {
					t.Errorf("health path = %q, want /healthz", request.URL.Path)
				}
				response.WriteHeader(test.statusCode)
				_, _ = response.Write([]byte("private response body"))
			}))
			defer server.Close()
			host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
			if err != nil || host != "127.0.0.1" {
				t.Fatalf("fixture address = %q, %v", server.URL, err)
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
			scheduler := newJobScheduler(manager, filepath.Join(t.TempDir(), "jobs.json"), nil)
			status, _, output := scheduler.runProjectHealthCheck(context.Background(), "nala-labs")
			if status != test.wantStatus {
				t.Fatalf("health status = %q, want %q", status, test.wantStatus)
			}
			if strings.Contains(strings.Join(output, "\n"), "private response body") {
				t.Fatalf("health check exposed the response body: %v", output)
			}
		})
	}
}

func TestBoundJobOutputLimitsLinesAndBytes(t *testing.T) {
	lines := make([]string, jobOutputLineLimit+20)
	for i := range lines {
		lines[i] = strings.Repeat("x", jobOutputByteLimit/jobOutputLineLimit+20)
	}
	bounded := boundJobOutput(lines)
	if len(bounded) > jobOutputLineLimit {
		t.Fatalf("bounded output has %d lines, limit is %d", len(bounded), jobOutputLineLimit)
	}
	bytes := 0
	for _, line := range bounded {
		bytes += len(line)
	}
	if bytes > jobOutputByteLimit {
		t.Fatalf("bounded output has %d bytes, limit is %d", bytes, jobOutputByteLimit)
	}
}
