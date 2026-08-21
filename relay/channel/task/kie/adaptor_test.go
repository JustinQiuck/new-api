package kie

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	kierelay "github.com/QuantumNous/new-api/relay/channel/kie"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg==")

func TestBuildVideoRequestFromFreeCanvasMultipart(t *testing.T) {
	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer provider-key", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"success":true,"code":200,"data":{"downloadUrl":"https://files.example/reference.png"}}`))
	}))
	defer uploadServer.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", kierelay.PublicVideoModel))
	require.NoError(t, writer.WriteField("prompt", "camera moves through a city"))
	require.NoError(t, writer.WriteField("seconds", "6"))
	require.NoError(t, writer.WriteField("size", "1280x720"))
	require.NoError(t, writer.WriteField("resolution_name", "720p"))
	part, err := writer.CreateFormFile("input_reference[]", "reference.png")
	require.NoError(t, err)
	_, err = part.Write(testPNG)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	info := &relaycommon.RelayInfo{
		OriginModelName: kierelay.PublicVideoModel,
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeKie,
			ChannelBaseUrl: "https://api.kie.ai",
			ApiKey:         "provider-key",
		},
	}
	adaptor := &TaskAdaptor{client: &kierelay.Client{
		FileUploadURL: uploadServer.URL,
		APIKey:        "provider-key",
		HTTPClient:    uploadServer.Client(),
	}}
	adaptor.Init(info)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))

	requestBody, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(requestBody)
	require.NoError(t, err)
	var request kierelay.CreateTaskRequest
	require.NoError(t, common.Unmarshal(payload, &request))

	assert.Equal(t, kierelay.KieVideoModel, request.Model)
	assert.Equal(t, "camera moves through a city", request.Input.Prompt)
	assert.Equal(t, []string{"https://files.example/reference.png"}, request.Input.ImageURLs)
	assert.Empty(t, request.Input.AspectRatio, "KIE ignores aspect_ratio for a single reference image")
	assert.Equal(t, "720p", request.Input.Resolution)
	require.NotNil(t, request.Input.Duration)
	assert.Equal(t, 6, *request.Input.Duration)
	assert.Equal(t, constant.TaskActionGenerate, info.Action)
}

func TestValidateVideoSettingsAndBillingBounds(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Duration: 6,
		Size:     "1280x720",
		Metadata: map[string]interface{}{"resolution_name": "720p"},
	})
	ratios := (&TaskAdaptor{}).EstimateBilling(c, nil)
	assert.Equal(t, float64(6), ratios["seconds"])
	assert.Equal(t, 1.5, ratios["resolution"])

	_, _, _, err := resolveVideoSettings(relaycommon.TaskSubmitReq{Duration: 16})
	require.ErrorContains(t, err, "between 1 and 15")
	_, _, _, err = resolveVideoSettings(relaycommon.TaskSubmitReq{Metadata: map[string]interface{}{"resolution_name": "4k"}})
	require.ErrorContains(t, err, "480p, 720p, or 1080p")
	_, _, _, err = resolveVideoSettings(relaycommon.TaskSubmitReq{Size: "4:5"})
	require.ErrorContains(t, err, "aspect ratio")
}

func TestDoResponseReturnsOnlyPublicTaskID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("task_request", relaycommon.TaskSubmitReq{Duration: 8, Size: "16:9"})
	info := &relaycommon.RelayInfo{
		OriginModelName: kierelay.PublicVideoModel,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID: "task_public",
		},
	}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"code":200,"msg":"success","data":{"taskId":"private-kie-task"}}`))}

	upstreamID, taskData, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	require.Nil(t, taskErr)
	assert.Equal(t, "private-kie-task", upstreamID)
	assert.NotContains(t, string(taskData), "private-kie-task")
	assert.Contains(t, string(taskData), "task_public")
	assert.NotContains(t, recorder.Body.String(), "private-kie-task")
}

func TestParseAndSanitizeTaskRecord(t *testing.T) {
	raw := []byte(`{
		"code":200,
		"msg":"success",
		"data":{
			"taskId":"private-task",
			"model":"grok-imagine-video-1-5-preview",
			"state":"success",
			"param":"{\"input\":{\"prompt\":\"secret prompt\"}}",
			"resultJson":"{\"resultUrls\":[\"https://result.example/video.mp4\"]}",
			"failCode":"",
			"failMsg":"private failure detail",
			"progress":100,
			"creditsConsumed":42
		}
	}`)
	adaptor := &TaskAdaptor{}
	result, err := adaptor.ParseTaskResult(raw)
	require.NoError(t, err)
	assert.Equal(t, string(model.TaskStatusSuccess), result.Status)
	assert.Equal(t, "https://result.example/video.mp4", result.Url)

	cleaned := string(adaptor.SanitizeTaskResponse(raw))
	assert.Contains(t, cleaned, `"state":"success"`)
	assert.NotContains(t, cleaned, "private-task")
	assert.NotContains(t, cleaned, "secret prompt")
	assert.NotContains(t, cleaned, "result.example")
	assert.NotContains(t, cleaned, "private failure detail")
	assert.NotContains(t, cleaned, "creditsConsumed")

	failure := []byte(`{"code":200,"data":{"state":"fail","failCode":"GENERATION_FAILED","failMsg":"supplier-only detail","creditsConsumed":9}}`)
	failedResult, err := adaptor.ParseTaskResult(failure)
	require.NoError(t, err)
	assert.Equal(t, string(model.TaskStatusFailure), failedResult.Status)
	assert.Equal(t, "KIE task failed (GENERATION_FAILED)", failedResult.Reason)
	assert.NotContains(t, failedResult.Reason, "supplier-only detail")
}

func TestConvertToOpenAIVideoUsesPublicStateAndPrivateResultURL(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusSuccess,
		Progress:   "100%",
		CreatedAt:  100,
		FinishTime: 200,
		Properties: model.Properties{OriginModelName: kierelay.PublicVideoModel},
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "private-task",
			ResultURL:      "https://result.example/video.mp4",
			BillingContext: &model.TaskBillingContext{OtherRatios: map[string]float64{"seconds": 6}},
		},
	}
	payload, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var video dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(payload, &video))
	assert.Equal(t, "task_public", video.ID)
	assert.Equal(t, dto.VideoStatusCompleted, video.Status)
	assert.Equal(t, "6", video.Seconds)
	assert.Equal(t, "https://result.example/video.mp4", video.Metadata["url"])
	assert.NotContains(t, string(payload), "private-task")
}
