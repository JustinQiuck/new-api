package kie

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const maxProviderResponseBytes = 1 << 20

type Client struct {
	BaseURL       string
	FileUploadURL string
	APIKey        string
	HTTPClient    *http.Client
	PollInterval  time.Duration
	PollTimeout   time.Duration
}

type TaskFailedError struct {
	TaskID   string
	FailCode string
}

type HTTPStatusError struct {
	StatusCode int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("KIE request failed with HTTP %d", e.StatusCode)
}

func (e *TaskFailedError) Error() string {
	if e.FailCode == "" {
		return "KIE task failed"
	}
	return fmt.Sprintf("KIE task failed (%s)", e.FailCode)
}

type pollGate struct {
	mu   sync.Mutex
	next time.Time
}

var kiePollGates sync.Map

func (c *Client) CreateTask(ctx context.Context, request CreateTaskRequest) (string, error) {
	var response CreateTaskResponse
	if err := c.doJSON(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+CreateTaskPath, request, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Data.TaskID) == "" {
		return "", fmt.Errorf("KIE task creation returned no task ID (code %d)", response.Code)
	}
	return response.Data.TaskID, nil
}

func (c *Client) GetTask(ctx context.Context, taskID string) (*TaskRecordResponse, error) {
	query := url.Values{"taskId": []string{taskID}}
	requestURL := strings.TrimRight(c.BaseURL, "/") + TaskRecordPath + "?" + query.Encode()
	var response TaskRecordResponse
	if err := c.doJSON(ctx, http.MethodGet, requestURL, nil, &response); err != nil {
		return nil, err
	}
	if strings.TrimSpace(response.Data.State) == "" {
		return nil, fmt.Errorf("KIE task query returned no state (code %d)", response.Code)
	}
	return &response, nil
}

func (c *Client) WaitForTask(ctx context.Context, taskID string) (*TaskRecordResponse, error) {
	interval := c.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	timeout := c.PollTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		if err := c.waitForPollSlot(ctx); err != nil {
			return nil, fmt.Errorf("KIE task polling stopped: %w", err)
		}
		response, err := c.GetTask(ctx, taskID)
		if err != nil {
			var statusErr *HTTPStatusError
			if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusTooManyRequests {
				if err := waitForInterval(ctx, interval); err != nil {
					return nil, fmt.Errorf("KIE task polling stopped: %w", err)
				}
				continue
			}
			return nil, err
		}
		switch response.Data.State {
		case "success":
			return response, nil
		case "fail":
			return nil, &TaskFailedError{TaskID: taskID, FailCode: response.Data.FailCode}
		case "waiting", "queuing", "generating":
		default:
			return nil, fmt.Errorf("KIE returned unknown task state %q", response.Data.State)
		}

		if err := waitForInterval(ctx, interval); err != nil {
			return nil, fmt.Errorf("KIE task polling stopped: %w", err)
		}
	}
}

func (c *Client) UploadBase64(ctx context.Context, dataURL string) (string, error) {
	requestURL := c.FileUploadURL
	if requestURL == "" {
		requestURL = FileUploadURL
	}
	var response FileUploadResponse
	request := FileUploadRequest{Base64Data: dataURL, UploadPath: "new-api/kie"}
	if err := c.doJSON(ctx, http.MethodPost, requestURL, request, &response); err != nil {
		return "", err
	}
	fileURL := strings.TrimSpace(response.Data.DownloadURL)
	if fileURL == "" {
		fileURL = strings.TrimSpace(response.Data.FileURL)
	}
	if !response.Success || fileURL == "" {
		return "", fmt.Errorf("KIE file upload failed (code %d)", response.Code)
	}
	return fileURL, nil
}

func (c *Client) doJSON(ctx context.Context, method, requestURL string, body any, output any) error {
	var reader io.Reader
	if body != nil {
		payload, err := common.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.HTTPClient == nil {
		return errors.New("KIE HTTP client is not configured")
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponseBytes+1))
	if err != nil {
		return err
	}
	if len(responseBody) > maxProviderResponseBytes {
		return errors.New("KIE response exceeds maximum size")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &HTTPStatusError{StatusCode: resp.StatusCode}
	}
	if err := common.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("invalid KIE response: %w", err)
	}
	return nil
}

func waitForInterval(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) waitForPollSlot(ctx context.Context) error {
	digest := sha256.Sum256([]byte(c.APIKey))
	key := hex.EncodeToString(digest[:])
	value, _ := kiePollGates.LoadOrStore(key, &pollGate{})
	gate := value.(*pollGate)

	gate.mu.Lock()
	now := time.Now()
	wait := time.Duration(0)
	if gate.next.After(now) {
		wait = gate.next.Sub(now)
	}
	gate.next = now.Add(wait + 100*time.Millisecond)
	gate.mu.Unlock()

	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// WaitForPollSlot enforces KIE's per-key task-query rate limit.
func (c *Client) WaitForPollSlot(ctx context.Context) error {
	return c.waitForPollSlot(ctx)
}
