package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	gopsutilnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

const (
	openChamberProviderID          = "opencode"
	openChamberFreeModelID         = "deepseek-v4.1-flash-free"
	openChamberResponseByteLimit   = 16 * 1024
	openChamberAPIDiscoveryTimeout = 2 * time.Second
	openChamberProbeTimeout        = 90 * time.Second
)

type openChamberListener struct {
	pid        int32
	createTime int64
	address    string
}

type openChamberAPIInfo struct {
	Hostname string          `json:"hostname"`
	Version  string          `json:"version"`
	URLs     json.RawMessage `json:"urls"`
}

type openChamberGenerateRequest struct {
	Prompt string              `json:"prompt"`
	Model  openChamberModelRef `json:"model"`
}

type openChamberModelRef struct {
	ProviderID string `json:"providerID"`
	ID         string `json:"id"`
}

type openChamberGenerateResponse struct {
	Data struct {
		Text string `json:"text"`
	} `json:"data"`
}

type openChamberCreateSessionRequest struct {
	Title    string              `json:"title"`
	Model    openChamberModelRef `json:"model"`
	Location json.RawMessage     `json:"location"`
}

type openChamberCreateSessionResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

type openChamberSessionListResponse struct {
	Data []struct {
		Location json.RawMessage `json:"location"`
	} `json:"data"`
}

