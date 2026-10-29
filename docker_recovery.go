package main

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

const (
	dockerRecoveryTimeout        = 5 * time.Minute
	dockerRestartCommandTimeout  = 45 * time.Second
	dockerStartCommandTimeout    = 20 * time.Second
	dockerEngineProbeTimeout     = 10 * time.Second
	dockerEnginePollInterval     = 2 * time.Second
	dockerEngineReadinessTimeout = 90 * time.Second
)

// FixDocker repairs the known blocked Docker secrets socket when its exact
// startup error is present, then restarts Docker Desktop and waits for a
// local Desktop engine to accept Docker CLI requests.
func (p *ProjectManager) FixDocker() error {
	if !p.dockerFixMu.TryLock() {
		return errors.New("Docker Desktop recovery is already in progress.")
	}
	defer p.dockerFixMu.Unlock()

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return errors.New("Docker CLI was not found. Install Docker Desktop and make sure its CLI is available on PATH.")
	}

	parent := p.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, dockerRecoveryTimeout)
	defer cancel()

	if _, err := repairKnownDockerSecretsSocket(ctx, dockerPath); err != nil {
		return err
	}

	restartCtx, cancelRestart := context.WithTimeout(ctx, dockerRestartCommandTimeout)
	restartErr := exec.CommandContext(restartCtx, dockerPath, "desktop", "restart").Run()
	cancelRestart()
	if restartErr != nil {
		startCtx, cancelStart := context.WithTimeout(ctx, dockerStartCommandTimeout)
		startErr := exec.CommandContext(startCtx, dockerPath, "desktop", "start", "--detach").Run()
		cancelStart()
		if startErr != nil {
			return errors.New("Docker Desktop could not be restarted. Make sure Docker Desktop and its CLI are installed and try again.")
		}
	}

	readyCtx, cancelReady := context.WithTimeout(ctx, dockerEngineReadinessTimeout)
	defer cancelReady()
	if err := waitForDockerDesktopEngine(readyCtx, dockerPath); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("Docker Desktop did not become ready in time. Check Docker Desktop for details.")
		}
		return errors.New("Docker Desktop recovery was interrupted before the engine became ready.")
	}

	return nil
}

func waitForDockerDesktopEngine(ctx context.Context, dockerPath string) error {
	ticker := time.NewTicker(dockerEnginePollInterval)
	defer ticker.Stop()

	for {
		if dockerDesktopEngineReady(ctx, dockerPath) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func dockerDesktopEngineReady(ctx context.Context, dockerPath string) bool {
	for _, desktopContext := range []string{"desktop-linux", "desktop-windows"} {
		inspectCtx, cancelInspect := context.WithTimeout(ctx, dockerEngineProbeTimeout)
		endpoint, err := exec.CommandContext(
			inspectCtx,
			dockerPath,
			"context", "inspect",
			"--format", "{{.Endpoints.docker.Host}}",
			desktopContext,
		).Output()
		cancelInspect()
		if err != nil || !isLocalDockerEndpoint(strings.TrimSpace(string(endpoint))) {
			continue
		}

		probeCtx, cancel := context.WithTimeout(ctx, dockerEngineProbeTimeout)
		err = exec.CommandContext(
			probeCtx,
			dockerPath,
			"--context", desktopContext,
			"info",
			"--format", "{{.ServerVersion}}",
		).Run()
		cancel()
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
	}
	return false
}

func isLocalDockerEndpoint(endpoint string) bool {
	switch {
	case strings.HasPrefix(endpoint, "npipe://"), strings.HasPrefix(endpoint, "unix://"):
		return true
	case strings.HasPrefix(endpoint, "tcp://"):
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return false
		}
		host := parsed.Hostname()
		if strings.EqualFold(host, "localhost") {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	default:
		return false
	}
}
