package kie

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func (a *Adaptor) convertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (*ImageBatchRequest, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	count := int(lo.FromPtrOr(request.N, uint(1)))
	if count < 1 || count > MaxImageCount {
		return nil, fmt.Errorf("KIE image count must be between 1 and %d", MaxImageCount)
	}
	aspectRatio, err := kieImageAspectRatio(request.Size)
	if err != nil {
		return nil, err
	}

	task := CreateTaskRequest{
		Model: KieImageModel,
		Input: TaskInput{
			Prompt:      request.Prompt,
			AspectRatio: aspectRatio,
		},
	}
	if info.RelayMode == relayconstant.RelayModeImagesEdits {
		client, err := a.clientFor(info)
		if err != nil {
			return nil, err
		}
		inputURLs, err := UploadReferenceImages(c, client, MaxReferenceImages)
		if err != nil {
			return nil, err
		}
		if len(inputURLs) == 0 {
			return nil, errors.New("image is required")
		}
		task.Model = KieImageEdit
		task.Input.InputURLs = inputURLs
	}
	info.UpstreamModelName = task.Model
	return &ImageBatchRequest{Task: task, Count: count, ResponseFormat: request.ResponseFormat}, nil
}

func (a *Adaptor) executeImageRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	payload, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, err
	}
	var request ImageBatchRequest
	if err := common.Unmarshal(payload, &request); err != nil {
		return nil, fmt.Errorf("invalid KIE image request: %w", err)
	}
	client, err := a.clientFor(info)
	if err != nil {
		return nil, err
	}

	imageData := make([]dto.ImageData, 0, request.Count)
	for i := 0; i < request.Count; i++ {
		if len(imageData) >= request.Count {
			break
		}
		taskID, err := client.CreateTask(c.Request.Context(), request.Task)
		if err != nil {
			return nil, err
		}
		result, err := client.WaitForTask(c.Request.Context(), taskID)
		if err != nil {
			var failed *TaskFailedError
			if errors.As(err, &failed) {
				logger.LogWarn(c, fmt.Sprintf("KIE image task failed: task_id=%s fail_code=%s", failed.TaskID, failed.FailCode))
			}
			return nil, err
		}
		var taskResult TaskResult
		if err := common.UnmarshalJsonStr(result.Data.ResultJSON, &taskResult); err != nil {
			return nil, errors.New("KIE image task returned invalid result data")
		}
		if len(taskResult.ResultURLs) == 0 {
			return nil, errors.New("KIE image task returned no result URL")
		}
		for _, resultURL := range taskResult.ResultURLs {
			data := dto.ImageData{Url: resultURL}
			if request.ResponseFormat == "b64_json" {
				downloadImage := a.downloadImage
				if downloadImage == nil {
					downloadImage = service.GetImageFromUrl
				}
				_, encoded, err := downloadImage(resultURL)
				if err != nil {
					return nil, fmt.Errorf("failed to download KIE image result: %w", err)
				}
				data.Url = ""
				data.B64Json = encoded
			}
			imageData = append(imageData, data)
			if len(imageData) == request.Count {
				break
			}
		}
	}

	responseBody, err := common.Marshal(OpenAIImageResponse{Created: time.Now().Unix(), Data: imageData})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}, nil
}

func kieImageAspectRatio(size string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(size)) {
	case "", "auto":
		return "auto", nil
	case "1:1", "1024x1024":
		return "1:1", nil
	case "3:2", "1536x1024":
		return "3:2", nil
	case "2:3", "1024x1536":
		return "2:3", nil
	default:
		return "", errors.New("KIE GPT Image 2 supports auto, 1:1, 3:2, or 2:3")
	}
}

// UploadReferenceImages validates and uploads multipart image inputs for KIE tasks.
// A zero maxFiles value uses MaxReferenceImages.
func UploadReferenceImages(c *gin.Context, client *Client, maxFiles int) ([]string, error) {
	form := c.Request.MultipartForm
	if form == nil {
		parsed, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return nil, fmt.Errorf("failed to parse image edit form: %w", err)
		}
		form = parsed
		defer form.RemoveAll()
	}
	files := collectImageFiles(form)
	if len(files) == 0 {
		return nil, nil
	}
	if maxFiles <= 0 {
		maxFiles = MaxReferenceImages
	}
	if len(files) > maxFiles {
		return nil, fmt.Errorf("KIE supports at most %d reference images", maxFiles)
	}

	urls := make([]string, 0, len(files))
	for _, fileHeader := range files {
		if fileHeader.Size > MaxReferenceBytes {
			return nil, fmt.Errorf("reference image exceeds %d bytes", MaxReferenceBytes)
		}
		file, err := fileHeader.Open()
		if err != nil {
			return nil, errors.New("failed to open reference image")
		}
		content, readErr := io.ReadAll(io.LimitReader(file, MaxReferenceBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, errors.New("failed to read reference image")
		}
		if closeErr != nil {
			return nil, errors.New("failed to close reference image")
		}
		if len(content) > MaxReferenceBytes {
			return nil, fmt.Errorf("reference image exceeds %d bytes", MaxReferenceBytes)
		}
		mimeType := http.DetectContentType(content)
		if mimeType != "image/jpeg" && mimeType != "image/png" && mimeType != "image/webp" {
			return nil, errors.New("reference image must be JPEG, PNG, or WebP")
		}
		dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(content)
		uploadedURL, err := client.UploadBase64(c.Request.Context(), dataURL)
		if err != nil {
			return nil, err
		}
		urls = append(urls, uploadedURL)
	}
	return urls, nil
}

func collectImageFiles(form *multipart.Form) []*multipart.FileHeader {
	if form == nil {
		return nil
	}
	var files []*multipart.FileHeader
	for _, field := range []string{"image", "image[]", "input_reference", "input_reference[]"} {
		files = append(files, form.File[field]...)
	}
	var indexedFields []string
	for field := range form.File {
		if strings.HasPrefix(field, "image[") && field != "image[]" {
			indexedFields = append(indexedFields, field)
		}
	}
	sort.Strings(indexedFields)
	for _, field := range indexedFields {
		files = append(files, form.File[field]...)
	}
	return files
}
