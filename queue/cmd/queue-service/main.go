package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	_ "modernc.org/sqlite"
)

const (
	taskTypeDownload        = "xmd:download_tweet_media"
	taskTypeAutotagAll      = "xmd:autotag_all"
	taskTypeAutotagUntagged = "xmd:autotag_untagged"
	taskTypeReconcileDB     = "xmd:reconcile_db"
	taskTypeDeleteUser      = "xmd:delete_user"
	taskTypeDeleteImage     = "xmd:delete_image"
	taskTypeDeleteImages    = "xmd:delete_images"
	taskTypeRetagImage      = "xmd:retag_image"

	taskListKey     = "xmd:download_task_ids"
	taskURLHashKey  = "xmd:download_task_urls"
	autotagLastTask = "xmd:autotag:last_task_id"
	taskMetaPrefix  = "xmd:task-meta-"
	maxTrackedTasks = 200
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

var logger = initLogger()

func initLogger() *slog.Logger {
	level := new(slog.LevelVar)
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn", "warning":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}
	l := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
	slog.SetDefault(l)
	return l
}

func main() {
	mode := flag.String("mode", "all", "run mode: all|api|worker")
	flag.Parse()

	cfg := loadConfig()
	st, err := newAppState(cfg)
	if err != nil {
		logger.Error("failed to initialize app state", "error", err)
		os.Exit(1)
	}
	defer st.redis.Close()
	defer st.asynqCli.Close()
	defer st.store.Close()
	defer st.inspector.Close()

	if *mode != "api" {
		if err := st.migrateLegacyMediaLayout(); err != nil {
			logger.Warn("legacy media migration finished with errors", "error", err)
		}
	}

	switch *mode {
	case "api":
		runAPI(st)
	case "worker":
		runWorker(st)
	case "all":
		go runWorker(st)
		runAPI(st)
	default:
		logger.Error("unknown run mode", "mode", *mode)
		os.Exit(1)
	}
}

func loadConfig() config {
	return config{
		redisAddr:        envOrDefault("REDIS_ADDR", "redis:6379"),
		redisPassword:    os.Getenv("REDIS_PASSWORD"),
		redisDB:          envInt("REDIS_DB", 0),
		queueName:        envOrDefault("ASYNQ_QUEUE", "default"),
		interactiveQueue: envOrDefault("ASYNQ_INTERACTIVE_QUEUE", "interactive"),
		mediaRoot:        envOrDefault("MEDIA_ROOT", "/app/downloaded_images"),
		dbPath:           envOrDefault("TAGS_DB_PATH", "/app/tags.db"),
		autotaggerURL:    os.Getenv("AUTOTAGGER_URL"),
		autotaggerEnable: strings.EqualFold(envOrDefault("AUTOTAGGER", "false"), "true"),
		concurrency:      envInt("ASYNQ_CONCURRENCY", 20),
		apiAddr:          envOrDefault("QUEUE_API_ADDR", ":8001"),
	}
}

func newAppState(cfg config) (*appState, error) {
	if err := os.MkdirAll(cfg.mediaRoot, 0o755); err != nil {
		return nil, err
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.redisAddr,
		Password: cfg.redisPassword,
		DB:       cfg.redisDB,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}

	store, err := openStore(cfg.dbPath)
	if err != nil {
		return nil, err
	}

	redisOpt := asynq.RedisClientOpt{Addr: cfg.redisAddr, Password: cfg.redisPassword, DB: cfg.redisDB}
	return &appState{
		cfg:       cfg,
		redis:     rdb,
		asynqCli:  asynq.NewClient(redisOpt),
		store:     store,
		inspector: asynq.NewInspector(redisOpt),
	}, nil
}

func runAPI(st *appState) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/download", st.handleDownload)
	mux.HandleFunc("/api/autotag/reload", st.handleAutotagReload)
	mux.HandleFunc("/api/autotag/untagged", st.handleAutotagUntagged)
	mux.HandleFunc("/api/autotag/reconcile", st.handleReconcileDB)
	mux.HandleFunc("/api/autotag/status", st.handleAutotagStatus)
	mux.HandleFunc("/api/tags", st.handleTags)
	mux.HandleFunc("/api/users", st.handleUsers)
	mux.HandleFunc("/api/users/", st.handleUsersSubroutes)
	mux.HandleFunc("/api/images", st.handleImages)
	mux.HandleFunc("/api/images/bulk-delete", st.handleImagesBulkDelete)
	mux.HandleFunc("/api/images/retag", st.handleImagesRetag)
	mux.HandleFunc("/api/tasks/status", st.handleTaskStatus)

	logger.Info("queue api listening", "addr", st.cfg.apiAddr)
	if err := http.ListenAndServe(st.cfg.apiAddr, loggingMiddleware(mux)); err != nil {
		logger.Error("api server stopped", "error", err)
		os.Exit(1)
	}
}

func runWorker(st *appState) {
	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: st.cfg.redisAddr, Password: st.cfg.redisPassword, DB: st.cfg.redisDB},
		asynq.Config{
			Concurrency: st.cfg.concurrency,
			Queues: map[string]int{
				st.cfg.interactiveQueue: 4,
				st.cfg.queueName:        1,
			},
		},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(taskTypeDownload, st.processDownloadTask)
	mux.HandleFunc(taskTypeAutotagAll, st.processAutotagAllTask)
	mux.HandleFunc(taskTypeAutotagUntagged, st.processAutotagUntaggedTask)
	mux.HandleFunc(taskTypeReconcileDB, st.processReconcileDBTask)
	mux.HandleFunc(taskTypeDeleteUser, st.processDeleteUserTask)
	mux.HandleFunc(taskTypeDeleteImage, st.processDeleteImageTask)
	mux.HandleFunc(taskTypeDeleteImages, st.processDeleteImagesTask)
	mux.HandleFunc(taskTypeRetagImage, st.processRetagImageTask)

	logger.Info("queue worker started",
		"queue", st.cfg.queueName,
		"interactive_queue", st.cfg.interactiveQueue,
		"concurrency", st.cfg.concurrency,
	)
	if err := srv.Run(mux); err != nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func (st *appState) migrateLegacyMediaLayout() error {
	entries, err := os.ReadDir(st.cfg.mediaRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	totalMoved := 0
	var firstErr error
	for _, userEntry := range entries {
		if !userEntry.IsDir() {
			continue
		}
		username := userEntry.Name()
		userPath := filepath.Join(st.cfg.mediaRoot, username)
		moved, err := st.migrateLegacyUserLayout(username, userPath)
		totalMoved += moved
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if totalMoved > 0 {
		logger.Info("legacy media layout migrated", "moved_files", totalMoved)
	}
	return firstErr
}

func (st *appState) migrateLegacyUserLayout(username, userPath string) (int, error) {
	entries, err := os.ReadDir(userPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}

	moved := 0
	var firstErr error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		legacyDir := filepath.Join(userPath, entry.Name())
		imgEntries, err := os.ReadDir(legacyDir)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, img := range imgEntries {
			if img.IsDir() || !isImageFile(img.Name()) {
				continue
			}
			src := filepath.Join(legacyDir, img.Name())
			dst, err := nextAvailablePath(filepath.Join(userPath, img.Name()))
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if err := os.Rename(src, dst); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			oldRel := normalizeRelPath(st.cfg.mediaRoot, src)
			newRel := normalizeRelPath(st.cfg.mediaRoot, dst)
			if err := st.store.MoveTagsPath(oldRel, newRel); err != nil && firstErr == nil {
				firstErr = err
			}
			moved++
		}
		_ = cleanupEmptyParents(legacyDir, st.cfg.mediaRoot)
	}

	if moved > 0 {
		logger.Info("legacy user media migrated", "username", username, "moved_files", moved)
	}
	return moved, firstErr
}

func nextAvailablePath(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		return "", err
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; i <= 9999; i++ {
		candidate := fmt.Sprintf("%s_dup%02d%s", base, i, ext)
		if _, err := os.Stat(candidate); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			}
			return "", err
		}
	}
	return "", fmt.Errorf("no available filename for %s", path)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if reqID == "" {
			reqID = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", reqID)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if recErr := recover(); recErr != nil {
				logger.Error("panic recovered in http handler",
					"request_id", reqID,
					"method", r.Method,
					"path", r.URL.Path,
					"error", recErr,
					"stack", string(debug.Stack()),
				)
				http.Error(rec, "internal server error", http.StatusInternalServerError)
			}

			durationMs := time.Since(start).Milliseconds()
			attrs := []any{
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"query", r.URL.RawQuery,
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", durationMs,
				"remote_addr", r.RemoteAddr,
			}
			switch {
			case rec.status >= 500:
				logger.Error("http request completed", attrs...)
			case rec.status >= 400:
				logger.Warn("http request completed", attrs...)
			default:
				logger.Info("http request completed", attrs...)
			}
		}()

		next.ServeHTTP(rec, r)
	})
}

