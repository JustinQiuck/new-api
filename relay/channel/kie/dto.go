package kie

import "github.com/QuantumNous/new-api/relaykit/dto"

type TaskInput struct {
	Prompt      string   `json:"prompt"`
	InputURLs   []string `json:"input_urls,omitempty"`
	ImageURLs   []string `json:"image_urls,omitempty"`
	AspectRatio string   `json:"aspect_ratio,omitempty"`
	Resolution  string   `json:"resolution,omitempty"`
	Duration    *int     `json:"duration,omitempty"`
}

type CreateTaskRequest struct {
	Model       string    `json:"model"`
	Input       TaskInput `json:"input"`
	CallbackURL string    `json:"callBackUrl,omitempty"`
}

type ImageBatchRequest struct {
	Task           CreateTaskRequest `json:"task"`
	Count          int               `json:"count"`
	ResponseFormat string            `json:"response_format,omitempty"`
}

type CreateTaskResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		TaskID string `json:"taskId"`
	} `json:"data"`
}

type TaskRecordResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		TaskID     string `json:"taskId"`
		Model      string `json:"model"`
		State      string `json:"state"`
		ResultJSON string `json:"resultJson"`
		FailCode   string `json:"failCode"`
		FailMsg    string `json:"failMsg"`
		Progress   int    `json:"progress"`
	} `json:"data"`
}

type TaskResult struct {
	ResultURLs []string `json:"resultUrls"`
}

type FileUploadRequest struct {
	Base64Data string `json:"base64Data"`
	UploadPath string `json:"uploadPath"`
}

type FileUploadResponse struct {
	Success bool   `json:"success"`
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Data    struct {
		DownloadURL string `json:"downloadUrl"`
		FileURL     string `json:"fileUrl"`
		MimeType    string `json:"mimeType"`
		FileSize    int64  `json:"fileSize"`
	} `json:"data"`
}

type OpenAIImageResponse struct {
	Created int64           `json:"created"`
	Data    []dto.ImageData `json:"data"`
}
