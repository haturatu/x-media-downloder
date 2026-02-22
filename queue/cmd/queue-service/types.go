package main

import (
	"database/sql"
	"sync"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

type config struct {
	redisAddr        string
	redisPassword    string
	redisDB          int
	queueName        string
	interactiveQueue string
	mediaRoot        string
	dbPath           string
	autotaggerURL    string
	autotaggerEnable bool
	concurrency      int
	apiAddr          string
}

type appState struct {
	cfg       config
	redis     *redis.Client
	asynqCli  *asynq.Client
	store     *store
	inspector *asynq.Inspector
}

type store struct {
	db *sql.DB
	mu sync.Mutex
}

type queueTaskStatus struct {
	Status    string      `json:"status"`
	Result    interface{} `json:"result,omitempty"`
	UpdatedAt string      `json:"updated_at"`
}

type downloadTaskPayload struct {
	TaskID string `json:"task_id"`
	URL    string `json:"url"`
}

type autotagTaskPayload struct {
	TaskID string `json:"task_id"`
}

type deleteUserTaskPayload struct {
	TaskID   string `json:"task_id"`
	Username string `json:"username"`
}

type deleteImageTaskPayload struct {
	TaskID   string `json:"task_id"`
	Filepath string `json:"filepath"`
}

type deleteImagesTaskPayload struct {
	TaskID    string   `json:"task_id"`
	Filepaths []string `json:"filepaths"`
}

type retagImageTaskPayload struct {
	TaskID   string `json:"task_id"`
	Filepath string `json:"filepath"`
}

type retagImagesTaskPayload struct {
	TaskID    string   `json:"task_id"`
	Filepaths []string `json:"filepaths"`
}

type downloadTaskStatusResponse struct {
	TaskID          string  `json:"task_id"`
	URL             *string `json:"url"`
	State           string  `json:"state"`
	Message         string  `json:"message"`
	Current         *int    `json:"current,omitempty"`
	Total           *int    `json:"total,omitempty"`
	DownloadedCount *int    `json:"downloaded_count,omitempty"`
	SkippedCount    *int    `json:"skipped_count,omitempty"`
}

type progressResult struct {
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Status  string `json:"status"`
}

type downloadResult struct {
	URL             string `json:"url"`
	Success         bool   `json:"success"`
	Message         string `json:"message,omitempty"`
	DownloadedCount int    `json:"downloaded_count"`
	SkippedCount    int    `json:"skipped_count"`
}

type autotagResult struct {
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Status  string `json:"status"`
}

type imageTag struct {
	Tag        string  `json:"tag"`
	Confidence float64 `json:"confidence"`
}
