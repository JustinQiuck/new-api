package kie

type sanitizedTaskResponse struct {
	Code int               `json:"code"`
	Data sanitizedTaskData `json:"data"`
}

type sanitizedTaskData struct {
	State    string `json:"state,omitempty"`
	FailCode string `json:"failCode,omitempty"`
	Progress int    `json:"progress,omitempty"`
}
