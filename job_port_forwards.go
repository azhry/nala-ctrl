package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

const (
	portForwardReadyTimeout = 8 * time.Second
	portForwardStopTimeout  = 5 * time.Second
)

type portForwardSpec struct {
	ID         string   `json:"id"`
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	WorkingDir string   `json:"workingDir"`
	LocalPort  int      `json:"localPort"`
	RemotePort int      `json:"remotePort"`
	Addresses  []string `json:"addresses"`
}

type livePortForward struct {
	Spec       portForwardSpec
	PID        int32
	CreateTime int64
}

type managedForward struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func parseKubectlPortForward(executable string, args []string, workingDir string) (portForwardSpec, error) {
	if executable == "" || workingDir == "" {
		return portForwardSpec{}, errors.New("executable or working directory is unknown")
	}
	base := strings.ToLower(filepath.Base(executable))
	if base != "kubectl" && base != "kubectl.exe" {
		return portForwardSpec{}, errors.New("process is not the kubectl executable")
	}
	resolvedExecutable, err := filepath.Abs(executable)
	if err != nil {
		return portForwardSpec{}, errors.New("kubectl executable path is invalid")
	}
	resolvedWorkingDir, err := filepath.Abs(workingDir)
	if err != nil {
		return portForwardSpec{}, errors.New("kubectl working directory is invalid")
	}
	commandIndex := -1
	for i, arg := range args {
		if arg == "port-forward" {
			if commandIndex != -1 {
				return portForwardSpec{}, errors.New("port-forward command is ambiguous")
			}
			commandIndex = i
		}
	}
	if commandIndex < 0 {
		return portForwardSpec{}, errors.New("port-forward command is missing")
	}

	var resource string
	var mapping string
	addresses := []string{"127.0.0.1", "::1"}
	forwardArgs := append([]string(nil), args...)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if i == commandIndex {
			continue
		}
		name, value, hasEquals := strings.Cut(arg, "=")
		if strings.HasPrefix(arg, "-") {
			switch name {
			case "--address":
				if !hasEquals {
					if i+1 >= len(args) {
						return portForwardSpec{}, errors.New("address flag value is missing")
					}
					i++
					value = args[i]
				}
				parsed, err := parseLoopbackAddresses(value)
				if err != nil {
					return portForwardSpec{}, err
				}
				addresses = parsed
			case "--namespace", "-n", "--context", "--kubeconfig", "--pod-running-timeout", "--request-timeout":
				if !hasEquals {
					if i+1 >= len(args) {
						return portForwardSpec{}, fmt.Errorf("%s flag value is missing", name)
					}
					i++
					value = args[i]
				}
				if strings.TrimSpace(value) == "" || strings.ContainsRune(value, '\x00') {
					return portForwardSpec{}, fmt.Errorf("%s flag value is invalid", name)
				}
				if name == "--pod-running-timeout" || name == "--request-timeout" {
					if _, err := time.ParseDuration(value); err != nil {
						return portForwardSpec{}, fmt.Errorf("%s flag value is invalid", name)
					}
				}
			default:
				return portForwardSpec{}, errors.New("kubectl command contains an unsupported flag")
			}
			continue
		}
		if i < commandIndex {
			return portForwardSpec{}, errors.New("kubectl global arguments are ambiguous")
		}
		if resource == "" {
			resource = arg
		} else if mapping == "" {
			mapping = arg
		} else {
			return portForwardSpec{}, errors.New("kubectl port-forward has an unsupported number of targets")
		}
	}
	if !safeForwardResource(resource) {
		return portForwardSpec{}, errors.New("kubectl port-forward target is invalid")
	}
	localPort, remotePort, err := parseForwardPortMapping(mapping)
	if err != nil {
		return portForwardSpec{}, err
	}
	if runtime.GOOS == "windows" {
		resolvedExecutable = filepath.Clean(resolvedExecutable)
	}
	identifier := sha256.Sum256([]byte(strings.Join([]string{
		resolvedExecutable, resolvedWorkingDir, strings.Join(forwardArgs, "\x00"),
	}, "\x00")))
	spec := portForwardSpec{
		ID: "forward-" + hex.EncodeToString(identifier[:]), Executable: resolvedExecutable,
		Args: forwardArgs, WorkingDir: resolvedWorkingDir, LocalPort: localPort,
		RemotePort: remotePort, Addresses: addresses,
	}
	return spec, nil
}

