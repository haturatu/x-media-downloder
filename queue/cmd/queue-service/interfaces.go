package main

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// RedisClient abstracts Redis operations used by API/task state flows.
type RedisClient interface {
	Ping(ctx context.Context) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	LRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd
	LTrim(ctx context.Context, key string, start, stop int64) *redis.StatusCmd
	RPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	HGet(ctx context.Context, key, field string) *redis.StringCmd
	HSet(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	Close() error
}

// AsynqClient abstracts task enqueue operations.
type AsynqClient interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
	Close() error
}

// QueueInspector abstracts queue info inspection.
type QueueInspector interface {
	GetQueueInfo(queue string) (*asynq.QueueInfo, error)
	Close() error
}

// TagStore abstracts persistent media/tag storage.
type TagStore interface {
	Close() error
	IsImageProcessed(hash string) (bool, error)
	MarkImageProcessed(hash string) error
	AddTags(filepath string, tags map[string]float64) error
	DeleteAllTags() error
	ClearProcessedImages() error
	GetAllTaggedFilepaths() (map[string]struct{}, error)
	GetAllProcessedHashes() ([]string, error)
	DeleteProcessedHashes(hashes []string) (int, error)
	GetTagsForFiles(filepaths []string) (map[string][]imageTag, error)
	GetAllTags() ([]map[string]any, error)
	FindFilesByTagPatterns(tags []string) ([]string, error)
	DeleteTag(tag string) (int, error)
	DeleteTagsForFile(filepathVal string) error
	DeleteTagsForUser(username string) error
}

var _ RedisClient = (*redis.Client)(nil)
var _ AsynqClient = (*asynq.Client)(nil)
var _ QueueInspector = (*asynq.Inspector)(nil)
var _ TagStore = (*store)(nil)
