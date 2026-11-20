package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	gopsutilnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

const (
	openChamberProviderID           = "opencode"
	openChamberFreeModelID          = "deepseek-v4.1-flash-free"
	openChamberUsageProjectName     = "Nala Ctrl Usage Monitor"
	openChamberResponseByteLimit    = 16 * 1024
	openChamberSettingsByteLimit    = 256 * 1024
	openChamberActivityByteLimit    = 256 * 1024
	openChamberAPIDiscoveryTimeout  = 2 * time.Second
	openChamberProbeTimeout         = 90 * time.Second
	openChamberFreeLimitRetryDelay  = 30 * time.Second
	openChamberFreeModelIdleTimeout = 3 * time.Minute
	openChamberActivePollInterval   = 5 * time.Second
)

var errOpenChamberFreeModelBusy = errors.New("OpenChamber free model remained active")

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

type openChamberModelRef struct {
	ProviderID string `json:"providerID"`
	ID         string `json:"id"`
}

type openChamberPermissionRule struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

type openChamberPromptRequest struct {
	Text string `json:"text"`
}

type openChamberSessionMessagesResponse struct {
	Data []openChamberSessionMessage `json:"data"`
}

type openChamberSessionMessageMetadataResponse struct {
	Data []openChamberSessionMessageMetadata `json:"data"`
}

type openChamberSessionMessageMetadata struct {
	Type   string                 `json:"type"`
	Finish string                 `json:"finish"`
	Error  json.RawMessage        `json:"error"`
	Model  openChamberModelRef    `json:"model"`
	Time   openChamberMessageTime `json:"time"`
}

type openChamberMessageTime struct {
	Created    int64 `json:"created"`
	FirstToken int64 `json:"firstToken"`
	Completed  int64 `json:"completed"`
}

type openChamberActiveSessionsResponse struct {
	Data map[string]openChamberActiveSessionStatus `json:"data"`
}

type openChamberActiveSessionStatus struct {
	Type string `json:"type"`
}

type openChamberSessionModelResponse struct {
	Data struct {
		Model openChamberModelRef `json:"model"`
	} `json:"data"`
}