func parseLoopbackAddresses(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	if len(parts) == 0 {
		return nil, errors.New("kubectl port-forward must bind to a loopback address")
	}
	seen := make(map[string]bool, len(parts))
	addresses := make([]string, 0, len(parts))
	for _, part := range parts {
		address := strings.TrimSpace(part)
		ip := net.ParseIP(address)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("kubectl port-forward must bind only to loopback addresses")
		}
		canonical := ip.String()
		if !seen[canonical] {
			seen[canonical] = true
			addresses = append(addresses, canonical)
		}
	}
	return addresses, nil
}

func parseForwardPortMapping(value string) (int, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) == 1 {
		parts = []string{parts[0], parts[0]}
	}
	if len(parts) != 2 {
		return 0, 0, errors.New("kubectl port-forward must contain one local-to-remote port mapping")
	}
	localPort, localErr := strconv.Atoi(parts[0])
	remotePort, remoteErr := strconv.Atoi(parts[1])
	if localErr != nil || remoteErr != nil || localPort < 1 || localPort > 65535 || remotePort < 1 || remotePort > 65535 {
		return 0, 0, errors.New("kubectl port-forward mapping must use fixed ports from 1 to 65535")
	}
	return localPort, remotePort, nil
}

func safeForwardResource(resource string) bool {
	if resource == "" || strings.HasPrefix(resource, "-") || strings.ContainsAny(resource, " \t\r\n\x00") {
		return false
	}
	if strings.Count(resource, "/") > 1 || strings.Contains(resource, "..") {
		return false
	}
	for _, value := range resource {
		if (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || (value >= '0' && value <= '9') || strings.ContainsRune("._/-", value) {
			continue
		}
		return false
	}
	return true
}

func (s *JobScheduler) discoverKubectlPortForwards() ([]livePortForward, []string) {
	processes, err := process.Processes()
	if err != nil {
		return nil, []string{"The local port-forward process list could not be inspected."}
	}
	var forwards []livePortForward
	var issues []string
	for _, proc := range processes {
		args, err := proc.CmdlineSlice()
		if err != nil || len(args) == 0 {
			continue
		}
		commandIndex := -1
		for i, arg := range args {
			if arg == "port-forward" {
				commandIndex = i
				break
			}
		}
		if commandIndex < 0 {
			continue
		}
		executable, err := proc.Exe()
		if err != nil {
			issues = append(issues, "A port-forward command could not be identified safely.")
			continue
		}
		workingDir, err := proc.Cwd()
		if err != nil {
			issues = append(issues, "A port-forward working directory could not be verified.")
			continue
		}
		argv := args
		if len(argv) > 0 && strings.EqualFold(filepath.Base(argv[0]), filepath.Base(executable)) {
			argv = argv[1:]
		}
		spec, err := parseKubectlPortForward(executable, argv, workingDir)
		if err != nil {
			issues = append(issues, "A port-forward command could not be established uniquely and was left untouched.")
			continue
		}
		created, err := proc.CreateTime()
		if err != nil || created <= 0 {
			issues = append(issues, "A port-forward process start time could not be verified and it was left untouched.")
			continue
		}
		forwards = append(forwards, livePortForward{Spec: spec, PID: proc.Pid, CreateTime: created})
	}
	if len(issues) > 0 {
		return forwards, dedupeJobOutput(issues)
	}
	ports := make(map[int]string, len(forwards))
	ids := make(map[string]bool, len(forwards))
	for _, forward := range forwards {
		if previous, exists := ports[forward.Spec.LocalPort]; exists && previous != forward.Spec.ID {
			issues = append(issues, fmt.Sprintf("Local port %d has multiple port-forward identities; none were changed.", forward.Spec.LocalPort))
		}
		if ids[forward.Spec.ID] {
			issues = append(issues, "A port-forward command has multiple matching process identities; it was left untouched.")
		}
		ports[forward.Spec.LocalPort] = forward.Spec.ID
		ids[forward.Spec.ID] = true
	}
	return forwards, dedupeJobOutput(issues)
}

func dedupeJobOutput(lines []string) []string {
	seen := make(map[string]bool, len(lines))
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" && !seen[line] {
			seen[line] = true
			result = append(result, line)
		}
	}
	return result
}

