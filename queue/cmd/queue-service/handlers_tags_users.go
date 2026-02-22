package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

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
		internalServerError(w)
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
	items := any(tags)
	if !allItems {
		start, end := pageBounds(offset, perPage, totalItems)
		items = tags[start:end]
	}
	writePaginatedResponse(w, items, totalItems, perPage, page, allItems, 1)
}

func (st *appState) handleTagsDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tag string `json:"tag"`
	}
	if !decodeJSONOrBadRequest(w, r, &body, "tag is required") {
		return
	}
	tag := strings.TrimSpace(body.Tag)
	if tag == "" {
		badRequest(w, "tag is required")
		return
	}
	deleted, err := st.store.DeleteTag(tag)
	if err != nil {
		internalServerError(w)
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
		internalServerError(w)
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
	items := any(users)
	if !allItems {
		start, end := pageBounds(offset, perPage, totalItems)
		items = users[start:end]
	}
	writePaginatedResponse(w, items, totalItems, perPage, page, allItems, 1)
}

func (st *appState) handleUsersDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if !decodeJSONOrBadRequest(w, r, &body, "username is required") {
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" || strings.Contains(username, "/") || strings.Contains(username, "\\") {
		badRequest(w, "Invalid username")
		return
	}
	taskID := uuid.NewString()
	payload := deleteUserTaskPayload{TaskID: taskID, Username: username}
	err := st.enqueueTask(taskTypeDeleteUser, st.cfg.interactiveQueue, taskID, payload, 10*time.Minute)
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
		internalServerError(w)
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
			internalServerError(w)
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
	items := any(tweets)
	if !returnAll {
		start, end := pageBounds(offset, perPage, totalItems)
		items = tweets[start:end]
	}
	writePaginatedResponse(w, items, totalItems, perPage, page, returnAll, 0)
}
