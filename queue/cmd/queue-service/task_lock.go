package main

import (
	"context"
	"strings"
)

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