type openChamberSessionMessage struct {
	Type    string              `json:"type"`
	Finish  string              `json:"finish"`
	Error   json.RawMessage     `json:"error"`
	Model   openChamberModelRef `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type openChamberCreateSessionRequest struct {
	Title       string                      `json:"title"`
	Model       openChamberModelRef         `json:"model"`
	Location    openChamberSessionLocation  `json:"location"`
	Metadata    openChamberSessionMetadata  `json:"metadata"`
	Permissions []openChamberPermissionRule `json:"permissions"`
}

type openChamberSessionMetadata struct {
	OpenChamber openChamberSessionMetadataDetails `json:"openchamber"`
}

type openChamberSessionMetadataDetails struct {
	Kind string `json:"kind"`
}

type openChamberSessionLocation struct {
	Directory string `json:"directory"`
}

type openChamberCreateSessionResponse struct {
	Data struct {
		ID          string                      `json:"id"`
		Location    openChamberSessionLocation  `json:"location"`
		Metadata    openChamberSessionMetadata  `json:"metadata"`
		Permissions []openChamberPermissionRule `json:"permissions"`
	} `json:"data"`
}

type openChamberSettingsResponse struct {
	Projects *[]openChamberProject `json:"projects"`
}

type openChamberProject struct {
	Path string `json:"path"`
}

type openChamberCreateProjectRequest struct {
	Path   string `json:"path"`
	Create bool   `json:"create"`
}

type openChamberCreateProjectResponse struct {
	Success bool   `json:"success"`
	Path    string `json:"path"`
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

	projectDirectory, err := ensureOpenChamberUsageProject(ctx, client, target)
	if err != nil {
		return jobStatusFailed, "The dedicated OpenChamber project could not be prepared, so free usage could not be checked.", []string{"The Nala Ctrl Usage Monitor project was not available in OpenChamber."}
	}
	if !openChamberListenerStillOwned(target) {
		return jobStatusFailed, "OpenChamber's local API changed before the free usage check could run.", []string{"The OpenChamber listener was no longer available after project setup."}
	}

	sessionID, err := createOpenChamberUsageSession(ctx, client, target, projectDirectory)
	if err != nil {
		return jobStatusFailed, "OpenChamber session creation was not confirmed, so free usage could not be checked.", []string{"The OpenChamber session API did not return a usable session ID."}
	}
	if !openChamberListenerStillOwned(target) {
		return jobStatusFailed, "OpenChamber's local API changed before the free usage check could run.", []string{"A new OpenChamber session was created, but the verified listener was no longer available for its probe."}
	}

	probeStartedAt := time.Now()
	if err := sendOpenChamberUsagePrompt(ctx, client, target, sessionID); err != nil {
		return jobStatusFailed, "OpenCode Zen's free usage state could not be confirmed.", []string{"The session prompt was not accepted by OpenChamber."}
	}
	result, err := inspectOpenChamberUsageSession(ctx, client, target, sessionID)
	if err != nil {
		return jobStatusFailed, "OpenCode Zen's free usage state could not be confirmed.", []string{"The OpenChamber session did not finish or its transcript could not be read."}
	}
	if result == "limited" {
		availableInAnotherSession, err := waitForOpenChamberFreeModelIdle(ctx, client, target, sessionID, probeStartedAt)
		if err != nil {
			if errors.Is(err, errOpenChamberFreeModelBusy) {
				return jobStatusFailed, "OpenCode Zen's free usage state is unknown because another session kept the free model busy.", []string{"No delayed retry was sent because another session using the same free model remained active for three minutes."}
			}
			return jobStatusFailed, "OpenCode Zen's free usage state could not be confirmed.", []string{"The OpenChamber active-session state could not be checked before the delayed retry."}
		}
		if availableInAnotherSession {
			return jobStatusHealthy, "The OpenCode Zen free model responded in another OpenChamber session; free usage is available.", []string{"A different active session using the same provider and model received a completed response after this check began."}
		}
		if err := sendOpenChamberUsagePrompt(ctx, client, target, sessionID); err != nil {
			return jobStatusFailed, "OpenCode Zen's free usage state could not be confirmed.", []string{"The delayed retry prompt was not accepted by OpenChamber."}
		}
		result, err = inspectOpenChamberUsageSession(ctx, client, target, sessionID)
		if err != nil {
			return jobStatusFailed, "OpenCode Zen's free usage state could not be confirmed.", []string{"The delayed retry did not finish or its transcript could not be read."}
		}
	}

	switch result {
	case "available":
		return jobStatusHealthy, "The OpenCode Zen free model responded; free usage is available.", []string{"A session in the Nala Ctrl Usage Monitor project contains the successful free model probe."}
	case "limited":
		return jobStatusUnhealthy, "OpenCode Zen still reports the free usage limit after a delayed retry.", []string{"The initial probe and one delayed retry in the Nala Ctrl Usage Monitor project both returned the free model limit response."}
	default:
		return jobStatusFailed, "OpenCode Zen's free usage state could not be confirmed.", []string{"The session did not contain a completed text response or a recognized free usage limit response."}
	}
}

func inspectOpenChamberUsageSession(ctx context.Context, client *http.Client, target openChamberListener, sessionID string) (string, error) {
	if err := waitForOpenChamberUsageSession(ctx, client, target, sessionID); err != nil {
		return "failed", err
	}
	messages, err := readOpenChamberUsageMessages(ctx, client, target, sessionID)
	if err != nil {
		return "failed", err
	}
	return inspectOpenChamberUsageMessages(messages), nil
}

func waitForOpenChamberFreeModelIdle(ctx context.Context, client *http.Client, target openChamberListener, probeSessionID string, after time.Time) (bool, error) {
	delayTimer := time.NewTimer(openChamberFreeLimitRetryDelay)
	defer delayTimer.Stop()
	ticker := time.NewTicker(openChamberActivePollInterval)
	defer ticker.Stop()
	var idleTimer *time.Timer
	var idleTimerChannel <-chan time.Time
	delayElapsed := false
	idleTimeoutElapsed := false
	observedSessions := make(map[string]struct{})

	for {
		activeSessions, err := openChamberFreeModelSessions(ctx, client, target)
		if err != nil {
			return false, err
		}
		for _, sessionID := range activeSessions {
			observedSessions[sessionID] = struct{}{}
		}
		observedIDs := make([]string, 0, len(observedSessions))
		for sessionID := range observedSessions {
			observedIDs = append(observedIDs, sessionID)
		}
		sort.Strings(observedIDs)
		for _, sessionID := range observedIDs {
			if sessionID == probeSessionID {
				continue
			}
			responded, err := openChamberFreeModelSessionResponded(ctx, client, target, sessionID, after)
			if err != nil {
				return false, err
			}
			if responded {
				return true, nil
			}
		}
		if len(activeSessions) == 0 {
			if delayElapsed {
				return false, nil
			}
		}
		if idleTimeoutElapsed {
			return false, errOpenChamberFreeModelBusy
		}

		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-delayTimer.C:
			delayElapsed = true
			idleTimer = time.NewTimer(openChamberFreeModelIdleTimeout)
			idleTimerChannel = idleTimer.C
			defer idleTimer.Stop()
		case <-idleTimerChannel:
			idleTimeoutElapsed = true
		case <-ticker.C:
		}
	}
}

func openChamberFreeModelSessions(ctx context.Context, client *http.Client, target openChamberListener) ([]string, error) {
	if !openChamberListenerStillOwned(target) {
		return nil, errors.New("OpenChamber listener changed before checking active sessions")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target.address+"/api/session/active", nil)
	if err != nil {
		return nil, errors.New("could not create the OpenChamber active-session request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("OpenChamber active-session request failed")
	}
	body, err := readOpenChamberResponse(response)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		return nil, errors.New("OpenChamber active-session response was unavailable")
	}
	var active openChamberActiveSessionsResponse
	if json.Unmarshal(body, &active) != nil || active.Data == nil {
		return nil, errors.New("OpenChamber active-session response was unreadable")
	}

	matching := make([]string, 0, len(active.Data))
	for sessionID, status := range active.Data {
		if strings.TrimSpace(status.Type) == "" || strings.EqualFold(status.Type, "idle") {
			continue
		}
		model, err := readOpenChamberSessionModel(ctx, client, target, sessionID)
		if err != nil {
			return nil, err
		}
		if model.ProviderID == openChamberProviderID && model.ID == openChamberFreeModelID {
			matching = append(matching, sessionID)
		}
	}
	sort.Strings(matching)
	return matching, nil
}

func openChamberFreeModelSessionResponded(ctx context.Context, client *http.Client, target openChamberListener, sessionID string, after time.Time) (bool, error) {
	if !openChamberListenerStillOwned(target) {
		return false, errors.New("OpenChamber listener changed before reading active session messages")
	}
	endpoint := "http://" + target.address + "/api/session/" + url.PathEscape(sessionID) + "/message"
	query := url.Values{"type": {"assistant"}, "order": {"desc"}, "limit": {"10"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return false, errors.New("could not create the active-session message request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return false, errors.New("active-session message request failed")
	}
	body, err := readOpenChamberResponseLimit(response, openChamberActivityByteLimit)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		return false, errors.New("active-session messages were unavailable")
	}
	var messages openChamberSessionMessageMetadataResponse
	if json.Unmarshal(body, &messages) != nil {
		return false, errors.New("active-session messages were unreadable")
	}
	for _, message := range messages.Data {
		if openChamberSuccessfulMessageSince(message, after) {
			return true, nil
		}
	}
	return false, nil
}

func openChamberSuccessfulMessageSince(message openChamberSessionMessageMetadata, after time.Time) bool {
	return strings.EqualFold(message.Type, "assistant") &&
		message.Model.ProviderID == openChamberProviderID &&
		message.Model.ID == openChamberFreeModelID &&
		!hasOpenChamberMessageError(message.Error) &&
		!strings.EqualFold(message.Finish, "error") &&
		message.Time.FirstToken >= after.UnixMilli() &&
		message.Time.Completed > 0
}

func readOpenChamberSessionModel(ctx context.Context, client *http.Client, target openChamberListener, sessionID string) (openChamberModelRef, error) {
	if !openChamberListenerStillOwned(target) {
		return openChamberModelRef{}, errors.New("OpenChamber listener changed before reading an active session")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target.address+"/api/session/"+url.PathEscape(sessionID), nil)
	if err != nil {
		return openChamberModelRef{}, errors.New("could not create the active-session detail request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return openChamberModelRef{}, errors.New("active-session detail request failed")
	}
	body, err := readOpenChamberResponse(response)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		return openChamberModelRef{}, errors.New("active-session details were unavailable")
	}
	var session openChamberSessionModelResponse
	if json.Unmarshal(body, &session) != nil || strings.TrimSpace(session.Data.Model.ProviderID) == "" || strings.TrimSpace(session.Data.Model.ID) == "" {
		return openChamberModelRef{}, errors.New("active-session model details were unreadable")
	}
	return session.Data.Model, nil
}

func sendOpenChamberUsagePrompt(ctx context.Context, client *http.Client, target openChamberListener, sessionID string) error {
	if !openChamberListenerStillOwned(target) {
		return errors.New("OpenChamber listener changed before the session prompt")
	}
	payload, err := json.Marshal(openChamberPromptRequest{Text: "Reply only with OK. Do not use tools."})
	if err != nil {
		return errors.New("could not create the OpenChamber session prompt")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+target.address+"/api/session/"+url.PathEscape(sessionID)+"/prompt", bytes.NewReader(payload))
	if err != nil {
		return errors.New("could not create the OpenChamber session prompt")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return errors.New("OpenChamber session prompt request failed")
	}
	defer response.Body.Close()
	if _, err := readOpenChamberResponse(response); err != nil || response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New("OpenChamber did not accept the session prompt")
	}
	return nil
}

func waitForOpenChamberUsageSession(ctx context.Context, client *http.Client, target openChamberListener, sessionID string) error {
	if !openChamberListenerStillOwned(target) {
		return errors.New("OpenChamber listener changed before waiting for the session")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+target.address+"/api/experimental/session/"+url.PathEscape(sessionID)+"/wait", nil)
	if err != nil {
		return errors.New("could not create the OpenChamber session wait request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return errors.New("OpenChamber session wait request failed")
	}
	defer response.Body.Close()
	if _, err := readOpenChamberResponse(response); err != nil || response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New("OpenChamber session did not reach idle state")
	}
	return nil
}

func readOpenChamberUsageMessages(ctx context.Context, client *http.Client, target openChamberListener, sessionID string) ([]openChamberSessionMessage, error) {
	if !openChamberListenerStillOwned(target) {
		return nil, errors.New("OpenChamber listener changed before reading the session")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target.address+"/api/session/"+url.PathEscape(sessionID)+"/message", nil)
	if err != nil {
		return nil, errors.New("could not create the OpenChamber session history request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("OpenChamber session history request failed")
	}
	defer response.Body.Close()
	body, err := readOpenChamberResponse(response)
	if err != nil || response.StatusCode != http.StatusOK {
		return nil, errors.New("OpenChamber session history was unavailable")
	}
	var result openChamberSessionMessagesResponse
	if json.Unmarshal(body, &result) != nil {
		return nil, errors.New("OpenChamber session history was unreadable")
	}
	return result.Data, nil
}

func inspectOpenChamberUsageMessages(messages []openChamberSessionMessage) string {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if !strings.EqualFold(message.Type, "assistant") {
			continue
		}
		if message.Model.ProviderID != openChamberProviderID || message.Model.ID != openChamberFreeModelID {
			return "failed"
		}
		if openChamberFreeUsageLimitError(message.Error) {
			return "limited"
		}
		if hasOpenChamberMessageError(message.Error) || strings.EqualFold(message.Finish, "error") {
			return "failed"
		}
		textFound := false
		for _, part := range message.Content {
			if isOpenChamberToolPart(part.Type) {
				return "failed"
			}
			if strings.EqualFold(part.Type, "text") && strings.TrimSpace(part.Text) != "" {
				textFound = true
			}
		}
		if textFound {
			return "available"
		}
		return "failed"
	}
	return "failed"
}

func isOpenChamberToolPart(partType string) bool {
	return strings.EqualFold(partType, "tool") ||
		strings.EqualFold(partType, "tool-call") ||
		strings.EqualFold(partType, "tool-result")
}

func openChamberFreeUsageLimitError(value json.RawMessage) bool {
	if len(value) == 0 || string(value) == "null" {
		return false
	}
	message := strings.ToLower(string(value))
	return strings.Contains(message, "provider.free-limit") ||
		strings.Contains(message, "freeusagelimiterror") ||
		strings.Contains(message, "free usage limit reached")
}

func hasOpenChamberMessageError(value json.RawMessage) bool {
	return len(value) > 0 && string(value) != "null"
}

func ensureOpenChamberUsageProject(ctx context.Context, client *http.Client, target openChamberListener) (string, error) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDirectory) == "" {
		return "", errors.New("the user home directory is unavailable")
	}
	projectDirectory := filepath.Join(homeDirectory, "Documents", "Projects", openChamberUsageProjectName)
	if !filepath.IsAbs(projectDirectory) {
		return "", errors.New("the OpenChamber project directory is not absolute")
	}
	if !openChamberListenerStillOwned(target) {
		return "", errors.New("OpenChamber listener changed before project lookup")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target.address+"/api/config/settings", nil)
	if err != nil {
		return "", errors.New("could not create the OpenChamber project lookup")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("OpenChamber project lookup failed")
	}
	body, readErr := readOpenChamberResponseLimit(response, openChamberSettingsByteLimit)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK {
		return "", errors.New("OpenChamber project lookup did not succeed")
	}
	var settings openChamberSettingsResponse
	if json.Unmarshal(body, &settings) != nil || settings.Projects == nil {
		return "", errors.New("OpenChamber project settings were unreadable")
	}
	for _, project := range *settings.Projects {
		if strings.TrimSpace(project.Path) != "" && sameOpenChamberPath(project.Path, projectDirectory) {
			return filepath.Clean(project.Path), nil
		}
	}

	if !openChamberListenerStillOwned(target) {
		return "", errors.New("OpenChamber listener changed before project creation")
	}
	payload, err := json.Marshal(openChamberCreateProjectRequest{Path: projectDirectory, Create: true})
	if err != nil {
		return "", errors.New("could not create the OpenChamber project request")
	}
	request, err = http.NewRequestWithContext(ctx, http.MethodPost, "http://"+target.address+"/api/openchamber/directory", bytes.NewReader(payload))
	if err != nil {
		return "", errors.New("could not create the OpenChamber project request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err = client.Do(request)
	if err != nil {
		return "", errors.New("OpenChamber project creation request failed")
	}
	body, readErr = readOpenChamberResponse(response)
	response.Body.Close()
	if readErr != nil || response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", errors.New("OpenChamber project creation did not succeed")
	}
	var created openChamberCreateProjectResponse
	if json.Unmarshal(body, &created) != nil || !created.Success || strings.TrimSpace(created.Path) == "" || !sameOpenChamberPath(created.Path, projectDirectory) {
		return "", errors.New("OpenChamber project creation returned no matching project directory")
	}
	return filepath.Clean(created.Path), nil
}

func sameOpenChamberPath(first, second string) bool {
	first = filepath.Clean(strings.TrimSpace(first))
	second = filepath.Clean(strings.TrimSpace(second))
	if filepath.Separator == '\\' {
		return strings.EqualFold(first, second)
	}
	return first == second
}

func createOpenChamberUsageSession(ctx context.Context, client *http.Client, target openChamberListener, projectDirectory string) (string, error) {
	if !openChamberListenerStillOwned(target) {
		return "", errors.New("OpenChamber listener changed before session creation")
	}
	payload, err := json.Marshal(openChamberCreateSessionRequest{
		Title:       "OpenChamber free usage check " + time.Now().Format("2006-01-02 15:04:05.000"),
		Model:       openChamberModelRef{ProviderID: openChamberProviderID, ID: openChamberFreeModelID},
		Location:    openChamberSessionLocation{Directory: projectDirectory},
		Metadata:    openChamberSessionMetadata{OpenChamber: openChamberSessionMetadataDetails{Kind: "scheduled"}},
		Permissions: []openChamberPermissionRule{{Action: "*", Resource: "*", Effect: "deny"}},
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
	if json.Unmarshal(body, &result) != nil || strings.TrimSpace(result.Data.ID) == "" || strings.TrimSpace(result.Data.Location.Directory) == "" || !sameOpenChamberPath(result.Data.Location.Directory, projectDirectory) || result.Data.Metadata.OpenChamber.Kind != "scheduled" || !hasOpenChamberDenyAllPermission(result.Data.Permissions) {
		return "", errors.New("OpenChamber session creation did not return the requested project, scheduled classification, and tool restrictions")
	}
	return result.Data.ID, nil
}

func hasOpenChamberDenyAllPermission(rules []openChamberPermissionRule) bool {
	for _, rule := range rules {
		if rule.Action == "*" && rule.Resource == "*" && rule.Effect == "deny" {
			return true
		}
	}
	return false
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
	return readOpenChamberResponseLimit(response, openChamberResponseByteLimit)
}

func readOpenChamberResponseLimit(response *http.Response, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, errors.New("could not read the bounded response")
	}
	if int64(len(body)) > limit {
		return nil, errors.New("response exceeded the bounded size")
	}
	return body, nil
}