func (st *appState) handleDownload(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		st.handleDownloadPost(w, r)
	case http.MethodGet:
		st.handleDownloadGet(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (st *appState) handleDownloadPost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URLs []string `json:"urls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.URLs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "URL list is required"})
		return
	}

	ctx := r.Context()
	count := 0
	queued := make([]map[string]string, 0)
	for _, rawURL := range body.URLs {
		url := strings.TrimSpace(rawURL)
		if !isTweetURL(url) {
			continue
		}
		taskID := uuid.NewString()
		payload := downloadTaskPayload{TaskID: taskID, URL: url}
		b, _ := json.Marshal(payload)
		task := asynq.NewTask(taskTypeDownload, b)

		_, err := st.asynqCli.Enqueue(task,
			asynq.Queue(st.cfg.queueName),
			asynq.TaskID(taskID),
			asynq.MaxRetry(0),
			asynq.Timeout(30*time.Minute),
		)
		if err != nil {
			logger.Warn("failed to enqueue download task",
				"task_type", taskTypeDownload,
				"task_id", taskID,
				"url", url,
				"error", err,
			)
			continue
		}

		setTaskState(ctx, st.redis, taskID, "PENDING", map[string]any{"status": "Queued"})
		st.redis.RPush(ctx, taskListKey, taskID)
		st.redis.HSet(ctx, taskURLHashKey, taskID, url)
		count++
		queued = append(queued, map[string]string{"task_id": taskID, "url": url})
	}

	st.redis.LTrim(ctx, taskListKey, -maxTrackedTasks, -1)
	logger.Info("download tasks queued", "count", count)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"message":      fmt.Sprintf("%d download tasks have been queued.", count),
		"queued_tasks": queued,
	})
}

func (st *appState) handleDownloadGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	requested := strings.TrimSpace(r.URL.Query().Get("ids"))
	var taskIDs []string
	if requested != "" {
		taskIDs = uniqueReverse(strings.Split(requested, ","))
	} else {
		ids, err := st.redis.LRange(ctx, taskListKey, -30, -1).Result()
		if err == nil {
			taskIDs = uniqueReverse(ids)
		}
	}

	items := make([]downloadTaskStatusResponse, 0, len(taskIDs))
	for _, id := range taskIDs {
		item := st.resolveDownloadStatus(ctx, strings.TrimSpace(id))
		if item.TaskID != "" {
			items = append(items, item)
		}
	}

	queueDepth := 0
	if q, err := st.inspector.GetQueueInfo(st.cfg.queueName); err == nil {
		queueDepth = q.Pending + q.Active + q.Scheduled + q.Retry
	}

	summary := map[string]int{"total": len(items), "pending": 0, "success": 0, "failure": 0}
	for _, item := range items {
		switch item.State {
		case "PENDING", "PROGRESS":
			summary["pending"]++
		case "SUCCESS":
			summary["success"]++
		case "FAILURE":
			summary["failure"]++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"queue_depth": queueDepth,
		"summary":     summary,
		"items":       items,
	})
}

func (st *appState) resolveDownloadStatus(ctx context.Context, taskID string) downloadTaskStatusResponse {
	if taskID == "" {
		return downloadTaskStatusResponse{}
	}
	urlVal, _ := st.redis.HGet(ctx, taskURLHashKey, taskID).Result()
	var url *string
	if urlVal != "" {
		url = &urlVal
	}

	rec, ok := getTaskState(ctx, st.redis, taskID)
	if !ok {
		return downloadTaskStatusResponse{TaskID: taskID, URL: url, State: "PENDING", Message: "Queued or running"}
	}

	resp := downloadTaskStatusResponse{TaskID: taskID, URL: url, State: rec.Status, Message: "Running"}
	resultMap, _ := rec.Result.(map[string]any)

	switch rec.Status {
	case "PROGRESS":
		if v, ok := intFromAny(resultMap["current"]); ok {
			resp.Current = &v
		}
		if v, ok := intFromAny(resultMap["total"]); ok {
			resp.Total = &v
		}
		if s, ok := stringFromAny(resultMap["status"]); ok {
			resp.Message = s
		}
	case "SUCCESS":
		if s, ok := stringFromAny(resultMap["message"]); ok && s != "" {
			resp.Message = s
		} else {
			resp.Message = "Completed"
		}
		if v, ok := intFromAny(resultMap["downloaded_count"]); ok {
			resp.DownloadedCount = &v
		}
		if v, ok := intFromAny(resultMap["skipped_count"]); ok {
			resp.SkippedCount = &v
		}
	case "FAILURE":
		if s, ok := stringFromAny(resultMap["message"]); ok {
			resp.Message = s
		} else {
			resp.Message = "Task failed"
		}
	default:
		resp.State = "PENDING"
		resp.Message = "Queued or running"
	}
	return resp
}

func (st *appState) handleAutotagReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !st.cfg.autotaggerEnable || st.cfg.autotaggerURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Autotagger is not configured."})
		return
	}
	st.enqueueAutotagTask(w, r, taskTypeAutotagAll, "Started force re-tagging for ALL images in the background.")
}

func (st *appState) handleAutotagUntagged(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !st.cfg.autotaggerEnable || st.cfg.autotaggerURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Autotagger is not configured."})
		return
	}
	st.enqueueAutotagTask(w, r, taskTypeAutotagUntagged, "Autotagging for untagged images started in the background.")
}

func (st *appState) handleReconcileDB(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	st.enqueueAutotagTask(
		w,
		r,
		taskTypeReconcileDB,
		"Started DB consistency check and cleanup in the background.",
	)
}

func (st *appState) enqueueAutotagTask(w http.ResponseWriter, r *http.Request, taskType, message string) {
	taskID := uuid.NewString()
	payload := autotagTaskPayload{TaskID: taskID}
	b, _ := json.Marshal(payload)
	task := asynq.NewTask(taskType, b)

	_, err := st.asynqCli.Enqueue(task,
		asynq.Queue(st.cfg.queueName),
		asynq.TaskID(taskID),
		asynq.MaxRetry(0),
		asynq.Timeout(12*time.Hour),
	)
	if err != nil {
		logger.Error("failed to enqueue autotag task",
			"task_type", taskType,
			"task_id", taskID,
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "failed to queue task"})
		return
	}
	ctx := r.Context()
	st.redis.Set(ctx, autotagLastTask, taskID, 7*24*time.Hour)
	setTaskState(ctx, st.redis, taskID, "PENDING", map[string]any{"status": "Task is pending..."})
	logger.Info("autotag task queued", "task_type", taskType, "task_id", taskID)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": message, "task_id": taskID})
}

func (st *appState) handleAutotagStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	taskID, err := st.redis.Get(ctx, autotagLastTask).Result()
	if err != nil || taskID == "" {
		writeJSON(w, http.StatusOK, map[string]any{"state": "NOT_FOUND", "status": "No autotagging task has been run yet."})
		return
	}
	rec, ok := getTaskState(ctx, st.redis, taskID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"state": "PENDING", "status": "Task is pending..."})
		return
	}

	resultMap, _ := rec.Result.(map[string]any)
	resp := map[string]any{"state": rec.Status, "status": "Processing..."}
	if s, ok := stringFromAny(resultMap["status"]); ok {
		resp["status"] = s
	}
	if v, ok := intFromAny(resultMap["current"]); ok {
		resp["current"] = v
	}
	if v, ok := intFromAny(resultMap["total"]); ok {
		resp["total"] = v
	}
	writeJSON(w, http.StatusOK, resp)
}

func (st *appState) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	taskID := strings.TrimSpace(r.URL.Query().Get("id"))
	if taskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
		return
	}
	rec, ok := getTaskState(r.Context(), st.redis, taskID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"task_id": taskID, "state": "PENDING", "message": "Queued or running"})
		return
	}
	resultMap, _ := rec.Result.(map[string]any)
	message := "Running"
	if s, ok := stringFromAny(resultMap["message"]); ok && s != "" {
		message = s
	} else if s, ok := stringFromAny(resultMap["status"]); ok && s != "" {
		message = s
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id": taskID,
		"state":   rec.Status,
		"message": message,
		"result":  resultMap,
	})
}

func (st *appState) handleTags(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st.handleTagsGet(w, r)
	case http.MethodDelete:
		st.handleTagsDelete(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (st *appState) handleTagsGet(w http.ResponseWriter, r *http.Request) {
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	perPage := parsePositiveInt(r.URL.Query().Get("per_page"), 100)
	offset := (page - 1) * perPage
	allItems := parseBoolParam(r.URL.Query().Get("all"))
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	match := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("match")))
	minCount := parseNonNegativeInt(r.URL.Query().Get("min_count"), -1)
	maxCount := parseNonNegativeInt(r.URL.Query().Get("max_count"), -1)
	sortBy := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))

	tags, err := st.store.GetAllTags()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal Server Error"})
		return
	}
	filtered := make([]map[string]any, 0, len(tags))
	for _, item := range tags {
		tagVal, _ := item["tag"].(string)
		countInt := 0
		switch v := item["count"].(type) {
		case int:
			countInt = v
		case int64:
			countInt = int(v)
		case float64:
			countInt = int(v)
		}

		if q != "" {
			tagLower := strings.ToLower(tagVal)
			if match == "exact" {
				if tagLower != q {
					continue
				}
			} else if !strings.Contains(tagLower, q) {
				continue
			}
		}
		if minCount >= 0 && countInt < minCount {
			continue
		}
		if maxCount >= 0 && countInt > maxCount {
			continue
		}
		filtered = append(filtered, map[string]any{
			"tag":   tagVal,
			"count": countInt,
		})
	}
	tags = filtered

	switch sortBy {
	case "name_desc":
		sort.Slice(tags, func(i, j int) bool {
			a, _ := tags[i]["tag"].(string)
			b, _ := tags[j]["tag"].(string)
			return strings.ToLower(a) > strings.ToLower(b)
		})
	case "count_asc":
		sort.Slice(tags, func(i, j int) bool {
			a, _ := tags[i]["count"].(int)
			b, _ := tags[j]["count"].(int)
			if a == b {
				at, _ := tags[i]["tag"].(string)
				bt, _ := tags[j]["tag"].(string)
				return strings.ToLower(at) < strings.ToLower(bt)
			}
			return a < b
		})
	case "name_asc":
		sort.Slice(tags, func(i, j int) bool {
			a, _ := tags[i]["tag"].(string)
			b, _ := tags[j]["tag"].(string)
			return strings.ToLower(a) < strings.ToLower(b)
		})
	default:
		sort.Slice(tags, func(i, j int) bool {
			a, _ := tags[i]["count"].(int)
			b, _ := tags[j]["count"].(int)
			if a == b {
				at, _ := tags[i]["tag"].(string)
				bt, _ := tags[j]["tag"].(string)
				return strings.ToLower(at) < strings.ToLower(bt)
			}
			return a > b
		})
	}

	totalItems := len(tags)
	if allItems {
		writeJSON(w, http.StatusOK, map[string]any{
			"items":        tags,
			"total_items":  totalItems,
			"per_page":     totalItems,
			"current_page": 1,
			"total_pages":  1,
		})
		return
	}
	start, end := pageBounds(offset, perPage, totalItems)
	writeJSON(w, http.StatusOK, map[string]any{
		"items":        tags[start:end],
		"total_items":  totalItems,
		"per_page":     perPage,
		"current_page": page,
		"total_pages":  totalPages(totalItems, perPage),
	})
}

func (st *appState) handleTagsDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tag string `json:"tag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "tag is required"})
		return
	}
	tag := strings.TrimSpace(body.Tag)
	if tag == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "tag is required"})
		return
	}
	deleted, err := st.store.DeleteTag(tag)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal Server Error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"message":       fmt.Sprintf("Deleted tag '%s' from %d entries", tag, deleted),
		"tag":           tag,
		"deleted_count": deleted,
	})
}