type openChamberProviderError struct {
	Tag        string `json:"_tag"`
	ProviderID string `json:"providerID"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
}

func runOpenChamberFreeUsageCheck(ctx context.Context) (string, string, []string) {
	target, err := findOpenChamberAPITarget(ctx)
	if err != nil {
		return jobStatusFailed, "OpenChamber's local API is unavailable, so free usage could not be checked.", []string{"The OpenChamber desktop API was not available on a verified loopback listener."}
	}
	if !openChamberListenerStillOwned(target) {
		return jobStatusFailed, "OpenChamber's local API changed before the free usage check could run.", []string{"The verified OpenChamber listener was no longer available."}
	}

	client := openChamberHTTPClient(openChamberProbeTimeout)
	defer client.CloseIdleConnections()

	_, err = createOpenChamberUsageSession(ctx, client, target)
	if err != nil {
		return jobStatusFailed, "OpenChamber session creation was not confirmed, so free usage could not be checked.", []string{"The OpenChamber session API did not return a usable session ID."}
	}
	if !openChamberListenerStillOwned(target) {
		return jobStatusFailed, "OpenChamber's local API changed before the free usage check could run.", []string{"A new OpenChamber session was created, but the verified listener was no longer available for its probe."}
	}

	payload, err := json.Marshal(openChamberGenerateRequest{
		Prompt: "Reply only with OK.",
		Model:  openChamberModelRef{ProviderID: openChamberProviderID, ID: openChamberFreeModelID},
	})
	if err != nil {
		return jobStatusFailed, "The OpenCode Zen free usage check could not be prepared.", []string{"Could not create the stateless model request."}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+target.address+"/api/experimental/generate/text", bytes.NewReader(payload))
	if err != nil {
		return jobStatusFailed, "The OpenCode Zen free usage check could not be prepared.", []string{"Could not create the stateless model request."}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return jobStatusFailed, "OpenCode Zen's free usage state could not be confirmed.", []string{"The stateless model request could not connect or timed out."}
	}
	defer response.Body.Close()
	body, err := readOpenChamberResponse(response)
	if err != nil {
		return jobStatusFailed, "OpenCode Zen's free usage state could not be confirmed.", []string{"The model response was unavailable or exceeded the response limit."}
	}

	if response.StatusCode == http.StatusOK {
		var result openChamberGenerateResponse
		if json.Unmarshal(body, &result) != nil || strings.TrimSpace(result.Data.Text) == "" {
			return jobStatusFailed, "OpenCode Zen returned an unreadable model response.", []string{"The stateless model response did not contain text."}
		}
		return jobStatusHealthy, "The OpenCode Zen free model responded; free usage is available.", []string{"A new OpenChamber session was created and the one-request free model probe succeeded."}
	}

	var providerError openChamberProviderError
	if json.Unmarshal(body, &providerError) == nil &&
		providerError.Tag == "ProviderRequestError" &&
		providerError.ProviderID == openChamberProviderID {
		message := strings.ToLower(providerError.Message)
		if strings.Contains(message, "free usage limit reached") {
			return jobStatusUnhealthy, "OpenCode Zen reports that the free usage limit has not reset.", []string{"A new OpenChamber session was created; the free model probe returned its free usage limit message."}
		}
		if providerError.Reason == "rate_limit" {
			return jobStatusFailed, "The free model was rate limited; its usage reset could not be confirmed.", []string{"A new OpenChamber session was created; the provider rate limit did not identify the free usage reset state."}
		}
	}
	return jobStatusFailed, "OpenCode Zen's free usage state could not be confirmed.", []string{"A new OpenChamber session was created; the model probe failed for a reason other than its known free usage limit response."}
}

func createOpenChamberUsageSession(ctx context.Context, client *http.Client, target openChamberListener) (string, error) {
	if !openChamberListenerStillOwned(target) {
		return "", errors.New("OpenChamber listener changed before session creation")
	}
	location, err := openChamberLatestSessionLocation(ctx, client, target)
	if err != nil {
		return "", errors.New("OpenChamber did not provide a recent session location")
	}
	if !openChamberListenerStillOwned(target) {
		return "", errors.New("OpenChamber listener changed before session creation")
	}
	payload, err := json.Marshal(openChamberCreateSessionRequest{
		Title:    "OpenChamber free usage check " + time.Now().Format("2006-01-02 15:04:05.000"),
		Model:    openChamberModelRef{ProviderID: openChamberProviderID, ID: openChamberFreeModelID},
		Location: location,
	})
	if err != nil {
		return "", errors.New("could not create the OpenChamber session request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+target.address+"/api/session", bytes.NewReader(payload))
	if err != nil {
		return "", errors.New("could not create the OpenChamber session request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("OpenChamber session creation request failed")
	}
	defer response.Body.Close()
	body, err := readOpenChamberResponse(response)
	if err != nil || response.StatusCode != http.StatusOK {
		return "", errors.New("OpenChamber session creation did not succeed")
	}
	var result openChamberCreateSessionResponse
	if json.Unmarshal(body, &result) != nil || strings.TrimSpace(result.Data.ID) == "" {
		return "", errors.New("OpenChamber session creation returned no session ID")
	}
	return result.Data.ID, nil
}

func openChamberLatestSessionLocation(ctx context.Context, client *http.Client, target openChamberListener) (json.RawMessage, error) {
	if !openChamberListenerStillOwned(target) {
		return nil, errors.New("OpenChamber listener changed before session lookup")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target.address+"/api/session?limit=1&order=desc", nil)
	if err != nil {
		return nil, errors.New("could not create the OpenChamber session lookup")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("OpenChamber session lookup failed")
	}
	defer response.Body.Close()
	body, err := readOpenChamberResponse(response)
	if err != nil || response.StatusCode != http.StatusOK {
		return nil, errors.New("OpenChamber session lookup did not succeed")
	}
	var result openChamberSessionListResponse
	if json.Unmarshal(body, &result) != nil || len(result.Data) == 0 {
		return nil, errors.New("OpenChamber has no recent session")
	}
	location := result.Data[0].Location
	if len(location) == 0 || !json.Valid(location) || strings.TrimSpace(string(location)) == "null" {
		return nil, errors.New("OpenChamber's latest session has no usable location")
	}
	return location, nil
}

func findOpenChamberAPITarget(ctx context.Context) (openChamberListener, error) {
	connections, err := gopsutilnet.Connections("tcp")
	if err != nil {
		return openChamberListener{}, errors.New("could not inspect local TCP listeners")
	}

	listeners := make(map[string]openChamberListener)
	for _, connection := range connections {
		if connection.Status != "LISTEN" || connection.Pid <= 0 || connection.Laddr.Port == 0 {
			continue
		}
		ip := net.ParseIP(connection.Laddr.IP)
		if ip == nil || !ip.IsLoopback() {
			continue
		}
		createTime, ok := openChamberDesktopProcessCreateTime(connection.Pid)
		if !ok {
			continue
		}

		key := strconv.FormatInt(int64(connection.Pid), 10) + ":" + strconv.FormatUint(uint64(connection.Laddr.Port), 10)
		address := net.JoinHostPort(ip.String(), strconv.FormatUint(uint64(connection.Laddr.Port), 10))
		listener, exists := listeners[key]
		if !exists || ip.To4() != nil && strings.HasPrefix(listener.address, "[::1]:") {
			listeners[key] = openChamberListener{pid: connection.Pid, createTime: createTime, address: address}
		}
	}
	if len(listeners) == 0 {
		return openChamberListener{}, errors.New("no OpenChamber loopback listener was found")
	}

	keys := make([]string, 0, len(listeners))
	for key := range listeners {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	client := openChamberHTTPClient(openChamberAPIDiscoveryTimeout)
	defer client.CloseIdleConnections()
	var matches []openChamberListener
	for _, key := range keys {
		if ctx.Err() != nil {
			return openChamberListener{}, ctx.Err()
		}
		listener := listeners[key]
		if !openChamberListenerStillOwned(listener) {
			continue
		}
		if !isOpenChamberAPIServer(ctx, client, listener.address) {
			continue
		}
		if !openChamberListenerStillOwned(listener) {
			continue
		}
		matches = append(matches, listener)
	}
	if len(matches) != 1 {
		return openChamberListener{}, errors.New("OpenChamber loopback API is missing or ambiguous")
	}
	return matches[0], nil
}

func openChamberListenerStillOwned(target openChamberListener) bool {
	createTime, ok := openChamberDesktopProcessCreateTime(target.pid)
	if !ok || createTime != target.createTime {
		return false
	}
	targetIPText, targetPortText, err := net.SplitHostPort(target.address)
	if err != nil {
		return false
	}
	targetIP := net.ParseIP(targetIPText)
	targetPort, err := strconv.ParseUint(targetPortText, 10, 16)
	if targetIP == nil || !targetIP.IsLoopback() || err != nil {
		return false
	}
	connections, err := gopsutilnet.Connections("tcp")
	if err != nil {
		return false
	}
	found := false
	for _, connection := range connections {
		if connection.Status != "LISTEN" || uint64(connection.Laddr.Port) != targetPort {
			continue
		}
		ip := net.ParseIP(connection.Laddr.IP)
		if ip == nil || !ip.IsLoopback() || !ip.Equal(targetIP) {
			continue
		}
		if connection.Pid != target.pid {
			return false
		}
		found = true
	}
	if !found {
		return false
	}
	createTime, ok = openChamberDesktopProcessCreateTime(target.pid)
	return ok && createTime == target.createTime
}

func openChamberDesktopProcessCreateTime(pid int32) (int64, bool) {
	candidate, err := process.NewProcess(pid)
	if err != nil {
		return 0, false
	}
	name, err := candidate.Name()
	if err != nil || !isOpenChamberExecutable(name) {
		return 0, false
	}
	executable, err := candidate.Exe()
	if err != nil || !isOpenChamberExecutable(executable) {
		return 0, false
	}
	createTime, err := candidate.CreateTime()
	if err != nil || createTime <= 0 {
		return 0, false
	}
	return createTime, true
}

func isOpenChamberExecutable(value string) bool {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(value)))
	return name == "openchamber" || name == "openchamber.exe"
}

func isOpenChamberAPIServer(ctx context.Context, client *http.Client, address string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/info", nil)
	if err != nil {
		return false
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	body, err := readOpenChamberResponse(response)
	if err != nil {
		return false
	}
	var info openChamberAPIInfo
	if json.Unmarshal(body, &info) != nil || info.Hostname == "" || info.Version == "" {
		return false
	}
	return len(info.URLs) > 0 && json.Valid(info.URLs) && string(info.URLs) != "null"
}

func openChamberHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func readOpenChamberResponse(response *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(response.Body, openChamberResponseByteLimit+1))
	if err != nil {
		return nil, errors.New("could not read the bounded response")
	}
	if len(body) > openChamberResponseByteLimit {
		return nil, errors.New("response exceeded the bounded size")
	}
	return body, nil
}
