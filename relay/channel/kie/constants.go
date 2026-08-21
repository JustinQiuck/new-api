package kie

const (
	ChannelName = "kie"

	ResponsesPath  = "/codex/v1/responses"
	CreateTaskPath = "/api/v1/jobs/createTask"
	TaskRecordPath = "/api/v1/jobs/recordInfo"
	FileUploadURL  = "https://kieai.redpandaai.co/api/file-base64-upload"

	PublicTextModel  = "gpt-5.5"
	PublicImageModel = "gpt-image-2"
	PublicVideoModel = "grok-imagine-video"

	KieTextModel  = "gpt-5-5"
	KieImageModel = "gpt-image-2-text-to-image"
	KieImageEdit  = "gpt-image-2-image-to-image"
	KieVideoModel = "grok-imagine-video-1-5-preview"

	MaxImageCount      = 1
	MaxReferenceImages = 16
	MaxReferenceBytes  = 10 * 1024 * 1024
)

var ModelList = []string{
	PublicTextModel,
	PublicImageModel,
	PublicVideoModel,
}

func mapModel(model string) string {
	switch model {
	case PublicTextModel:
		return KieTextModel
	case PublicImageModel:
		return KieImageModel
	case PublicVideoModel:
		return KieVideoModel
	default:
		return model
	}
}