func (s *JobScheduler) runMinikubePortForwardCheck(ctx context.Context) (string, string, []string) {
	live, issues := s.discoverKubectlPortForwards()
	if len(issues) > 0 {
		return jobStatusFailed, "At least one port-forward could not be checked safely; no ambiguous process was changed.", issues
	}
	s.mu.Lock()
	job := s.findJobLocked("minikube-port-forwards")
	if job == nil {
		s.mu.Unlock()
		return jobStatusFailed, "The Minikube port-forward job is unavailable.", []string{"The local job definition could not be loaded."}
	}
	byID := make(map[string]portForwardSpec, len(job.ForwardSpecs)+len(live))
	byPort := make(map[int]string, len(job.ForwardSpecs)+len(live))
	for _, spec := range job.ForwardSpecs {
		byID[spec.ID] = spec
		byPort[spec.LocalPort] = spec.ID
	}
	for _, forward := range live {
		if existingID, exists := byPort[forward.Spec.LocalPort]; exists && existingID != forward.Spec.ID {
			s.mu.Unlock()
			return jobStatusFailed, "A local port has conflicting saved and running forward mappings; no process was changed.", []string{fmt.Sprintf("Local port %d has a different saved mapping; no process was changed.", forward.Spec.LocalPort)}
		}
		byID[forward.Spec.ID] = forward.Spec
		byPort[forward.Spec.LocalPort] = forward.Spec.ID
	}
	job.ForwardSpecs = make([]portForwardSpec, 0, len(byID))
	for _, spec := range byID {
		job.ForwardSpecs = append(job.ForwardSpecs, spec)
	}
	sort.Slice(job.ForwardSpecs, func(i, j int) bool { return job.ForwardSpecs[i].LocalPort < job.ForwardSpecs[j].LocalPort })
	if err := writeJobsFile(s.path, s.state); err != nil {
		s.mu.Unlock()
		return jobStatusFailed, "The port-forward recovery settings could not be saved.", []string{"Could not save the locally discovered forward mappings."}
	}
	specs := append([]portForwardSpec(nil), job.ForwardSpecs...)
	s.mu.Unlock()
	if len(specs) == 0 {
		return jobStatusHealthy, "No eligible kubectl port-forwards are currently configured.", []string{"No kubectl port-forward processes were found."}
	}
	var output []string
	for _, spec := range specs {
		if ctx.Err() != nil {
			return jobStatusFailed, "The port-forward check stopped during app shutdown.", append(output, "Port-forward check was interrupted.")
		}
		var matches []livePortForward
		for _, forward := range live {
			if forward.Spec.ID == spec.ID {
				matches = append(matches, forward)
			}
		}
		if len(matches) > 1 {
			return jobStatusFailed, "A saved port-forward has multiple matching processes; no process was changed.", append(output, fmt.Sprintf("Local port %d has multiple matching processes.", spec.LocalPort))
		}
		owners, err := findPortOwners(spec.LocalPort)
		if err != nil {
			return jobStatusFailed, "The local port owner could not be inspected.", append(output, fmt.Sprintf("Local port %d could not be inspected.", spec.LocalPort))
		}
		if len(owners) > 1 {
			return jobStatusFailed, "A local port has multiple owners; no process was changed.", append(output, fmt.Sprintf("Local port %d has multiple owners.", spec.LocalPort))
		}
		var current *livePortForward
		if len(matches) == 1 {
			current = &matches[0]
		}
		if current != nil && len(owners) == 1 && owners[0].PID != current.PID {
			return jobStatusFailed, "The configured local port belongs to a different process; it was left untouched.", append(output, fmt.Sprintf("Local port %d belongs to a different process.", spec.LocalPort))
		}
		if current == nil && len(owners) > 0 {
			return jobStatusFailed, "The configured local port belongs to a different process; it was left untouched.", append(output, fmt.Sprintf("Local port %d belongs to a different process.", spec.LocalPort))
		}
		if current != nil && len(owners) == 1 && portForwardHealthy(spec) {
			output = append(output, healthyForwardOutput(spec))
			continue
		}
		if current != nil {
			if err := s.stopPortForward(*current); err != nil {
				return jobStatusFailed, "An unhealthy port-forward could not be stopped safely.", append(output, fmt.Sprintf("Local port %d was left untouched because its process identity changed or could not be verified.", spec.LocalPort))
			}
		}
		owners, err = findPortOwners(spec.LocalPort)
		if err != nil || len(owners) > 0 {
			return jobStatusFailed, "The local port could not be safely released for recovery.", append(output, fmt.Sprintf("Local port %d is still occupied; no process was started.", spec.LocalPort))
		}
		managed, err := s.startPortForward(ctx, spec)
		if err != nil {
			return jobStatusFailed, "The port-forward could not be restarted.", append(output, fmt.Sprintf("Could not restart the port-forward on local port %d.", spec.LocalPort))
		}
		if err := s.waitPortForwardHealthy(ctx, spec, managed); err != nil {
			_ = managed.cmd.Process.Kill()
			select {
			case <-managed.done:
			case <-time.After(portForwardStopTimeout):
			}
			return jobStatusFailed, "The restarted port-forward did not become healthy.", append(output, fmt.Sprintf("The port-forward on local port %d did not become healthy.", spec.LocalPort))
		}
		if spec.RemotePort == mongoWireProtocolPort {
			output = append(output, fmt.Sprintf("Restarted the port-forward on local port %d and it answered the MongoDB hello handshake.", spec.LocalPort))
		} else {
			output = append(output, fmt.Sprintf("Restarted the port-forward on local port %d using its saved mapping.", spec.LocalPort))
		}
	}
	return jobStatusHealthy, "All configured kubectl port-forwards are healthy.", output
}

