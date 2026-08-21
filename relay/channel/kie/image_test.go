package kie

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var onePixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg==")

func TestConvertImageGenerationRequest(t *testing.T) {
	count := uint(1)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: PublicImageModel},
		RelayMode:   relayconstant.RelayModeImagesGenerations,
	}

	converted, err := (&Adaptor{}).ConvertImageRequest(nil, info, dto.ImageRequest{
		Model:          PublicImageModel,
		Prompt:         "cinematic city",
		N:              &count,
		Size:           "1:1",
		ResponseFormat: "b64_json",
	})
	require.NoError(t, err)
	request, ok := converted.(*ImageBatchRequest)
	require.True(t, ok)
	assert.Equal(t, KieImageModel, request.Task.Model)
	assert.Equal(t, "1:1", request.Task.Input.AspectRatio)
	assert.Equal(t, "b64_json", request.ResponseFormat)
	assert.Equal(t, KieImageModel, info.UpstreamModelName)
}

func TestConvertImageRequestRejectsUnsupportedCountAndAspectRatio(t *testing.T) {
	count := uint(2)
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}
	_, err := (&Adaptor{}).ConvertImageRequest(nil, info, dto.ImageRequest{Prompt: "test", N: &count})
	require.ErrorContains(t, err, "image count")

	count = 1
	_, err = (&Adaptor{}).ConvertImageRequest(nil, info, dto.ImageRequest{Prompt: "test", N: &count, Size: "16:9"})
	require.ErrorContains(t, err, "supports auto, 1:1, 3:2, or 2:3")
}

func TestConvertImageEditUploadsValidatedReference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"code":200,"data":{"downloadUrl":"https://files.example/reference.png"}}`))
	}))
	defer server.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", PublicImageModel))
	require.NoError(t, writer.WriteField("prompt", "edit image"))
	part, err := writer.CreateFormFile("image", "reference.png")
	require.NoError(t, err)
	_, err = part.Write(onePixelPNG)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayMode:   relayconstant.RelayModeImagesEdits,
	}
	adaptor := &Adaptor{client: &Client{FileUploadURL: server.URL, APIKey: "key", HTTPClient: server.Client()}}

	converted, err := adaptor.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "edit image"})
	require.NoError(t, err)
	request, ok := converted.(*ImageBatchRequest)
	require.True(t, ok)
	assert.Equal(t, KieImageEdit, request.Task.Model)
	assert.Equal(t, []string{"https://files.example/reference.png"}, request.Task.Input.InputURLs)
}

func TestExecuteImageRequestReturnsOpenAIBase64Response(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case CreateTaskPath:
			_, _ = w.Write([]byte(`{"code":200,"data":{"taskId":"private-task"}}`))
		case TaskRecordPath:
			resultURL := "http://" + r.Host + "/result.png"
			response := TaskRecordResponse{Code: 200}
			response.Data.TaskID = "private-task"
			response.Data.State = "success"
			resultJSON, err := common.Marshal(TaskResult{ResultURLs: []string{resultURL}})
			require.NoError(t, err)
			response.Data.ResultJSON = string(resultJSON)
			payload, err := common.Marshal(response)
			require.NoError(t, err)
			_, _ = w.Write(payload)
		case "/result.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(onePixelPNG)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	request := ImageBatchRequest{
		Task:           CreateTaskRequest{Model: KieImageModel, Input: TaskInput{Prompt: "test"}},
		Count:          1,
		ResponseFormat: "b64_json",
	}
	payload, err := common.Marshal(request)
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeKie,
			ChannelBaseUrl: server.URL,
			ApiKey:         "key",
		},
		RelayMode: relayconstant.RelayModeImagesGenerations,
	}
	adaptor := &Adaptor{client: &Client{
		BaseURL:      server.URL,
		APIKey:       "key",
		HTTPClient:   server.Client(),
		PollInterval: time.Millisecond,
		PollTimeout:  time.Second,
	}, downloadImage: func(resultURL string) (string, string, error) {
		assert.Equal(t, server.URL+"/result.png", resultURL)
		return "image/png", base64.StdEncoding.EncodeToString(onePixelPNG), nil
	}}

	resp, err := adaptor.executeImageRequest(c, info, bytes.NewReader(payload))
	require.NoError(t, err)
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var response OpenAIImageResponse
	require.NoError(t, common.Unmarshal(responseBody, &response))
	require.Len(t, response.Data, 1)
	assert.Empty(t, response.Data[0].Url)
	assert.Equal(t, base64.StdEncoding.EncodeToString(onePixelPNG), response.Data[0].B64Json)
}
