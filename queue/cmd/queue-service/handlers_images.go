package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

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
	if !decodeJSONOrBadRequest(w, r, &body, "filepaths is required") {
		return
	}
	filepaths := normalizeUniqueFilepaths(body.Filepaths)
	if len(filepaths) == 0 {
		badRequest(w, "filepaths is required")
		return
	}

	taskID := uuid.NewString()
	payload := deleteImagesTaskPayload{TaskID: taskID, Filepaths: filepaths}
	err := st.enqueueTask(taskTypeDeleteImages, st.cfg.interactiveQueue, taskID, payload, 30*time.Minute)
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
	if !decodeJSONOrBadRequest(w, r, &body, "filepath is required") {
		return
	}
	rel := normalizeFilepath(body.Filepath)
	if rel == "" {
		badRequest(w, "filepath is required")
		return
	}
	taskID := uuid.NewString()
	payload := deleteImageTaskPayload{TaskID: taskID, Filepath: rel}
	err := st.enqueueTask(taskTypeDeleteImage, st.cfg.interactiveQueue, taskID, payload, 5*time.Minute)
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
	if !decodeJSONOrBadRequest(w, r, &body, "filepath is required") {
		return
	}
	rel := normalizeFilepath(body.Filepath)
	if rel == "" {
		badRequest(w, "filepath is required")
		return
	}
	taskID := uuid.NewString()
	payload := retagImageTaskPayload{TaskID: taskID, Filepath: rel}
	err := st.enqueueTask(taskTypeRetagImage, st.cfg.interactiveQueue, taskID, payload, 10*time.Minute)
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
	if !decodeJSONOrBadRequest(w, r, &body, "filepaths is required") {
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

	filepaths := normalizeUniqueFilepaths(body.Filepaths)
	if len(filepaths) == 0 {
		badRequest(w, "filepaths is required")
		return
	}

	taskID := uuid.NewString()
	payload := retagImagesTaskPayload{TaskID: taskID, Filepaths: filepaths}
	err := st.enqueueTask(taskTypeRetagImages, st.cfg.interactiveQueue, taskID, payload, 30*time.Minute)
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
