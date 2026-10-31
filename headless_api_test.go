package main

import (
	"io"
	"net"
	"net/http"
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
