package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

func main() {
	mode := flag.String("mode", "all", "run mode: all|api|worker")
	flag.Parse()

	cfg := loadConfig()
	st, err := newAppState(cfg)
	if err != nil {
		logger.Error("failed to initialize app state", "error", err)
		os.Exit(1)
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
		logger.Error("unknown run mode", "mode", *mode)
		os.Exit(1)
	}
}

func loadConfig() config {
	return config{
		redisAddr:        envOrDefault("REDIS_ADDR", "redis:6379"),
		redisPassword:    os.Getenv("REDIS_PASSWORD"),
		redisDB:          envInt("REDIS_DB", 0),
		queueName:        envOrDefault("ASYNQ_QUEUE", "default"),
		interactiveQueue: envOrDefault("ASYNQ_INTERACTIVE_QUEUE", "interactive"),
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
	mux.HandleFunc("/api/autotag/reconcile", st.handleReconcileDB)
	mux.HandleFunc("/api/autotag/status", st.handleAutotagStatus)
	mux.HandleFunc("/api/autotag/retag-status", st.handleRetagStatus)
	mux.HandleFunc("/api/tags", st.handleTags)
	mux.HandleFunc("/api/users", st.handleUsers)
	mux.HandleFunc("/api/users/", st.handleUsersSubroutes)
	mux.HandleFunc("/api/images", st.handleImages)
	mux.HandleFunc("/api/images/bulk-delete", st.handleImagesBulkDelete)
	mux.HandleFunc("/api/images/retag", st.handleImagesRetag)
	mux.HandleFunc("/api/images/retag/bulk", st.handleImagesRetagBulk)
	mux.HandleFunc("/api/tasks/status", st.handleTaskStatus)

	logger.Info("queue api listening", "addr", st.cfg.apiAddr)
	if err := http.ListenAndServe(st.cfg.apiAddr, loggingMiddleware(mux)); err != nil {
		logger.Error("api server stopped", "error", err)
		os.Exit(1)
	}
}

func runWorker(st *appState) {
	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: st.cfg.redisAddr, Password: st.cfg.redisPassword, DB: st.cfg.redisDB},
		asynq.Config{
			Concurrency: st.cfg.concurrency,
			Queues: map[string]int{
				st.cfg.interactiveQueue: 4,
				st.cfg.queueName:        1,
			},
		},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(taskTypeDownload, st.processDownloadTask)
	mux.HandleFunc(taskTypeAutotagAll, st.processAutotagAllTask)
	mux.HandleFunc(taskTypeAutotagUntagged, st.processAutotagUntaggedTask)
	mux.HandleFunc(taskTypeReconcileDB, st.processReconcileDBTask)
	mux.HandleFunc(taskTypeDeleteUser, st.processDeleteUserTask)
	mux.HandleFunc(taskTypeDeleteImage, st.processDeleteImageTask)
	mux.HandleFunc(taskTypeDeleteImages, st.processDeleteImagesTask)
	mux.HandleFunc(taskTypeRetagImage, st.processRetagImageTask)
	mux.HandleFunc(taskTypeRetagImages, st.processRetagImagesTask)

	logger.Info("queue worker started",
		"queue", st.cfg.queueName,
		"interactive_queue", st.cfg.interactiveQueue,
		"concurrency", st.cfg.concurrency,
	)
	if err := srv.Run(mux); err != nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}
