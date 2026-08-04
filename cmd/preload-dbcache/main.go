package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/Alliance-Sky/proxy-api/internal/api"
	"github.com/Alliance-Sky/proxy-api/internal/cache"
	"github.com/Alliance-Sky/proxy-api/internal/db"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("Starting preload-dbcache script...")
	start := time.Now()
	ctx := context.Background()

	cacheSvc, err := cache.NewService()
	if err != nil {
		slog.Error("Failed to init cache", "error", err)
		os.Exit(1)
	}
	
	// We restore from file first so we don't wipe out the MovesetCache entries
	if err := cacheSvc.RestoreFromFile("cache-backup.bin"); err != nil {
		slog.Warn("Could not restore existing cache, starting fresh", "error", err)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://ubuntu@/smogon_stats?host=/var/run/postgresql"
	}
	dbSvc, err := db.NewService(dsn)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbSvc.Close()

	h := api.NewHandler(cacheSvc, dbSvc, os.Getenv("ADMIN_TOKEN"), "https://www.smogon.com/stats")

	months, err := dbSvc.GetMonths(ctx)
	if err != nil {
		slog.Error("Failed to fetch months", "error", err)
		os.Exit(1)
	}

	slog.Info("Preloading init and months endpoints...")
	req, _ := http.NewRequestWithContext(ctx, "GET", "/api/v3/init", nil)
	h.GetInit(httptest.NewRecorder(), req)

	req, _ = http.NewRequestWithContext(ctx, "GET", "/api/v3/months", nil)
	h.GetMonths(httptest.NewRecorder(), req)

	var totalTasks int32
	var completedTasks int32

	type task struct {
		month  string
		format string
		rating string
	}
	var tasks []task

	slog.Info("Collecting all month/format/rating combinations...")
	for _, month := range months {
		req, _ := http.NewRequestWithContext(ctx, "GET", "/api/v3/formats?month="+month, nil)
		h.GetFormats(httptest.NewRecorder(), req)

		formatsData, err := dbSvc.GetFormatsByMonth(ctx, month)
		if err != nil {
			continue
		}
		for _, f := range formatsData {
			tasks = append(tasks, task{month: month, format: f.Format, rating: f.Rating})
		}
	}

	totalTasks = int32(len(tasks))
	slog.Info("Preloading tasks generated", "total", totalTasks)

	concurrency := runtime.NumCPU()
	taskCh := make(chan task, len(tasks))
	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)

	workerDone := make(chan struct{})
	for i := 0; i < concurrency; i++ {
		go func() {
			for t := range taskCh {
				startTask := time.Now()

				endpoints := []struct {
					path    string
					handler func(http.ResponseWriter, *http.Request)
				}{
					{"/api/v3/usage", h.GetUsage},

					{"/api/v3/metagame", h.GetMetagame},
					{"/api/v3/format-stats", h.GetFormatStats},
					{"/api/v3/stats", h.GetAggregatedStatsTuple},
				}

				for _, ep := range endpoints {
					urlStr := ep.path + "?month=" + t.month + "&format=" + t.format + "&rating=" + t.rating
					req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
					ep.handler(httptest.NewRecorder(), req)
				}

				// Throttle CPU usage to ~75% max
				elapsed := time.Since(startTask)
				time.Sleep(elapsed / 3)

				c := atomic.AddInt32(&completedTasks, 1)
				if c%1000 == 0 {
					slog.Info("DBCache Preload progress", "completed", c, "total", totalTasks)
				}
			}
			workerDone <- struct{}{}
		}()
	}

	for i := 0; i < concurrency; i++ {
		<-workerDone
	}

	slog.Info("Saving cache to cache-backup.bin...")
	if err := cacheSvc.BackupToFile("cache-backup.bin"); err != nil {
		slog.Error("Failed to save cache backup", "error", err)
		os.Exit(1)
	}

	slog.Info("DBCache preload script completed successfully", "duration", time.Since(start))
}
