package kie

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	kierelay "github.com/QuantumNous/new-api/relay/channel/kie"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const (
	defaultDuration   = 8
	defaultResolution = "480p"
	maxPromptRunes    = 4096
	maxVideoImages    = 7
)

var videoModelList = []string{kierelay.PublicVideoModel}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	apiKey string
	client *kierelay.Client
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	if taskErr := relaycommon.ValidateMultipartDirect(c, info); taskErr != nil {
		return taskErr
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return localTaskError(err, "invalid_request")
	}
	if err := captureMultipartSettings(c, &req); err != nil {
		return localTaskError(err, "invalid_multipart_form")
	}
	if utf8.RuneCountInString(req.Prompt) > maxPromptRunes {
		return localTaskError(fmt.Errorf("prompt must not exceed %d characters", maxPromptRunes), "invalid_prompt")
	}
	if _, _, _, err := resolveVideoSettings(req); err != nil {
		return localTaskError(err, "invalid_video_settings")
	}
	c.Set("task_request", req)
	return nil
}

func localTaskError(err error, code string) *taskdto.TaskError {
	return &taskdto.TaskError{
		Code:       code,
		Message:    err.Error(),
		StatusCode: http.StatusBadRequest,
		LocalError: true,
		Error:      err,
	}
}

func captureMultipartSettings(c *gin.Context, req *relaycommon.TaskSubmitReq) error {
	if c == nil || c.Request == nil || !strings.Contains(c.GetHeader("Content-Type"), gin.MIMEMultipartPOSTForm) {
		return nil
	}
	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return err
	}
	defer form.RemoveAll()
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}
	for _, field := range []string{"resolution_name", "resolution", "aspect_ratio"} {
		if values := form.Value[field]; len(values) > 0 && strings.TrimSpace(values[0]) != "" {
			req.Metadata[field] = strings.TrimSpace(values[0])
		}
	}
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, _ *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	duration, resolution, _, err := resolveVideoSettings(req)
	if err != nil {
		return nil
	}
	return map[string]float64{
		"seconds":    float64(duration),
		"resolution": resolutionRatio(resolution),
	}
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return strings.TrimRight(info.ChannelBaseUrl, "/") + kierelay.CreateTaskPath, nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}
	duration, resolution, aspectRatio, err := resolveVideoSettings(req)
	if err != nil {
		return nil, err
	}

	referenceURLs, err := normalizeReferenceURLs(req.Images)
	if err != nil {
		return nil, err
	}
	if strings.Contains(c.GetHeader("Content-Type"), gin.MIMEMultipartPOSTForm) {
		client, err := a.clientFor(info)
		if err != nil {
			return nil, err
		}
		uploadedURLs, err := kierelay.UploadReferenceImages(c, client, maxVideoImages)
		if err != nil {
			return nil, err
		}
		referenceURLs = append(referenceURLs, uploadedURLs...)
	}
	if len(referenceURLs) > maxVideoImages {
		return nil, fmt.Errorf("KIE video supports at most %d reference images", maxVideoImages)
	}
	if resolution == "1080p" && len(referenceURLs) > 1 {
		return nil, errors.New("KIE 1080p video supports at most one reference image")
	}
	if len(referenceURLs) > 0 {
		info.Action = constant.TaskActionGenerate
	}
	if len(referenceURLs) == 1 {
		aspectRatio = ""
	}

	info.UpstreamModelName = kierelay.KieVideoModel
	payload, err := common.Marshal(kierelay.CreateTaskRequest{
		Model: kierelay.KieVideoModel,
		Input: kierelay.TaskInput{
			Prompt:      req.Prompt,
			ImageURLs:   referenceURLs,
			AspectRatio: aspectRatio,
			Resolution:  resolution,
			Duration:    &duration,
		},
	})
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(payload), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	resp, err := channel.DoTaskApiRequest(a, c, info, requestBody)
	if err != nil || resp == nil || resp.StatusCode == http.StatusOK {
		return resp, err
	}
	_ = resp.Body.Close()
	safeBody, marshalErr := common.Marshal(map[string]int{"code": resp.StatusCode})
	if marshalErr != nil {
		return nil, marshalErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(safeBody))
	return resp, nil
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()

	var providerResponse kierelay.CreateTaskResponse
	if err := common.Unmarshal(responseBody, &providerResponse); err != nil {
		return "", nil, service.TaskErrorWrapper(errors.New("invalid KIE task creation response"), "invalid_response", http.StatusBadGateway)
	}
	if providerResponse.Code != http.StatusOK || strings.TrimSpace(providerResponse.Data.TaskID) == "" {
		status := providerStatus(providerResponse.Code)
		return "", nil, service.TaskErrorWrapper(
			fmt.Errorf("KIE task creation failed (code %d)", providerResponse.Code),
			"kie_task_creation_failed",
			status,
		)
	}

	video := dto.NewOpenAIVideo()
	video.ID = info.PublicTaskID
	video.TaskID = info.PublicTaskID
	video.Model = info.OriginModelName
	video.CreatedAt = common.GetTimestamp()
	if req, getErr := relaycommon.GetTaskRequest(c); getErr == nil {
		duration, _, aspectRatio, _ := resolveVideoSettings(req)
		video.Seconds = strconv.Itoa(duration)
		video.Size = aspectRatio
	}
	c.JSON(http.StatusOK, video)
	taskData, err := common.Marshal(video)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}
	return providerResponse.Data.TaskID, taskData, nil
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, errors.New("invalid task_id")
	}
	query := url.Values{"taskId": []string{taskID}}
	if err := (&kierelay.Client{APIKey: key}).WaitForPollSlot(context.Background()); err != nil {
		return nil, err
	}
	requestURL := strings.TrimRight(baseURL, "/") + kierelay.TaskRecordPath + "?" + query.Encode()
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("create proxy HTTP client: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_ = resp.Body.Close()
		return nil, &kierelay.HTTPStatusError{StatusCode: resp.StatusCode}
	}
	return resp, nil
}