func (st *appState) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st.handleUsersGet(w, r)
	case http.MethodDelete:
		st.handleUsersDelete(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (st *appState) handleUsersGet(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	match := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("match")))
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	perPage := parsePositiveInt(r.URL.Query().Get("per_page"), 100)
	offset := (page - 1) * perPage
	allItems := parseBoolParam(r.URL.Query().Get("all"))
	minTweets := parseNonNegativeInt(r.URL.Query().Get("min_tweets"), -1)
	maxTweets := parseNonNegativeInt(r.URL.Query().Get("max_tweets"), -1)
	sortBy := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))

	type userInfo struct {
		Username   string `json:"username"`
		TweetCount int    `json:"tweet_count"`
	}
	users := make([]userInfo, 0)
	entries, err := os.ReadDir(st.cfg.mediaRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal Server Error"})
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		username := entry.Name()
		if q != "" {
			usernameLower := strings.ToLower(username)
			if match == "exact" {
				if usernameLower != q {
					continue
				}
			} else if !strings.Contains(usernameLower, q) {
				continue
			}
		}
		userPath := filepath.Join(st.cfg.mediaRoot, username)
		tweetIDs, err := collectUserTweetIDs(userPath)
		if err != nil {
			continue
		}
		tweetCount := len(tweetIDs)
		if tweetCount <= 0 {
			continue
		}
		if minTweets >= 0 && tweetCount < minTweets {
			continue
		}
		if maxTweets >= 0 && tweetCount > maxTweets {
			continue
		}
		users = append(users, userInfo{Username: username, TweetCount: tweetCount})
	}
	switch sortBy {
	case "name_desc":
		sort.Slice(users, func(i, j int) bool { return strings.ToLower(users[i].Username) > strings.ToLower(users[j].Username) })
	case "tweets_desc":
		sort.Slice(users, func(i, j int) bool {
			if users[i].TweetCount == users[j].TweetCount {
				return strings.ToLower(users[i].Username) < strings.ToLower(users[j].Username)
			}
			return users[i].TweetCount > users[j].TweetCount
		})
	case "tweets_asc":
		sort.Slice(users, func(i, j int) bool {
			if users[i].TweetCount == users[j].TweetCount {
				return strings.ToLower(users[i].Username) < strings.ToLower(users[j].Username)
			}
			return users[i].TweetCount < users[j].TweetCount
		})
	default:
		sort.Slice(users, func(i, j int) bool { return strings.ToLower(users[i].Username) < strings.ToLower(users[j].Username) })
	}

	totalItems := len(users)
	if allItems {
		writeJSON(w, http.StatusOK, map[string]any{
			"items":        users,
			"total_items":  totalItems,
			"per_page":     totalItems,
			"current_page": 1,
			"total_pages":  1,
		})
		return
	}
	start, end := pageBounds(offset, perPage, totalItems)
	writeJSON(w, http.StatusOK, map[string]any{
		"items":        users[start:end],
		"total_items":  totalItems,
		"per_page":     perPage,
		"current_page": page,
		"total_pages":  totalPages(totalItems, perPage),
	})
}

func (st *appState) handleUsersDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "username is required"})
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" || strings.Contains(username, "/") || strings.Contains(username, "\\") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid username"})
		return
	}
	taskID := uuid.NewString()
	payload := deleteUserTaskPayload{TaskID: taskID, Username: username}
	b, _ := json.Marshal(payload)
	task := asynq.NewTask(taskTypeDeleteUser, b)
	_, err := st.asynqCli.Enqueue(task,
		asynq.Queue(st.cfg.interactiveQueue),
		asynq.TaskID(taskID),
		asynq.MaxRetry(0),
		asynq.Timeout(10*time.Minute),
	)
	if err != nil {
		logger.Error("failed to enqueue delete user task",
			"task_type", taskTypeDeleteUser,
			"task_id", taskID,
			"username", username,
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to queue task"})
		return
	}
	setTaskState(r.Context(), st.redis, taskID, "PENDING", map[string]any{"message": "Delete user task queued"})
	logger.Info("delete user task queued", "task_id", taskID, "username", username)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"success": true,
		"queued":  true,
		"task_id": taskID,
		"message": "Delete user task queued",
	})
}

