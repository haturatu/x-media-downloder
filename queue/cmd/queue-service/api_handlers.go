package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

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
	ctx := r.Context()
	if st.isTrackedTaskBusy(ctx, autotagLastTask) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"success": false,
			"message": "Another autotag task is already running.",
		})
		return
	}

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

func (st *appState) handleRetagStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	taskID, err := st.redis.Get(ctx, retagLastTask).Result()
	if err != nil || taskID == "" {
		writeJSON(w, http.StatusOK, map[string]any{"state": "NOT_FOUND", "status": "No bulk retag task has been run yet.", "task_id": ""})
		return
	}
	rec, ok := getTaskState(ctx, st.redis, taskID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"state": "PENDING", "status": "Task is pending...", "task_id": taskID})
		return
	}

	resultMap, _ := rec.Result.(map[string]any)
	resp := map[string]any{"state": rec.Status, "status": "Processing...", "task_id": taskID}
	if s, ok := stringFromAny(resultMap["status"]); ok {
		resp["status"] = s
	}
	if s, ok := stringFromAny(resultMap["message"]); ok && s != "" {
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

func (st *appState) handleImagesRetagBulk(w http.ResponseWriter, r *http.Request) {
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

	ctx := r.Context()
	if st.isTrackedTaskBusy(ctx, retagLastTask) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"success": false,
			"message": "Another bulk retag task is already running.",
		})
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
	payload := retagImagesTaskPayload{TaskID: taskID, Filepaths: filepaths}
	b, _ := json.Marshal(payload)
	task := asynq.NewTask(taskTypeRetagImages, b)
	_, err := st.asynqCli.Enqueue(task,
		asynq.Queue(st.cfg.interactiveQueue),
		asynq.TaskID(taskID),
		asynq.MaxRetry(0),
		asynq.Timeout(30*time.Minute),
	)
	if err != nil {
		logger.Error("failed to enqueue bulk retag task",
			"task_type", taskTypeRetagImages,
			"task_id", taskID,
			"count", len(filepaths),
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to queue task"})
		return
	}

	st.redis.Set(ctx, retagLastTask, taskID, 7*24*time.Hour)
	setTaskState(ctx, st.redis, taskID, "PENDING", map[string]any{
		"message": "Bulk retag task queued",
		"total":   len(filepaths),
	})
	logger.Info("bulk retag task queued", "task_id", taskID, "count", len(filepaths))
	writeJSON(w, http.StatusAccepted, map[string]any{
		"success":      true,
		"queued":       true,
		"task_id":      taskID,
		"queued_count": len(filepaths),
		"message":      "Bulk retag task queued",
	})
}

func (st *appState) isTrackedTaskBusy(ctx context.Context, taskKey string) bool {
	taskID, err := st.redis.Get(ctx, taskKey).Result()
	if err != nil || strings.TrimSpace(taskID) == "" {
		return false
	}
	rec, ok := getTaskState(ctx, st.redis, taskID)
	if !ok {
		return true
	}
	return rec.Status == "PENDING" || rec.Status == "PROGRESS"
}
