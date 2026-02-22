package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
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
	if !decodeJSONOrBadRequest(w, r, &body, "URL list is required") {
		return
	}
	if len(body.URLs) == 0 {
		badRequest(w, "URL list is required")
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
		err := st.enqueueTask(taskTypeDownload, st.cfg.queueName, taskID, payload, 30*time.Minute)
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

func (st *appState) selectAutotagStatusTaskID(ctx context.Context, preferredTaskID string) string {
	ordered := make([]string, 0, 32)
	seen := make(map[string]struct{}, 32)
	appendUnique := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}

	appendUnique(preferredTaskID)
	if recent, err := st.redis.LRange(ctx, taskListKey, -30, -1).Result(); err == nil {
		for _, id := range uniqueReverse(recent) {
			appendUnique(id)
		}
	}
	if len(ordered) == 0 {
		return ""
	}

	bestPending := ""
	bestSuccess := ""
	bestFailure := ""
	for _, id := range ordered {
		rec, ok := getTaskState(ctx, st.redis, id)
		if !ok {
			if id == preferredTaskID && bestPending == "" {
				bestPending = id
			}
			continue
		}
		switch rec.Status {
		case "PROGRESS":
			return id
		case "PENDING":
			if bestPending == "" {
				bestPending = id
			}
		case "SUCCESS":
			if bestSuccess == "" {
				bestSuccess = id
			}
		case "FAILURE":
			if bestFailure == "" {
				bestFailure = id
			}
		}
	}

	if bestPending != "" {
		return bestPending
	}
	if bestSuccess != "" {
		return bestSuccess
	}
	if bestFailure != "" {
		return bestFailure
	}
	return preferredTaskID
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
	err := st.enqueueTask(taskType, st.cfg.queueName, taskID, payload, 12*time.Hour)
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
	manualTaskID, _ := st.redis.Get(ctx, autotagLastTask).Result()
	manualTaskID = strings.TrimSpace(manualTaskID)
	manualRec, manualOK := getTaskState(ctx, st.redis, manualTaskID)
	downloadRec, downloadOK := getDownloadAutotagState(ctx, st.redis)

	// Keep explicit manual autotag task behavior (Tag Untagged Images / Reload / Reconcile).
	if manualOK && (manualRec.Status == "PENDING" || manualRec.Status == "PROGRESS") {
		resultMap, _ := manualRec.Result.(map[string]any)
		resp := map[string]any{
			"state":   manualRec.Status,
			"status":  pickFirstNonEmpty(resultMap, "Processing...", "status", "message"),
			"task_id": manualTaskID,
		}
		addProgressFields(resp, resultMap)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Then fall back to download-triggered autotag status.
	if downloadOK {
		resultMap, _ := downloadRec.Result.(map[string]any)
		resp := map[string]any{
			"state":  downloadRec.Status,
			"status": pickFirstNonEmpty(resultMap, "Processing...", "status", "message"),
			"source": "download",
		}
		if taskID, ok := stringFromAny(resultMap["task_id"]); ok && taskID != "" {
			resp["task_id"] = taskID
		}
		addProgressFields(resp, resultMap)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	if manualOK {
		resultMap, _ := manualRec.Result.(map[string]any)
		resp := map[string]any{
			"state":   manualRec.Status,
			"status":  pickFirstNonEmpty(resultMap, "Processing...", "status", "message"),
			"task_id": manualTaskID,
		}
		addProgressFields(resp, resultMap)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	if manualTaskID != "" {
		writeJSON(w, http.StatusOK, map[string]any{"state": "PENDING", "status": "Task is pending...", "task_id": manualTaskID})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"state": "NOT_FOUND", "status": "No autotagging task has been run yet."})
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
	resp := map[string]any{
		"state":   rec.Status,
		"status":  pickFirstNonEmpty(resultMap, "Processing...", "message", "status"),
		"task_id": taskID,
	}
	addProgressFields(resp, resultMap)
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
	message := pickFirstNonEmpty(resultMap, "Running", "message", "status")
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id": taskID,
		"state":   rec.Status,
		"message": message,
		"result":  resultMap,
	})
}