func (st *appState) handleUsersSubroutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/users/")
	if !strings.HasSuffix(path, "/tweets") {
		http.NotFound(w, r)
		return
	}
	username := strings.TrimSuffix(path, "/tweets")
	username = strings.TrimSuffix(username, "/")
	if username == "" || strings.Contains(username, "/") || strings.Contains(username, "\\") {
		http.NotFound(w, r)
		return
	}
	st.handleUserTweetsGet(w, r, username)
}

func (st *appState) handleUserTweetsGet(w http.ResponseWriter, r *http.Request, username string) {
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	perPage := parsePositiveInt(r.URL.Query().Get("per_page"), 100)
	offset := (page - 1) * perPage
	returnAll := strings.TrimSpace(r.URL.Query().Get("all")) == "1"
	minTagCount := parseNonNegativeInt(r.URL.Query().Get("min_tag_count"), -1)
	maxTagCount := parseNonNegativeInt(r.URL.Query().Get("max_tag_count"), -1)
	excludeTags := splitCSV(r.URL.Query().Get("exclude_tags"))

	userPath, err := resolvePathUnderRoot(st.cfg.mediaRoot, username)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "User not found"})
		return
	}
	entries, err := os.ReadDir(userPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "User not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal Server Error"})
		return
	}

	imagesByTweet := make(map[string][]string)
	for _, entry := range entries {
		entryPath := filepath.Join(userPath, entry.Name())
		if entry.IsDir() {
			tweetID := entry.Name()
			imgEntries, err := os.ReadDir(entryPath)
			if err != nil {
				continue
			}
			for _, img := range imgEntries {
				if img.IsDir() || !isImageFile(img.Name()) {
					continue
				}
				full := filepath.Join(entryPath, img.Name())
				imagesByTweet[tweetID] = append(imagesByTweet[tweetID], normalizeRelPath(st.cfg.mediaRoot, full))
			}
			continue
		}

		if !isImageFile(entry.Name()) {
			continue
		}
		tweetID := tweetIDFromFilename(entry.Name())
		if tweetID == "" {
			continue
		}
		imagesByTweet[tweetID] = append(imagesByTweet[tweetID], normalizeRelPath(st.cfg.mediaRoot, entryPath))
	}

	tweetIDs := make([]string, 0, len(imagesByTweet))
	for tweetID, paths := range imagesByTweet {
		if len(paths) > 0 {
			tweetIDs = append(tweetIDs, tweetID)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(tweetIDs)))

	type tweet struct {
		TweetID string `json:"tweet_id"`
		Images  []any  `json:"images"`
	}
	tweets := make([]tweet, 0, len(tweetIDs))
	for _, tweetID := range tweetIDs {
		imagePaths := imagesByTweet[tweetID]
		if len(imagePaths) == 0 {
			continue
		}
		sort.Strings(imagePaths)

		tagsMap, err := st.store.GetTagsForFiles(imagePaths)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal Server Error"})
			return
		}

		images := make([]any, 0, len(imagePaths))
		for _, p := range imagePaths {
			tagsForImage := tagsMap[p]
			if hasTagPattern(tagsForImage, excludeTags) {
				continue
			}
			tagCount := len(tagsForImage)
			if minTagCount >= 0 && tagCount < minTagCount {
				continue
			}
			if maxTagCount >= 0 && tagCount > maxTagCount {
				continue
			}
			images = append(images, map[string]any{"path": p, "tags": tagsForImage})
		}
		if len(images) == 0 {
			continue
		}
		tweets = append(tweets, tweet{TweetID: tweetID, Images: images})
	}

	totalItems := len(tweets)
	if returnAll {
		writeJSON(w, http.StatusOK, map[string]any{
			"items":        tweets,
			"total_items":  totalItems,
			"per_page":     totalItems,
			"current_page": 1,
			"total_pages": func() int {
				if totalItems == 0 {
					return 0
				}
				return 1
			}(),
		})
		return
	}

	start, end := pageBounds(offset, perPage, totalItems)
	writeJSON(w, http.StatusOK, map[string]any{
		"items":        tweets[start:end],
		"total_items":  totalItems,
		"per_page":     perPage,
		"current_page": page,
		"total_pages":  totalPages(totalItems, perPage),
	})
}

func (st *appState) handleImages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st.handleImagesGet(w, r)
	case http.MethodDelete:
		st.handleImagesDelete(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (st *appState) handleImagesBulkDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Filepaths []string `json:"filepaths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Filepaths) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "filepaths is required"})
		return
	}

	uniq := make(map[string]struct{}, len(body.Filepaths))
	filepaths := make([]string, 0, len(body.Filepaths))
	for _, raw := range body.Filepaths {
		rel := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		if rel == "" {
			continue
		}
		if _, exists := uniq[rel]; exists {
			continue
		}
		uniq[rel] = struct{}{}
		filepaths = append(filepaths, rel)
	}
	if len(filepaths) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "filepaths is required"})
		return
	}

	taskID := uuid.NewString()
	payload := deleteImagesTaskPayload{TaskID: taskID, Filepaths: filepaths}
	b, _ := json.Marshal(payload)
	task := asynq.NewTask(taskTypeDeleteImages, b)
	_, err := st.asynqCli.Enqueue(task,
		asynq.Queue(st.cfg.interactiveQueue),
		asynq.TaskID(taskID),
		asynq.MaxRetry(0),
		asynq.Timeout(30*time.Minute),
	)
	if err != nil {
		logger.Error("failed to enqueue bulk delete image task",
			"task_type", taskTypeDeleteImages,
			"task_id", taskID,
			"count", len(filepaths),
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to queue task"})
		return
	}
	setTaskState(r.Context(), st.redis, taskID, "PENDING", map[string]any{
		"message": fmt.Sprintf("Bulk delete task queued (%d images)", len(filepaths)),
		"total":   len(filepaths),
	})
	logger.Info("bulk delete image task queued", "task_id", taskID, "count", len(filepaths))
	writeJSON(w, http.StatusAccepted, map[string]any{
		"success":      true,
		"queued":       true,
		"task_id":      taskID,
		"queued_count": len(filepaths),
		"message":      "Bulk delete image task queued",
	})
}

func (st *appState) handleImagesGet(w http.ResponseWriter, r *http.Request) {
	sortMode := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sortMode == "" {
		sortMode = "latest"
	}
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	perPage := parsePositiveInt(r.URL.Query().Get("per_page"), 100)
	offset := (page - 1) * perPage
	returnAll := strings.TrimSpace(r.URL.Query().Get("all")) == "1"
	searchTags := splitCSV(r.URL.Query().Get("tags"))
	minTagCount := parseNonNegativeInt(r.URL.Query().Get("min_tag_count"), -1)
	maxTagCount := parseNonNegativeInt(r.URL.Query().Get("max_tag_count"), -1)
	excludeTags := splitCSV(r.URL.Query().Get("exclude_tags"))

	type imageInfo struct {
		Path  string
		MTime int64
	}
	allImages := make([]imageInfo, 0)

	if len(searchTags) > 0 {
		paths, err := st.store.FindFilesByTagPatterns(searchTags)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal Server Error"})
			return
		}
		for _, p := range paths {
			full := filepath.Join(st.cfg.mediaRoot, filepath.FromSlash(p))
			info, err := os.Stat(full)
			if err != nil {
				continue
			}
			allImages = append(allImages, imageInfo{Path: p, MTime: info.ModTime().UnixMilli()})
		}
	} else {
		files, err := listImageFiles(st.cfg.mediaRoot)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal Server Error"})
			return
		}
		for _, full := range files {
			info, err := os.Stat(full)
			if err != nil {
				continue
			}
			allImages = append(allImages, imageInfo{Path: normalizeRelPath(st.cfg.mediaRoot, full), MTime: info.ModTime().UnixMilli()})
		}
	}

	allTagsMap := map[string][]imageTag{}
	if minTagCount >= 0 || maxTagCount >= 0 || len(excludeTags) > 0 {
		paths := make([]string, 0, len(allImages))
		for _, img := range allImages {
			paths = append(paths, img.Path)
		}
		tagsMap, err := st.store.GetTagsForFiles(paths)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal Server Error"})
			return
		}
		allTagsMap = tagsMap

		filtered := make([]imageInfo, 0, len(allImages))
		for _, img := range allImages {
			tagsForImage := tagsMap[img.Path]
			if hasTagPattern(tagsForImage, excludeTags) {
				continue
			}
			tagCount := len(tagsForImage)
			if minTagCount >= 0 && tagCount < minTagCount {
				continue
			}
			if maxTagCount >= 0 && tagCount > maxTagCount {
				continue
			}
			filtered = append(filtered, img)
		}
		allImages = filtered
	}

	totalItems := len(allImages)
	switch sortMode {
	case "random":
		rand.Shuffle(len(allImages), func(i, j int) { allImages[i], allImages[j] = allImages[j], allImages[i] })
	default:
		sort.Slice(allImages, func(i, j int) bool { return allImages[i].MTime > allImages[j].MTime })
	}

	pageImages := allImages
	if !returnAll {
		start, end := pageBounds(offset, perPage, totalItems)
		pageImages = allImages[start:end]
	}
	paths := make([]string, 0, len(pageImages))
	for _, img := range pageImages {
		paths = append(paths, img.Path)
	}
	tagsMap := allTagsMap
	if minTagCount < 0 && maxTagCount < 0 && len(excludeTags) == 0 {
		var err error
		tagsMap, err = st.store.GetTagsForFiles(paths)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal Server Error"})
			return
		}
	}

	items := make([]any, 0, len(pageImages))
	for _, img := range pageImages {
		items = append(items, map[string]any{
			"path": img.Path,
			"tags": tagsMap[img.Path],
		})
	}
	respPerPage := perPage
	respCurrentPage := page
	respTotalPages := totalPages(totalItems, perPage)
	if returnAll {
		respPerPage = totalItems
		respCurrentPage = 1
		if totalItems == 0 {
			respTotalPages = 0
		} else {
			respTotalPages = 1
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":        items,
		"total_items":  totalItems,
		"per_page":     respPerPage,
		"current_page": respCurrentPage,
		"total_pages":  respTotalPages,
	})
}

func (st *appState) handleImagesDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Filepath string `json:"filepath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "filepath is required"})
		return
	}
	rel := strings.TrimSpace(strings.ReplaceAll(body.Filepath, "\\", "/"))
	if rel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "filepath is required"})
		return
	}
	taskID := uuid.NewString()
	payload := deleteImageTaskPayload{TaskID: taskID, Filepath: rel}
	b, _ := json.Marshal(payload)
	task := asynq.NewTask(taskTypeDeleteImage, b)
	_, err := st.asynqCli.Enqueue(task,
		asynq.Queue(st.cfg.interactiveQueue),
		asynq.TaskID(taskID),
		asynq.MaxRetry(0),
		asynq.Timeout(5*time.Minute),
	)
	if err != nil {
		logger.Error("failed to enqueue delete image task",
			"task_type", taskTypeDeleteImage,
			"task_id", taskID,
			"filepath", rel,
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to queue task"})
		return
	}
	setTaskState(r.Context(), st.redis, taskID, "PENDING", map[string]any{"message": "Delete image task queued"})
	logger.Info("delete image task queued", "task_id", taskID, "filepath", rel)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"success": true,
		"queued":  true,
		"task_id": taskID,
		"message": "Delete image task queued",
	})
}