func portForwardHealthy(spec portForwardSpec) bool {
	target := func(address string) string {
		return net.JoinHostPort(address, strconv.Itoa(spec.LocalPort))
	}
	if spec.RemotePort == mongoWireProtocolPort {
		for _, address := range spec.Addresses {
			if probeMongoHello(target(address), mongoHelloTimeout) == nil {
				return true
			}
		}
		return false
	}
	for _, address := range spec.Addresses {
		connection, err := net.DialTimeout("tcp", target(address), 500*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return true
		}
	}
	return false
}

func healthyForwardOutput(spec portForwardSpec) string {
	if spec.RemotePort == mongoWireProtocolPort {
		return fmt.Sprintf("127.0.0.1:%d to remote port %d is healthy and answered the MongoDB hello handshake.", spec.LocalPort, spec.RemotePort)
	}
	return fmt.Sprintf("127.0.0.1:%d to remote port %d is healthy.", spec.LocalPort, spec.RemotePort)
}

func (s *JobScheduler) startPortForward(ctx context.Context, spec portForwardSpec) (*managedForward, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !safeStoredPortForward(spec) {
		return nil, errors.New("saved port-forward does not pass safety checks")
	}
	cmd := exec.Command(spec.Executable, spec.Args...)
	cmd.Dir = spec.WorkingDir
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	managed := &managedForward{cmd: cmd, done: make(chan struct{})}
	s.forwardsMu.Lock()
	s.forwards[spec.ID] = managed
	s.forwardsMu.Unlock()
	go func() {
		_ = cmd.Wait()
		close(managed.done)
		s.forwardsMu.Lock()
		if s.forwards[spec.ID] == managed {
			delete(s.forwards, spec.ID)
		}
		s.forwardsMu.Unlock()
	}()
	return managed, nil
}

