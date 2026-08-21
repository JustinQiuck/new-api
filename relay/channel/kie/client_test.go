package kie

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientCreatesAndPollsTask(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case CreateTaskPath:
			var request CreateTaskRequest
			require.NoError(t, common.DecodeJson(r.Body, &request))
			assert.Equal(t, KieImageModel, request.Model)
			_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"taskId":"private-task"}}`))
		case TaskRecordPath:
			assert.Equal(t, "private-task", r.URL.Query().Get("taskId"))
			if polls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"code":200,"data":{"taskId":"private-task","state":"generating"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"taskId":"private-task","state":"success","resultJson":"{\"resultUrls\":[\"https://example.com/result.png\"]}"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &Client{
		BaseURL:      server.URL,
		APIKey:       "test-key",
		HTTPClient:   server.Client(),
		PollInterval: time.Millisecond,
		PollTimeout:  time.Second,
	}
	taskID, err := client.CreateTask(context.Background(), CreateTaskRequest{Model: KieImageModel})
	require.NoError(t, err)
	assert.Equal(t, "private-task", taskID)

	result, err := client.WaitForTask(context.Background(), taskID)
	require.NoError(t, err)
	assert.Equal(t, "success", result.Data.State)
	assert.Equal(t, int32(2), polls.Load())
}

func TestClientTaskFailureDoesNotExposeProviderPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"taskId":"private-task","state":"fail","failCode":"CONTENT_POLICY","failMsg":"secret prompt and URL"}}`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:      server.URL,
		APIKey:       "failure-key",
		HTTPClient:   server.Client(),
		PollInterval: time.Millisecond,
		PollTimeout:  time.Second,
	}
	_, err := client.WaitForTask(context.Background(), "private-task")
	require.Error(t, err)
	assert.Equal(t, "KIE task failed (CONTENT_POLICY)", err.Error())
	assert.NotContains(t, err.Error(), "secret prompt")
	assert.NotContains(t, err.Error(), "private-task")
}

func TestClientUploadsBase64WithoutStableFilename(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &request))
		assert.Equal(t, "new-api/kie", request["uploadPath"])
		assert.True(t, strings.HasPrefix(request["base64Data"].(string), "data:image/png;base64,"))
		_, hasFileName := request["fileName"]
		assert.False(t, hasFileName)
		_, _ = w.Write([]byte(`{"success":true,"code":200,"data":{"downloadUrl":"https://files.example/reference.png"}}`))
	}))
	defer server.Close()

	client := &Client{FileUploadURL: server.URL, APIKey: "upload-key", HTTPClient: server.Client()}
	fileURL, err := client.UploadBase64(context.Background(), "data:image/png;base64,AAAA")
	require.NoError(t, err)
	assert.Equal(t, "https://files.example/reference.png", fileURL)
}
