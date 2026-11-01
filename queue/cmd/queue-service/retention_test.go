package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"testing"
	"time"
)

func TestRecentTaskMigrationAndSteadyStateBound(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		client.HSet(ctx, taskURLHashKey, fmt.Sprint(i), "url")
		client.RPush(ctx, taskListKey, fmt.Sprint(i))
	}
	if err := trackDownloadTask(ctx, client, "", ""); err != nil {
		t.Fatal(err)
	}
	for i := 1000; i < 1600; i++ {
		if err := trackDownloadTask(ctx, client, fmt.Sprint(i), "new-url"); err != nil {
			t.Fatal(err)
		}
	}
	if client.HLen(ctx, taskURLHashKey).Val() != maxTrackedTasks || client.LLen(ctx, taskListKey).Val() != maxTrackedTasks {
		t.Fatal("recent task data exceeded bound")
	}
	if client.HGet(ctx, taskURLHashKey, "1599").Val() != "new-url" {
		t.Fatal("lost newest URL")
	}
	if client.HExists(ctx, taskURLHashKey, "1").Val() {
		t.Fatal("legacy URL survived")
	}
	server.FastForward(8 * 24 * time.Hour)
	if client.Exists(ctx, taskURLHashKey, taskListKey).Val() != 0 {
		t.Fatal("idle metadata did not expire")
	}
}
func TestDownloadStatusesBatchLookupPreservesResponse(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()
	client.HSet(ctx, taskURLHashKey, "a", "https://x.com/test/status/1")
	raw, _ := json.Marshal(queueTaskStatus{Status: "SUCCESS", Result: map[string]any{"downloaded_count": 3}})
	client.Set(ctx, taskMetaPrefix+"a", raw, time.Hour)
	st := &appState{redis: client}
	before := server.CommandCount()
	items, err := st.resolveDownloadStatuses(ctx, []string{"a", "missing"})
	if got := server.CommandCount() - before; got != 2 {
		t.Fatalf("status lookup used %d commands, want HMGET + MGET", got)
	}
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	if items[0].State != "SUCCESS" || items[0].DownloadedCount == nil || *items[0].DownloadedCount != 3 || items[0].URL == nil {
		t.Fatal(items[0])
	}
	if items[1].State != "PENDING" {
		t.Fatal(items[1])
	}
	server.Close()
	if _, err = st.resolveDownloadStatuses(ctx, []string{"a"}); err == nil {
		t.Fatal("Redis failure silently treated as pending")
	}
}