func (st *appState) handleImagesRetag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Filepath string `json:"filepath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "filepath is required"})
		return
	}
	rel := strings.TrimSpace(strings.ReplaceAll(body.Filepath, "\\", "/"))
	if rel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "filepath is required"})
		return
	}
	taskID := uuid.NewString()
	payload := retagImageTaskPayload{TaskID: taskID, Filepath: rel}
	b, _ := json.Marshal(payload)
	task := asynq.NewTask(taskTypeRetagImage, b)
	_, err := st.asynqCli.Enqueue(task,
		asynq.Queue(st.cfg.interactiveQueue),
		asynq.TaskID(taskID),
		asynq.MaxRetry(0),
		asynq.Timeout(10*time.Minute),
	)
	if err != nil {
		logger.Error("failed to enqueue retag image task",
			"task_type", taskTypeRetagImage,
			"task_id", taskID,
			"filepath", rel,
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to queue task"})
		return
	}
	setTaskState(r.Context(), st.redis, taskID, "PENDING", map[string]any{"message": "Retag task queued"})
	logger.Info("retag image task queued", "task_id", taskID, "filepath", rel)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"success": true,
		"queued":  true,
		"task_id": taskID,
		"message": "Retag task queued",
	})
}

func (st *appState) processDownloadTask(ctx context.Context, t *asynq.Task) error {
	var payload downloadTaskPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return err
	}
	taskID := payload.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}
	url := payload.URL
	if !isTweetURL(url) {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": "invalid tweet url"})
		return errors.New("invalid tweet url")
	}

	username := extractUsername(url)
	imageURLs, err := getTweetImages(url)
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}
	if len(imageURLs) == 0 {
		res := downloadResult{URL: url, Success: false, Message: "No images found", DownloadedCount: 0, SkippedCount: 0}
		setTaskState(ctx, st.redis, taskID, "SUCCESS", toMap(res))
		return nil
	}

	success := 0
	skipped := 0
	failed := 0
	total := len(imageURLs)
	setTaskState(ctx, st.redis, taskID, "PROGRESS", toMap(progressResult{Current: 0, Total: total, Status: fmt.Sprintf("Starting download for %s...", username)}))

	for i, imageURL := range imageURLs {
		res := st.downloadImage(imageURL, url, username, i+1)
		switch res {
		case "success":
			success++
		case "skipped":
			skipped++
		default:
			failed++
		}
		setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{
			"current": i + 1,
			"total":   total,
			"status":  fmt.Sprintf("saved:%d skipped:%d failed:%d", success, skipped, failed),
		})
	}

	res := downloadResult{
		URL:             url,
		Success:         success > 0,
		DownloadedCount: success,
		SkippedCount:    skipped,
		Message:         fmt.Sprintf("completed with saved:%d skipped:%d failed:%d", success, skipped, failed),
	}
	setTaskState(ctx, st.redis, taskID, "SUCCESS", toMap(res))
	return nil
}

func (st *appState) processAutotagAllTask(ctx context.Context, t *asynq.Task) error {
	var payload autotagTaskPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return err
	}
	taskID := payload.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}
	setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{"current": 0, "total": 1, "status": "Clearing database..."})

	if err := st.store.DeleteAllTags(); err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"status": err.Error(), "message": err.Error()})
		return err
	}
	if err := st.store.ClearProcessedImages(); err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"status": err.Error(), "message": err.Error()})
		return err
	}

	files, err := listImageFiles(st.cfg.mediaRoot)
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"status": err.Error(), "message": err.Error()})
		return err
	}
	if len(files) == 0 {
		setTaskState(ctx, st.redis, taskID, "SUCCESS", toMap(autotagResult{Current: 0, Total: 0, Status: "No images found to process."}))
		return nil
	}

	processed := 0
	total := len(files)
	for _, full := range files {
		rel := normalizeRelPath(st.cfg.mediaRoot, full)
		hash, err := fileMD5(full)
		if err == nil {
			_ = st.autotagFile(full, rel, hash)
			_ = st.store.MarkImageProcessed(hash)
			processed++
		}
		setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{
			"current": processed,
			"total":   total,
			"status":  fmt.Sprintf("Processed %d/%d (last: %s)", processed, total, rel),
		})
	}

	setTaskState(ctx, st.redis, taskID, "SUCCESS", toMap(autotagResult{Current: processed, Total: total, Status: fmt.Sprintf("Complete! Processed %d files.", processed)}))
	return nil
}

func (st *appState) processAutotagUntaggedTask(ctx context.Context, t *asynq.Task) error {
	var payload autotagTaskPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return err
	}
	taskID := payload.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}
	setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{"current": 0, "total": 1, "status": "Finding untagged files..."})

	tagged, err := st.store.GetAllTaggedFilepaths()
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"status": err.Error(), "message": err.Error()})
		return err
	}

	files, err := listImageFiles(st.cfg.mediaRoot)
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"status": err.Error(), "message": err.Error()})
		return err
	}
	untagged := make([]string, 0)
	for _, full := range files {
		rel := normalizeRelPath(st.cfg.mediaRoot, full)
		if _, ok := tagged[rel]; !ok {
			untagged = append(untagged, full)
		}
	}

	if len(untagged) == 0 {
		setTaskState(ctx, st.redis, taskID, "SUCCESS", toMap(autotagResult{Current: 0, Total: 0, Status: "No new untagged images to process."}))
		return nil
	}

	processed := 0
	total := len(untagged)
	for _, full := range untagged {
		rel := normalizeRelPath(st.cfg.mediaRoot, full)
		hash, err := fileMD5(full)
		if err == nil {
			_ = st.autotagFile(full, rel, hash)
			_ = st.store.MarkImageProcessed(hash)
			processed++
		}
		setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{
			"current": processed,
			"total":   total,
			"status":  fmt.Sprintf("Processed %d/%d (last: %s)", processed, total, rel),
		})
	}

	setTaskState(ctx, st.redis, taskID, "SUCCESS", toMap(autotagResult{Current: processed, Total: total, Status: fmt.Sprintf("Complete! Processed %d files.", processed)}))
	return nil
}

func (st *appState) processReconcileDBTask(ctx context.Context, t *asynq.Task) error {
	var payload autotagTaskPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return err
	}
	taskID := payload.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}

	files, err := listImageFiles(st.cfg.mediaRoot)
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}

	total := len(files)
	setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{
		"current": 0,
		"total":   total,
		"status":  "Scanning media files and calculating hashes...",
	})

	existingPaths := make(map[string]struct{}, len(files))
	existingHashes := make(map[string]struct{}, len(files))
	hashReadErrors := 0

	for i, full := range files {
		rel := normalizeRelPath(st.cfg.mediaRoot, full)
		existingPaths[rel] = struct{}{}

		hash, err := fileMD5(full)
		if err != nil {
			hashReadErrors++
		} else {
			existingHashes[hash] = struct{}{}
		}

		if i%100 == 0 || i == total-1 {
			setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{
				"current": i + 1,
				"total":   total,
				"status":  fmt.Sprintf("Scanned %d/%d files", i+1, total),
			})
		}
	}

	processedHashes, err := st.store.GetAllProcessedHashes()
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}
	staleHashes := make([]string, 0)
	for _, h := range processedHashes {
		if _, ok := existingHashes[h]; !ok {
			staleHashes = append(staleHashes, h)
		}
	}

	removedHashCount, err := st.store.DeleteProcessedHashes(staleHashes)
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}

	taggedPaths, err := st.store.GetAllTaggedFilepaths()
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}
	removedTagPathCount := 0
	for p := range taggedPaths {
		if _, ok := existingPaths[p]; ok {
			continue
		}
		if err := st.store.DeleteTagsForFile(p); err == nil {
			removedTagPathCount++
		}
	}

	setTaskState(ctx, st.redis, taskID, "SUCCESS", map[string]any{
		"success":                 true,
		"message":                 "DB consistency reconciliation completed",
		"scanned_files":           total,
		"db_hashes_total":         len(processedHashes),
		"removed_stale_hashes":    removedHashCount,
		"removed_missing_tagsets": removedTagPathCount,
		"hash_read_errors":        hashReadErrors,
	})
	return nil
}

func (st *appState) processDeleteUserTask(ctx context.Context, t *asynq.Task) error {
	var payload deleteUserTaskPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return err
	}
	taskID := payload.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}
	username := strings.TrimSpace(payload.Username)
	if username == "" {
		err := errors.New("invalid username")
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}

	userPath, err := resolvePathUnderRoot(st.cfg.mediaRoot, username)
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": "Invalid username"})
		return err
	}
	setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{"message": "Deleting user..."})

	imageCount := countImages(userPath)
	if err := os.RemoveAll(userPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}
	if err := st.store.DeleteTagsForUser(username); err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}

	setTaskState(ctx, st.redis, taskID, "SUCCESS", map[string]any{
		"success":        true,
		"message":        fmt.Sprintf("Deleted user '%s' and %d images", username, imageCount),
		"username":       username,
		"deleted_images": imageCount,
	})
	return nil
}

func (st *appState) processDeleteImageTask(ctx context.Context, t *asynq.Task) error {
	var payload deleteImageTaskPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return err
	}
	taskID := payload.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}
	rel := strings.TrimSpace(strings.ReplaceAll(payload.Filepath, "\\", "/"))
	if rel == "" {
		err := errors.New("filepath is required")
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}

	full, err := resolvePathUnderRoot(st.cfg.mediaRoot, rel)
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": "Invalid filepath"})
		return err
	}
	setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{"message": "Deleting image..."})

	if err := os.Remove(full); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": "Image not found"})
			return err
		}
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}
	_ = st.store.DeleteTagsForFile(rel)
	_ = cleanupEmptyParents(full, st.cfg.mediaRoot)
	setTaskState(ctx, st.redis, taskID, "SUCCESS", map[string]any{
		"success":  true,
		"message":  "Image deleted",
		"filepath": rel,
	})
	return nil
}

func (st *appState) processDeleteImagesTask(ctx context.Context, t *asynq.Task) error {
	var payload deleteImagesTaskPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return err
	}
	taskID := payload.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}
	if len(payload.Filepaths) == 0 {
		err := errors.New("filepaths is required")
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}

	uniq := make(map[string]struct{}, len(payload.Filepaths))
	filepaths := make([]string, 0, len(payload.Filepaths))
	for _, raw := range payload.Filepaths {
		rel := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		if rel == "" {
			continue
		}
		if _, exists := uniq[rel]; exists {
			continue
		}
		uniq[rel] = struct{}{}
		filepaths = append(filepaths, rel)
	}
	if len(filepaths) == 0 {
		err := errors.New("filepaths is required")
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}

	deleted := 0
	notFound := 0
	failed := 0
	total := len(filepaths)
	setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{
		"current": 0,
		"total":   total,
		"message": "Deleting images...",
	})

	for i, rel := range filepaths {
		full, err := resolvePathUnderRoot(st.cfg.mediaRoot, rel)
		if err != nil {
			failed++
		} else {
			if err := os.Remove(full); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					notFound++
				} else {
					failed++
				}
			} else {
				deleted++
				_ = st.store.DeleteTagsForFile(rel)
				_ = cleanupEmptyParents(full, st.cfg.mediaRoot)
			}
		}

		if i%20 == 0 || i == total-1 {
			setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{
				"current": i + 1,
				"total":   total,
				"status":  fmt.Sprintf("deleted:%d not_found:%d failed:%d", deleted, notFound, failed),
			})
		}
	}

	result := map[string]any{
		"success":         true,
		"message":         fmt.Sprintf("Bulk delete completed. deleted:%d not_found:%d failed:%d", deleted, notFound, failed),
		"deleted_count":   deleted,
		"not_found_count": notFound,
		"failed_count":    failed,
		"total":           total,
	}
	if deleted == 0 && failed > 0 {
		setTaskState(ctx, st.redis, taskID, "FAILURE", result)
		return errors.New("bulk delete failed")
	}
	setTaskState(ctx, st.redis, taskID, "SUCCESS", result)
	return nil
}

func (st *appState) processRetagImageTask(ctx context.Context, t *asynq.Task) error {
	var payload retagImageTaskPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return err
	}
	taskID := payload.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}
	rel := strings.TrimSpace(strings.ReplaceAll(payload.Filepath, "\\", "/"))
	if rel == "" {
		err := errors.New("filepath is required")
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}

	existing, err := st.store.GetTagsForFiles([]string{rel})
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}
	if len(existing[rel]) > 0 {
		setTaskState(ctx, st.redis, taskID, "SUCCESS", map[string]any{
			"success": true,
			"message": "Image already has tags.",
			"tags":    existing[rel],
		})
		return nil
	}

	full, err := resolvePathUnderRoot(st.cfg.mediaRoot, rel)
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": "Invalid filepath"})
		return err
	}
	if _, err := os.Stat(full); err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": "File not found"})
		return err
	}

	setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{"message": "Retagging image..."})
	hash, err := fileMD5(full)
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": "Could not read file"})
		return err
	}
	_ = st.autotagFile(full, rel, hash)
	_ = st.store.MarkImageProcessed(hash)
	updated, err := st.store.GetTagsForFiles([]string{rel})
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}
	setTaskState(ctx, st.redis, taskID, "SUCCESS", map[string]any{
		"success": true,
		"message": "Tags generated successfully!",
		"tags":    updated[rel],
	})
	return nil
}

func (st *appState) downloadImage(imageURL, tweetURL, username string, index int) string {
	req, _ := http.NewRequest(http.MethodGet, imageURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "failed"
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "failed"
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || len(body) == 0 {
		return "failed"
	}

	hashArr := md5.Sum(body)
	hash := hex.EncodeToString(hashArr[:])
	processed, err := st.store.IsImageProcessed(hash)
	if err == nil && processed {
		return "skipped"
	}

	tweetID := tweetIDFromURL(tweetURL)
	ext := extFromContentType(resp.Header.Get("content-type"))
	userDir := filepath.Join(st.cfg.mediaRoot, username)
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		return "failed"
	}
	filename := fmt.Sprintf("%s_%02d%s", tweetID, index, ext)
	fullPath := filepath.Join(userDir, filename)
	if err := os.WriteFile(fullPath, body, 0o644); err != nil {
		return "failed"
	}

	relPath := normalizeRelPath(st.cfg.mediaRoot, fullPath)
	if err := st.store.MarkImageProcessed(hash); err != nil {
		return "failed"
	}
	_ = st.autotagFile(fullPath, relPath, hash)
	return "success"
}

func (st *appState) autotagFile(fullPath, relativePath, _ string) error {
	if !st.cfg.autotaggerEnable || st.cfg.autotaggerURL == "" {
		return nil
	}

	f, err := os.Open(fullPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(fullPath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	if err := writer.WriteField("format", "json"); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, _ := http.NewRequest(http.MethodPost, st.cfg.autotaggerURL, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("autotagger response status=%d", resp.StatusCode)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var parsed []struct {
		Tags map[string]float64 `json:"tags"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return err
	}
	if len(parsed) == 0 || len(parsed[0].Tags) == 0 {
		return nil
	}

	tags := make(map[string]float64)
	for tag, conf := range parsed[0].Tags {
		if conf > 0.4 {
			tags[tag] = conf
		}
	}
	if len(tags) == 0 {
		return nil
	}
	return st.store.AddTags(relativePath, tags)
}

