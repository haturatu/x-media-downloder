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
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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

func main() {
	mode := flag.String("mode", "all", "run mode: all|api|worker")
	flag.Parse()

	cfg := loadConfig()
	st, err := newAppState(cfg)
	if err != nil {
		log.Fatalf("failed to initialize: %v", err)
	}
	defer st.redis.Close()
	defer st.asynqCli.Close()
	defer st.store.Close()
	defer st.inspector.Close()

	switch *mode {
	case "api":
		runAPI(st)
	case "worker":
		runWorker(st)
	case "all":
		go runWorker(st)
		runAPI(st)
	default:
		log.Fatalf("unknown mode: %s", *mode)
	}
}

func loadConfig() config {
	return config{
		redisAddr:        envOrDefault("REDIS_ADDR", "redis:6379"),
		redisPassword:    os.Getenv("REDIS_PASSWORD"),
		redisDB:          envInt("REDIS_DB", 0),
		queueName:        envOrDefault("ASYNQ_QUEUE", "default"),
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
	mux.HandleFunc("/api/autotag/status", st.handleAutotagStatus)

	log.Printf("queue api listening on %s", st.cfg.apiAddr)
	if err := http.ListenAndServe(st.cfg.apiAddr, mux); err != nil {
		log.Fatalf("api server stopped: %v", err)
	}
}

func runWorker(st *appState) {
	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: st.cfg.redisAddr, Password: st.cfg.redisPassword, DB: st.cfg.redisDB},
		asynq.Config{Concurrency: st.cfg.concurrency, Queues: map[string]int{st.cfg.queueName: 1}},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(taskTypeDownload, st.processDownloadTask)
	mux.HandleFunc(taskTypeAutotagAll, st.processAutotagAllTask)
	mux.HandleFunc(taskTypeAutotagUntagged, st.processAutotagUntaggedTask)

	log.Printf("queue worker started queue=%s concurrency=%d", st.cfg.queueName, st.cfg.concurrency)
	if err := srv.Run(mux); err != nil {
		log.Fatalf("worker stopped: %v", err)
	}
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
			log.Printf("enqueue download failed: %v", err)
			continue
		}

		setTaskState(ctx, st.redis, taskID, "PENDING", map[string]any{"status": "Queued"})
		st.redis.RPush(ctx, taskListKey, taskID)
		st.redis.HSet(ctx, taskURLHashKey, taskID, url)
		count++
		queued = append(queued, map[string]string{"task_id": taskID, "url": url})
	}

	st.redis.LTrim(ctx, taskListKey, -maxTrackedTasks, -1)
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
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "failed to queue task"})
		return
	}
	ctx := r.Context()
	st.redis.Set(ctx, autotagLastTask, taskID, 7*24*time.Hour)
	setTaskState(ctx, st.redis, taskID, "PENDING", map[string]any{"status": "Task is pending..."})
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
	tweetDir := filepath.Join(st.cfg.mediaRoot, username, tweetID)
	if err := os.MkdirAll(tweetDir, 0o755); err != nil {
		return "failed"
	}
	filename := fmt.Sprintf("%s_%02d%s", tweetID, index, ext)
	fullPath := filepath.Join(tweetDir, filename)
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
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE;`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return nil, err
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

func (s *store) Close() error {
	return s.db.Close()
}

func (s *store) IsImageProcessed(hash string) (bool, error) {
	var x int
	err := s.db.QueryRow(`SELECT 1 FROM processed_images WHERE image_hash = ?`, hash).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *store) MarkImageProcessed(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT OR IGNORE INTO processed_images (image_hash) VALUES (?)`, hash)
	return err
}

func (s *store) AddTags(filepath string, tags map[string]float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
}

func (s *store) DeleteAllTags() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM image_tags`)
	return err
}

func (s *store) ClearProcessedImages() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM processed_images`)
	return err
}

func (s *store) GetAllTaggedFilepaths() (map[string]struct{}, error) {
	rows, err := s.db.Query(`SELECT DISTINCT filepath FROM image_tags`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		result[p] = struct{}{}
	}
	return result, rows.Err()
}

func setTaskState(ctx context.Context, rdb *redis.Client, taskID, status string, result interface{}) {
	rec := queueTaskStatus{Status: status, Result: result, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(rec)
	rdb.Set(ctx, taskMetaPrefix+taskID, b, 7*24*time.Hour)
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
