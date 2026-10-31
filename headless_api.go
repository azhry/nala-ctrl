package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const headlessAPIDevAutostartEnv = "NALA_CTRL_DEV_HEADLESS_API"

type HeadlessAPIStatus struct {
	Running bool   `json:"running"`
	Address string `json:"address"`
	Error   string `json:"error,omitempty"`
}

type headlessAPIController struct {
	mu         sync.Mutex
	address    string
	newManager func() *Manager
	manager    *Manager
	server     *http.Server
	lastError  string
}

func newHeadlessAPIController(address string, newManager func() *Manager) *headlessAPIController {
	return &headlessAPIController{address: address, newManager: newManager}
}

func (c *headlessAPIController) Start() (HeadlessAPIStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.server != nil {
		return c.statusLocked(), nil
	}

	listener, err := listenAgentAPI(c.address)
	if err != nil {
		c.lastError = err.Error()
		return c.statusLocked(), err
	}
	if c.manager == nil {
		c.manager = c.newManager()
		c.manager.Workspace()
	}

	server := newAgentAPIServer(c.manager, agentAPIModeHeadless)
	c.server = server
	c.lastError = ""
	go c.serve(server, listener)

	return c.statusLocked(), nil
}

func (c *headlessAPIController) Stop() (HeadlessAPIStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var shutdownErr error
	if c.server != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		shutdownErr = c.server.Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, c.server.Close())
		}
		c.server = nil
	}

	var stopErr error
	if c.manager != nil {
		stopErr = c.manager.StopAll()
		if stopErr == nil {
			c.manager = nil
		}
	}

	err := errors.Join(shutdownErr, stopErr)
	if err != nil {
		c.lastError = err.Error()
	} else {
		c.lastError = ""
	}
	return c.statusLocked(), err
}

func (c *headlessAPIController) Status() HeadlessAPIStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *headlessAPIController) statusLocked() HeadlessAPIStatus {
	return HeadlessAPIStatus{
		Running: c.server != nil,
		Address: c.address,
		Error:   c.lastError,
	}
}

func (c *headlessAPIController) serve(server *http.Server, listener net.Listener) {
	err := server.Serve(listener)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.server != server {
		return
	}
	c.server = nil
	c.lastError = fmt.Sprintf("headless API stopped: %v", err)
	if c.manager != nil {
		if stopErr := c.manager.StopAll(); stopErr != nil {
			c.lastError = errors.Join(errors.New(c.lastError), stopErr).Error()
		}
	}
}
