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
