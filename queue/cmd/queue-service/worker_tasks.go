package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

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
	rel := normalizeFilepath(payload.Filepath)
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

	filepaths := normalizeUniqueFilepaths(payload.Filepaths)
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
	rel := normalizeFilepath(payload.Filepath)
	if rel == "" {
		err := errors.New("filepath is required")
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}
	setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{"message": "Retagging image...", "current": 0, "total": 1})
	result, err := st.retagSingleFile(rel, false)
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}
	updated, err := st.store.GetTagsForFiles([]string{rel})
	if err != nil {
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}
	msg := "Tags generated successfully!"
	if result == "skipped" {
		msg = "Image already has tags."
	}
	setTaskState(ctx, st.redis, taskID, "SUCCESS", map[string]any{
		"success": true,
		"message": msg,
		"tags":    updated[rel],
	})
	return nil
}

func (st *appState) processRetagImagesTask(ctx context.Context, t *asynq.Task) error {
	var payload retagImagesTaskPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return err
	}
	taskID := payload.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}

	filepaths := normalizeUniqueFilepaths(payload.Filepaths)
	if len(filepaths) == 0 {
		err := errors.New("filepaths is required")
		setTaskState(ctx, st.redis, taskID, "FAILURE", map[string]any{"message": err.Error()})
		return err
	}

	total := len(filepaths)
	success := 0
	skipped := 0
	failed := 0
	setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{
		"current": 0,
		"total":   total,
		"status":  "Retagging images...",
	})

	for i, rel := range filepaths {
		result, err := st.retagSingleFile(rel, true)
		if err != nil {
			failed++
		} else if result == "skipped" {
			skipped++
		} else {
			success++
		}

		if i%20 == 0 || i == total-1 {
			setTaskState(ctx, st.redis, taskID, "PROGRESS", map[string]any{
				"current": i + 1,
				"total":   total,
				"status":  fmt.Sprintf("retagged:%d skipped:%d failed:%d", success, skipped, failed),
			})
		}
	}

	result := map[string]any{
		"success":        true,
		"message":        fmt.Sprintf("Bulk retag (force) completed. retagged:%d skipped:%d failed:%d", success, skipped, failed),
		"retagged_count": success,
		"skipped_count":  skipped,
		"failed_count":   failed,
		"total":          total,
		"current":        total,
		"status":         fmt.Sprintf("force retagged:%d skipped:%d failed:%d", success, skipped, failed),
		"force":          true,
	}
	if success == 0 && failed > 0 {
		setTaskState(ctx, st.redis, taskID, "FAILURE", result)
		return errors.New("bulk retag failed")
	}
	setTaskState(ctx, st.redis, taskID, "SUCCESS", result)
	return nil
}

// retagSingleFile returns "success" when tags were generated and "skipped" when existing tags were kept.
// When force is true, existing tags are removed and regenerated.
func (st *appState) retagSingleFile(rel string, force bool) (string, error) {
	existing, err := st.store.GetTagsForFiles([]string{rel})
	if err != nil {
		return "", err
	}
	hasExisting := len(existing[rel]) > 0
	if hasExisting && !force {
		return "skipped", nil
	}
	if hasExisting && force {
		if err := st.store.DeleteTagsForFile(rel); err != nil {
			return "", err
		}
	}

	full, err := resolvePathUnderRoot(st.cfg.mediaRoot, rel)
	if err != nil {
		return "", errors.New("invalid filepath")
	}
	if _, err := os.Stat(full); err != nil {
		return "", errors.New("file not found")
	}

	hash, err := fileMD5(full)
	if err != nil {
		return "", errors.New("could not read file")
	}
	_ = st.autotagFile(full, rel, hash)
	_ = st.store.MarkImageProcessed(hash)
	return "success", nil
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