func openStore(path string) (*store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create db directory %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o664)
	if err != nil {
		return nil, fmt.Errorf("failed to open db file %s for read/write: %w", path, err)
	}
	_ = f.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sql open failed for %s: %w", path, err)
	}
	// SQLite is safer with a single shared connection under concurrent access.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE;`); err != nil {
		return nil, fmt.Errorf("set journal mode failed for %s: %w", path, err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return nil, fmt.Errorf("set busy timeout failed for %s: %w", path, err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS image_tags (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			filepath TEXT NOT NULL,
			tag TEXT NOT NULL,
			confidence REAL,
			UNIQUE(filepath, tag)
		);
	`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS processed_images (
			image_hash TEXT PRIMARY KEY
		);
	`); err != nil {
		return nil, err
	}
	return &store{db: db}, nil
}

func isRetryableSQLiteError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database is busy") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "unable to open database file")
}

func withSQLiteRetry(op func() error) error {
	var err error
	backoff := 50 * time.Millisecond
	for i := 0; i < 4; i++ {
		err = op()
		if err == nil {
			return nil
		}
		if !isRetryableSQLiteError(err) {
			return err
		}
		time.Sleep(backoff)
		backoff *= 2
	}
	return err
}

func (s *store) Close() error {
	return s.db.Close()
}

