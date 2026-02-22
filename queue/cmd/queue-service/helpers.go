package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

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
