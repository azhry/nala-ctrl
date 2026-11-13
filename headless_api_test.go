package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeadlessAPIControllerStartStop(t *testing.T) {
	address := unusedLoopbackAddress(t)
	controller := newHeadlessAPIController(address, func() *Manager {
		return newTestManager(t)
	})

	status := controller.Status()
	if status.Running || status.Address != address {
		t.Fatalf("initial status = %+v", status)
	}

	status, err := controller.Start()
	if err != nil || !status.Running || status.Error != "" {
		t.Fatalf("start status = %+v, %v", status, err)
	}
	if err := requireHeadlessHealth(address); err != nil {
		t.Fatal(err)
	}

	status, err = controller.Start()
	if err != nil || !status.Running {
		t.Fatalf("repeated start status = %+v, %v", status, err)
	}

	status, err = controller.Stop()
	if err != nil || status.Running || status.Error != "" {
		t.Fatalf("stop status = %+v, %v", status, err)
	}
	if err := requireHeadlessUnavailable(address); err != nil {
		t.Fatal(err)
	}

	status, err = controller.Stop()
	if err != nil || status.Running {
		t.Fatalf("repeated stop status = %+v, %v", status, err)
	}

	status, err = controller.Start()
	if err != nil || !status.Running {
		t.Fatalf("restart status = %+v, %v", status, err)
	}
	if err := requireHeadlessHealth(address); err != nil {
		t.Fatal(err)
	}
	status, err = controller.Stop()
	if err != nil || status.Running {
		t.Fatalf("final stop status = %+v, %v", status, err)
	}
}

func TestHeadlessAPIControllerDoesNotTakeOccupiedPort(t *testing.T) {
	address := unusedLoopbackAddress(t)
	owner, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()

	managerCreated := false
	controller := newHeadlessAPIController(address, func() *Manager {
		managerCreated = true
		return newTestManager(t)
	})
	status, err := controller.Start()
	if err == nil || status.Running || status.Error == "" {
		t.Fatalf("occupied-port start status = %+v, %v", status, err)
	}
	if managerCreated {
		t.Fatal("manager was created despite the API port being occupied")
	}
	otherListener, err := net.Listen("tcp", address)
	if err == nil {
		otherListener.Close()
		t.Fatal("headless API released an occupied port it did not own")
	}
}

func TestInAppHeadlessAPIUsesDesktopManagerAndStopsOnlyListener(t *testing.T) {
	folder := projectTestFolder(t, "headless-api-shared-manager")
	command := longRunningTestCommand()
	config := fmt.Sprintf("projects:\n  backend:\n    cwd: .\n    run: %q\n", command)
	if err := os.WriteFile(filepath.Join(folder, configFilename), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}

	manager := newTestManager(t)
	t.Cleanup(func() { _ = manager.StopAll() })
	workspace, err := manager.LoadFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	service := workspace.Projects[0].Services[0]
	if _, err := manager.RunProject(service.ID); err != nil {
		t.Fatal(err)
	}

	address := unusedLoopbackAddress(t)
	controller := newHeadlessAPIControllerForManager(address, manager, nil)
	t.Cleanup(func() { _, _ = controller.Stop() })
	status, err := controller.Start()
	if err != nil || !status.Running {
		t.Fatalf("start shared-manager API = %+v, %v", status, err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + address + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	healthBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(healthBody)) != `{"ok":true,"mode":"desktop"}` {
		t.Fatalf("in-app headless health = %d %s, want desktop mode", response.StatusCode, healthBody)
	}

	servicePath := "/v1/services/" + url.PathEscape(service.ID)
	response, err = client.Get("http://" + address + servicePath)
	if err != nil {
		t.Fatal(err)
	}
	var apiStatus agentServiceStatusDTO
	if err := json.NewDecoder(response.Body).Decode(&apiStatus); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !apiStatus.Running {
		t.Fatalf("in-app headless service status = %d %+v, want shared running state", response.StatusCode, apiStatus)
	}

	status, err = controller.Stop()
	if err != nil || status.Running {
		t.Fatalf("stop shared-manager API listener = %+v, %v", status, err)
	}
	details, err := manager.GetProjectDetails(service.ID)
	if err != nil || !details.Running {
		t.Fatalf("stopping the API listener changed desktop service ownership: details=%+v error=%v", details, err)
	}
}

func unusedLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func requireHeadlessHealth(address string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + address + "/v1/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != `{"ok":true,"mode":"headless"}` {
		return &unexpectedHeadlessHealthError{status: response.StatusCode, body: string(body)}
	}
	return nil
}

type unexpectedHeadlessHealthError struct {
	status int
	body   string
}

func (e *unexpectedHeadlessHealthError) Error() string {
	return "headless health response did not match: status=" + http.StatusText(e.status) + " body=" + e.body
}

func requireHeadlessUnavailable(address string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + address + "/v1/health")
	if err == nil {
		response.Body.Close()
		return &unexpectedHeadlessHealthError{status: response.StatusCode, body: "listener still responded"}
	}
	return nil
}