func (a *TaskAdaptor) ParseTaskResult(responseBody []byte) (*relaycommon.TaskInfo, error) {
	var response kierelay.TaskRecordResponse
	if err := common.Unmarshal(responseBody, &response); err != nil {
		return nil, errors.New("KIE task query returned invalid data")
	}

	result := &relaycommon.TaskInfo{Code: response.Code}
	switch response.Data.State {
	case "waiting":
		result.Status = model.TaskStatusSubmitted
	case "queuing":
		result.Status = model.TaskStatusQueued
	case "generating":
		result.Status = model.TaskStatusInProgress
	case "success":
		var taskResult kierelay.TaskResult
		if err := common.UnmarshalJsonStr(response.Data.ResultJSON, &taskResult); err != nil || len(taskResult.ResultURLs) == 0 {
			return nil, errors.New("KIE task returned no result URL")
		}
		result.Status = model.TaskStatusSuccess
		result.Url = taskResult.ResultURLs[0]
	case "fail":
		result.Status = model.TaskStatusFailure
		result.Reason = safeFailureReason(response.Data.FailCode)
	default:
		if response.Code == http.StatusTooManyRequests {
			result.Status = model.TaskStatusQueued
			break
		}
		return nil, fmt.Errorf("KIE task query returned unknown state %q (code %d)", response.Data.State, response.Code)
	}
	if response.Data.Progress > 0 && response.Data.Progress <= 100 {
		result.Progress = fmt.Sprintf("%d%%", response.Data.Progress)
	}
	return result, nil
}

// SanitizeTaskResponse is used by the generic poller before logging or persistence.
func (a *TaskAdaptor) SanitizeTaskResponse(responseBody []byte) []byte {
	var response kierelay.TaskRecordResponse
	if err := common.Unmarshal(responseBody, &response); err != nil {
		return []byte(`{"code":0,"data":{"state":"invalid"}}`)
	}
	cleaned, err := common.Marshal(sanitizedTaskResponse{
		Code: response.Code,
		Data: sanitizedTaskData{
			State:    response.Data.State,
			FailCode: response.Data.FailCode,
			Progress: response.Data.Progress,
		},
	})
	if err != nil {
		return []byte(`{"code":0,"data":{"state":"invalid"}}`)
	}
	return cleaned
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	video := dto.NewOpenAIVideo()
	video.ID = task.TaskID
	video.TaskID = task.TaskID
	video.Model = task.Properties.OriginModelName
	video.Status = task.Status.ToVideoStatus()
	video.SetProgressStr(task.Progress)
	video.CreatedAt = task.CreatedAt
	if task.FinishTime > 0 {
		video.CompletedAt = task.FinishTime
	}
	if billing := task.PrivateData.BillingContext; billing != nil {
		if seconds := billing.OtherRatios["seconds"]; seconds > 0 {
			video.Seconds = strconv.Itoa(int(seconds))
		}
	}
	if task.Status == model.TaskStatusSuccess && task.PrivateData.ResultURL != "" {
		video.SetMetadata("url", task.PrivateData.ResultURL)
	}
	if task.Status == model.TaskStatusFailure {
		message := task.FailReason
		if message == "" {
			message = "KIE task failed"
		}
		video.Error = &dto.OpenAIVideoError{Code: "kie_task_failed", Message: message}
	}
	return common.Marshal(video)
}