func (s *store) IsImageProcessed(hash string) (bool, error) {
	var found bool
	err := withSQLiteRetry(func() error {
		var x int
		err := s.db.QueryRow(`SELECT 1 FROM processed_images WHERE image_hash = ?`, hash).Scan(&x)
		if errors.Is(err, sql.ErrNoRows) {
			found = false
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	return found, err
}

func (s *store) MarkImageProcessed(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		_, err := s.db.Exec(`INSERT OR IGNORE INTO processed_images (image_hash) VALUES (?)`, hash)
		return err
	})
}

func (s *store) AddTags(filepath string, tags map[string]float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		stmt, err := tx.Prepare(`INSERT OR IGNORE INTO image_tags (filepath, tag, confidence) VALUES (?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for tag, conf := range tags {
			if _, err := stmt.Exec(filepath, tag, conf); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}

func (s *store) DeleteAllTags() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		_, err := s.db.Exec(`DELETE FROM image_tags`)
		return err
	})
}

func (s *store) ClearProcessedImages() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		_, err := s.db.Exec(`DELETE FROM processed_images`)
		return err
	})
}

func (s *store) GetAllTaggedFilepaths() (map[string]struct{}, error) {
	result := make(map[string]struct{})
	err := withSQLiteRetry(func() error {
		rows, err := s.db.Query(`SELECT DISTINCT filepath FROM image_tags`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				return err
			}
			result[p] = struct{}{}
		}
		return rows.Err()
	})
	return result, err
}

func (s *store) GetAllProcessedHashes() ([]string, error) {
	items := make([]string, 0)
	err := withSQLiteRetry(func() error {
		rows, err := s.db.Query(`SELECT image_hash FROM processed_images`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err != nil {
				return err
			}
			items = append(items, h)
		}
		return rows.Err()
	})
	return items, err
}

func (s *store) DeleteProcessedHashes(hashes []string) (int, error) {
	if len(hashes) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	totalDeleted := 0
	const chunkSize = 500
	for start := 0; start < len(hashes); start += chunkSize {
		end := start + chunkSize
		if end > len(hashes) {
			end = len(hashes)
		}
		chunk := hashes[start:end]

		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		query := fmt.Sprintf("DELETE FROM processed_images WHERE image_hash IN (%s)", placeholders)
		args := make([]any, 0, len(chunk))
		for _, h := range chunk {
			args = append(args, h)
		}

		var deleted int64
		err := withSQLiteRetry(func() error {
			res, err := s.db.Exec(query, args...)
			if err != nil {
				return err
			}
			deleted, _ = res.RowsAffected()
			return nil
		})
		if err != nil {
			return totalDeleted, err
		}
		totalDeleted += int(deleted)
	}
	return totalDeleted, nil
}

func (s *store) GetTagsForFiles(filepaths []string) (map[string][]imageTag, error) {
	result := make(map[string][]imageTag, len(filepaths))
	for _, p := range filepaths {
		result[p] = []imageTag{}
	}
	if len(filepaths) == 0 {
		return result, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(filepaths)), ",")
	query := fmt.Sprintf(
		"SELECT filepath, tag, confidence FROM image_tags WHERE filepath IN (%s) ORDER BY confidence DESC",
		placeholders,
	)
	args := make([]any, 0, len(filepaths))
	for _, p := range filepaths {
		args = append(args, p)
	}
	err := withSQLiteRetry(func() error {
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var filepathVal string
			var tag string
			var confidence float64
			if err := rows.Scan(&filepathVal, &tag, &confidence); err != nil {
				return err
			}
			result[filepathVal] = append(result[filepathVal], imageTag{Tag: tag, Confidence: confidence})
		}
		return rows.Err()
	})
	return result, err
}

func (s *store) GetAllTags() ([]map[string]any, error) {
	items := make([]map[string]any, 0)
	err := withSQLiteRetry(func() error {
		rows, err := s.db.Query(`
			SELECT tag, COUNT(id) as tag_count
			FROM image_tags
			GROUP BY tag
			ORDER BY tag_count DESC, tag ASC
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var tag string
			var count int
			if err := rows.Scan(&tag, &count); err != nil {
				return err
			}
			items = append(items, map[string]any{"tag": tag, "count": count})
		}
		return rows.Err()
	})
	return items, err
}

func (s *store) FindFilesByTagPatterns(tags []string) ([]string, error) {
	if len(tags) == 0 {
		return []string{}, nil
	}
	query := "SELECT filepath FROM image_tags WHERE LOWER(tag) LIKE ?"
	for i := 1; i < len(tags); i++ {
		query += " INTERSECT SELECT filepath FROM image_tags WHERE LOWER(tag) LIKE ?"
	}
	args := make([]any, 0, len(tags))
	for _, tag := range tags {
		args = append(args, "%"+strings.ToLower(strings.TrimSpace(tag))+"%")
	}
	items := make([]string, 0)
	err := withSQLiteRetry(func() error {
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var filepathVal string
			if err := rows.Scan(&filepathVal); err != nil {
				return err
			}
			items = append(items, filepathVal)
		}
		return rows.Err()
	})
	return items, err
}