func safeStoredPortForward(spec portForwardSpec) bool {
	if spec.ID == "" || spec.LocalPort < 1 || spec.LocalPort > 65535 || spec.RemotePort < 1 || spec.RemotePort > 65535 || spec.Executable == "" || spec.WorkingDir == "" {
		return false
	}
	if !filepath.IsAbs(spec.Executable) || !filepath.IsAbs(spec.WorkingDir) || len(spec.Addresses) == 0 {
		return false
	}
	parsed, err := parseKubectlPortForward(spec.Executable, spec.Args, spec.WorkingDir)
	if err != nil || parsed.ID != spec.ID || parsed.LocalPort != spec.LocalPort || parsed.RemotePort != spec.RemotePort || !stringSlicesEqual(parsed.Addresses, spec.Addresses) {
		return false
	}
	for _, address := range spec.Addresses {
		ip := net.ParseIP(address)
		if ip == nil || !ip.IsLoopback() {
			return false
		}
	}
	return true
}

func (s *JobScheduler) waitPortForwardHealthy(ctx context.Context, spec portForwardSpec, managed *managedForward) error {
	deadline := time.NewTimer(portForwardReadyTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if portForwardHealthy(spec) {
			owners, err := findPortOwners(spec.LocalPort)
			if err == nil && len(owners) == 1 && int(owners[0].PID) == managed.cmd.Process.Pid {
				return nil
			}
			if err == nil && len(owners) > 0 && int(owners[0].PID) != managed.cmd.Process.Pid {
				return errors.New("another process owns the local port")
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-managed.done:
			return errors.New("kubectl exited before the forward became healthy")
		case <-deadline.C:
			return errors.New("port-forward readiness timed out")
		case <-ticker.C:
		}
	}
}

func (s *JobScheduler) stopPortForward(forward livePortForward) error {
	if !portForwardIdentityMatches(forward) {
		return errors.New("port-forward process identity changed")
	}
	s.forwardsMu.Lock()
	managed := s.forwards[forward.Spec.ID]
	s.forwardsMu.Unlock()
	if managed != nil {
		if int32(managed.cmd.Process.Pid) != forward.PID {
			return errors.New("managed port-forward process changed")
		}
		if err := managed.cmd.Process.Kill(); err != nil {
			return err
		}
		select {
		case <-managed.done:
		case <-time.After(portForwardStopTimeout):
			return errors.New("managed port-forward did not stop")
		}
		return nil
	}
	proc, err := process.NewProcess(forward.PID)
	if err != nil {
		return err
	}
	if !portForwardIdentityMatches(forward) {
		return errors.New("port-forward process identity changed")
	}
	if err := proc.Kill(); err != nil {
		return err
	}
	deadline := time.Now().Add(portForwardStopTimeout)
	for time.Now().Before(deadline) {
		if !portForwardIdentityMatches(forward) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("port-forward process did not stop")
}

func portForwardIdentityMatches(forward livePortForward) bool {
	if !processIdentityMatches(forward.PID, forward.CreateTime) {
		return false
	}
	proc, err := process.NewProcess(forward.PID)
	if err != nil {
		return false
	}
	executable, err := proc.Exe()
	if err != nil || !sameProcessPath(executable, forward.Spec.Executable) {
		return false
	}
	args, err := proc.CmdlineSlice()
	if err != nil || len(args) == 0 {
		return false
	}
	if strings.EqualFold(filepath.Base(args[0]), filepath.Base(executable)) {
		args = args[1:]
	}
	return stringSlicesEqual(args, forward.Spec.Args)
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *JobScheduler) stopManagedForwards() {
	s.forwardsMu.Lock()
	forwards := make(map[string]*managedForward, len(s.forwards))
	for id, managed := range s.forwards {
		forwards[id] = managed
	}
	s.forwardsMu.Unlock()
	for _, managed := range forwards {
		if managed.cmd.Process != nil {
			_ = managed.cmd.Process.Kill()
		}
	}
	for _, managed := range forwards {
		select {
		case <-managed.done:
		case <-time.After(portForwardStopTimeout):
		}
	}
}