func (a *TaskAdaptor) GetModelList() []string { return videoModelList }

func (a *TaskAdaptor) GetChannelName() string { return kierelay.ChannelName }

func (a *TaskAdaptor) clientFor(info *relaycommon.RelayInfo) (*kierelay.Client, error) {
	if a.client != nil {
		return a.client, nil
	}
	httpClient, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil {
		return nil, fmt.Errorf("create KIE HTTP client: %w", err)
	}
	a.client = &kierelay.Client{
		BaseURL:       info.ChannelBaseUrl,
		FileUploadURL: kierelay.FileUploadURL,
		APIKey:        info.ApiKey,
		HTTPClient:    httpClient,
	}
	return a.client, nil
}

func resolveVideoSettings(req relaycommon.TaskSubmitReq) (int, string, string, error) {
	duration := req.Duration
	if duration == 0 && req.Seconds != "" {
		duration, _ = strconv.Atoi(req.Seconds)
	}
	if duration == 0 {
		duration = defaultDuration
	}
	if duration < 1 || duration > 15 {
		return 0, "", "", errors.New("KIE video duration must be between 1 and 15 seconds")
	}

	resolution := metadataString(req.Metadata, "resolution_name")
	if resolution == "" {
		resolution = metadataString(req.Metadata, "resolution")
	}
	resolution = strings.ToLower(strings.TrimSpace(resolution))
	if resolution == "" || resolution == "auto" {
		resolution = defaultResolution
	}
	if resolution != "480p" && resolution != "720p" && resolution != "1080p" {
		return 0, "", "", errors.New("KIE video resolution must be 480p, 720p, or 1080p")
	}

	aspectRatio := metadataString(req.Metadata, "aspect_ratio")
	if aspectRatio == "" {
		aspectRatio = req.Size
	}
	var err error
	aspectRatio, err = normalizeAspectRatio(aspectRatio)
	if err != nil {
		return 0, "", "", err
	}
	return duration, resolution, aspectRatio, nil
}

func normalizeAspectRatio(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto", nil
	case "1:1", "1024x1024", "960x960":
		return "1:1", nil
	case "16:9", "1280x720", "1920x1080", "1792x1024":
		return "16:9", nil
	case "9:16", "720x1280", "1080x1920", "1024x1792":
		return "9:16", nil
	case "3:2", "1536x1024":
		return "3:2", nil
	case "2:3", "1024x1536":
		return "2:3", nil
	default:
		return "", errors.New("KIE video aspect ratio must be auto, 1:1, 16:9, 9:16, 3:2, or 2:3")
	}
}

func normalizeReferenceURLs(values []string) ([]string, error) {
	urls := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, errors.New("KIE video reference images must use HTTP or HTTPS URLs")
		}
		urls = append(urls, value)
	}
	return urls, nil
}

func metadataString(metadata map[string]interface{}, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return value
}

func resolutionRatio(resolution string) float64 {
	switch resolution {
	case "720p":
		return 1.5
	case "1080p":
		return 2.25
	default:
		return 1
	}
}

func providerStatus(code int) int {
	switch code {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusNotFound,
		http.StatusUnprocessableEntity, http.StatusTooManyRequests:
		return code
	case 455:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func safeFailureReason(failCode string) string {
	failCode = strings.TrimSpace(failCode)
	if failCode == "" {
		return "KIE task failed"
	}
	return fmt.Sprintf("KIE task failed (%s)", failCode)
}