func (s *store) DeleteTag(tag string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var affected int64
	err := withSQLiteRetry(func() error {
		result, err := s.db.Exec(`DELETE FROM image_tags WHERE tag = ?`, tag)
		if err != nil {
			return err
		}
		affected, _ = result.RowsAffected()
		return nil
	})
	return int(affected), err
}

func (s *store) DeleteTagsForFile(filepathVal string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		_, err := s.db.Exec(`DELETE FROM image_tags WHERE filepath = ?`, filepathVal)
		return err
	})
}

func (s *store) DeleteTagsForUser(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		_, err := s.db.Exec(`DELETE FROM image_tags WHERE filepath LIKE ?`, username+"/%")
		return err
	})
}

func (s *store) MoveTagsPath(oldPath, newPath string) error {
	if oldPath == "" || newPath == "" || oldPath == newPath {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO image_tags(filepath, tag, confidence)
			SELECT ?, tag, confidence FROM image_tags WHERE filepath = ?
		`, newPath, oldPath); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM image_tags WHERE filepath = ?`, oldPath); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func setTaskState(ctx context.Context, rdb *redis.Client, taskID, status string, result interface{}) {
	rec := queueTaskStatus{Status: status, Result: result, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(rec)
	if err := rdb.Set(ctx, taskMetaPrefix+taskID, b, 7*24*time.Hour).Err(); err != nil {
		logger.Error("failed to persist task state", "task_id", taskID, "status", status, "error", err)
	}

	msg := ""
	if resultMap, ok := result.(map[string]any); ok {
		if s, ok := stringFromAny(resultMap["message"]); ok && s != "" {
			msg = s
		} else if s, ok := stringFromAny(resultMap["status"]); ok && s != "" {
			msg = s
		}
	}
	attrs := []any{"task_id", taskID, "status", status}
	if msg != "" {
		attrs = append(attrs, "message", msg)
	}
	switch status {
	case "FAILURE":
		logger.Error("task state updated", attrs...)
	case "PROGRESS":
		logger.Debug("task state updated", attrs...)
	default:
		logger.Info("task state updated", attrs...)
	}
}

func getTaskState(ctx context.Context, rdb *redis.Client, taskID string) (queueTaskStatus, bool) {
	raw, err := rdb.Get(ctx, taskMetaPrefix+taskID).Result()
	if err != nil || raw == "" {
		return queueTaskStatus{}, false
	}
	var rec queueTaskStatus
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return queueTaskStatus{}, false
	}
	return rec, true
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func parsePositiveInt(raw string, fallback int) int {
	val := strings.TrimSpace(raw)
	if val == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(val, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

func parseNonNegativeInt(raw string, fallback int) int {
	val := strings.TrimSpace(raw)
	if val == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(val, "%d", &n); err != nil || n < 0 {
		return fallback
	}
	return n
}

func parseBoolParam(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func pageBounds(offset, perPage, total int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + perPage
	if end > total {
		end = total
	}
	return offset, end
}

func totalPages(totalItems, perPage int) int {
	if totalItems <= 0 {
		return 0
	}
	return (totalItems + perPage - 1) / perPage
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		v := strings.TrimSpace(part)
		if v != "" {
			items = append(items, v)
		}
	}
	return items
}

func hasTagPattern(tags []imageTag, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	for _, t := range tags {
		tagName := strings.ToLower(strings.TrimSpace(t.Tag))
		if tagName == "" {
			continue
		}
		for _, pattern := range patterns {
			p := strings.ToLower(strings.TrimSpace(pattern))
			if p == "" {
				continue
			}
			if strings.Contains(tagName, p) {
				return true
			}
		}
	}
	return false
}

func resolvePathUnderRoot(root, rel string) (string, error) {
	cleanRel := filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if cleanRel == "." || cleanRel == "" || cleanRel == "/" {
		return "", errors.New("invalid path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(filepath.Join(absRoot, cleanRel))
	if err != nil {
		return "", err
	}
	if absPath != absRoot && !strings.HasPrefix(absPath, absRoot+string(os.PathSeparator)) {
		return "", errors.New("path traversal")
	}
	return absPath, nil
}

func countImages(root string) int {
	count := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if isImageFile(d.Name()) {
			count++
		}
		return nil
	})
	return count
}

func cleanupEmptyParents(startFilePath, uploadRoot string) error {
	absRoot, err := filepath.Abs(uploadRoot)
	if err != nil {
		return err
	}
	current := filepath.Dir(startFilePath)
	for {
		absCurrent, err := filepath.Abs(current)
		if err != nil {
			return err
		}
		if absCurrent == absRoot || !strings.HasPrefix(absCurrent, absRoot+string(os.PathSeparator)) {
			return nil
		}
		entries, err := os.ReadDir(absCurrent)
		if err != nil || len(entries) > 0 {
			return nil
		}
		if err := os.Remove(absCurrent); err != nil {
			return nil
		}
		current = filepath.Dir(absCurrent)
	}
}

func toMap(v interface{}) map[string]any {
	b, _ := json.Marshal(v)
	m := make(map[string]any)
	_ = json.Unmarshal(b, &m)
	return m
}

func extractUsername(tweetURL string) string {
	re := regexp.MustCompile(`(?:x|twitter)\.com/([^/]+)/status/`)
	m := re.FindStringSubmatch(tweetURL)
	if len(m) > 1 {
		return m[1]
	}
	return "unknown_user"
}

var tweetIDFilenameRe = regexp.MustCompile(`^(\d+)_\d+`)

func tweetIDFromFilename(name string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	m := tweetIDFilenameRe.FindStringSubmatch(base)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func collectUserTweetIDs(userPath string) (map[string]struct{}, error) {
	entries, err := os.ReadDir(userPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	tweetIDs := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() {
			tweetIDs[entry.Name()] = struct{}{}
			continue
		}
		if !isImageFile(entry.Name()) {
			continue
		}
		tweetID := tweetIDFromFilename(entry.Name())
		if tweetID == "" {
			continue
		}
		tweetIDs[tweetID] = struct{}{}
	}
	return tweetIDs, nil
}

func getTweetImages(tweetURL string) ([]string, error) {
	tweetID := tweetIDFromURL(tweetURL)
	if tweetID == "" {
		return nil, errors.New("invalid tweet id")
	}
	apiURL := fmt.Sprintf("https://cdn.syndication.twimg.com/tweet-result?id=%s&token=4", tweetID)
	req, _ := http.NewRequest(http.MethodGet, apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tweet api status=%d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Photos []struct {
			URL string `json:"url"`
		} `json:"photos"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	uniq := make(map[string]struct{})
	for _, p := range parsed.Photos {
		if p.URL == "" {
			continue
		}
		u := regexp.MustCompile(`:\\w+$`).ReplaceAllString(p.URL, ":orig")
		uniq[u] = struct{}{}
	}
	images := make([]string, 0, len(uniq))
	for u := range uniq {
		images = append(images, u)
	}
	sort.Strings(images)
	return images, nil
}

func listImageFiles(root string) ([]string, error) {
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if isImageFile(d.Name()) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func isImageFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".webp") || strings.HasSuffix(lower, ".gif")
}

func fileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func extFromContentType(contentType string) string {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "webp"):
		return ".webp"
	case strings.Contains(ct, "gif"):
		return ".gif"
	default:
		return ".jpg"
	}
}

func normalizeRelPath(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}

func tweetIDFromURL(tweetURL string) string {
	parts := strings.Split(strings.TrimSpace(tweetURL), "/")
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	if strings.Contains(last, "?") {
		last = strings.SplitN(last, "?", 2)[0]
	}
	return last
}

func isTweetURL(url string) bool {
	return (strings.Contains(url, "x.com") || strings.Contains(url, "twitter.com")) && strings.Contains(url, "/status/")
}

func uniqueReverse(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for i := len(values) - 1; i >= 0; i-- {
		v := strings.TrimSpace(values[i])
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

func intFromAny(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case float32:
		return int(t), true
	case int:
		return t, true
	case int64:
		return int(t), true
	case int32:
		return int(t), true
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func stringFromAny(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func envOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func envInt(key string, fallback int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	var n int
	_, err := fmt.Sscanf(val, "%d", &n)
	if err != nil {
		return fallback
	}
	return n
}
