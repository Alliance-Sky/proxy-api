package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Alliance-Sky/proxy-api/internal/api"
	"github.com/Alliance-Sky/proxy-api/internal/cache"
	"github.com/Alliance-Sky/proxy-api/internal/db"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
)

const statsFile = "proxy-api-stats.db"

func loadStats() {
	data, err := os.ReadFile(statsFile)
	if err == nil {
		var stats struct {
			InboundBytes  uint64 `json:"inboundBytes"`
			OutboundBytes uint64 `json:"outboundBytes"`
		}
		if err := json.Unmarshal(data, &stats); err == nil {
			atomic.StoreUint64(&api.TotalInboundBytes, stats.InboundBytes)
			atomic.StoreUint64(&api.TotalOutboundBytes, stats.OutboundBytes)
		}
	}
}

func saveStatsLoop(ctx context.Context) {
	var lastInbound, lastOutbound uint64
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			currInbound := atomic.LoadUint64(&api.TotalInboundBytes)
			currOutbound := atomic.LoadUint64(&api.TotalOutboundBytes)
			data, _ := json.Marshal(map[string]uint64{
				"inboundBytes":  currInbound,
				"outboundBytes": currOutbound,
			})
			os.WriteFile(statsFile, data, 0644)
			return
		case <-ticker.C:
			currInbound := atomic.LoadUint64(&api.TotalInboundBytes)
			currOutbound := atomic.LoadUint64(&api.TotalOutboundBytes)

			if currInbound != lastInbound || currOutbound != lastOutbound {
				data, _ := json.Marshal(map[string]uint64{
					"inboundBytes":  currInbound,
					"outboundBytes": currOutbound,
				})
				os.WriteFile(statsFile, data, 0644)
				lastInbound = currInbound
				lastOutbound = currOutbound
			}
		}
	}
}

func saveCacheLoop(ctx context.Context, cacheSvc *cache.Service) {
	ticker := time.NewTicker(7 * 24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutting down, backing up cache...")
			cacheSvc.BackupToFile("cache-backup.jsonl")
			return
		case <-ticker.C:
			slog.Info("Running scheduled cache backup...")
			cacheSvc.BackupToFile("cache-backup.jsonl")
		}
	}
}

func checkNewSmogonStatsLoop(ctx context.Context, cacheSvc *cache.Service) {
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()

	var lastKnownLatestMonth string
	regex := regexp.MustCompile(`<a href="(\d{4}-\d{2})/?">`)

	check := func() {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get("https://www.smogon.com/stats/")
		if err != nil {
			slog.Error("[Notifier] Error checking for new stats", "error", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return
		}

		matches := regex.FindAllStringSubmatch(string(bodyBytes), -1)
		if len(matches) > 0 {
			latestMonth := matches[len(matches)-1][1]

			if lastKnownLatestMonth == "" {
				lastKnownLatestMonth = latestMonth
			} else if latestMonth > lastKnownLatestMonth {
				slog.Info("[Notifier] New Smogon stats detected!", "previous", lastKnownLatestMonth, "new", latestMonth)
				lastKnownLatestMonth = latestMonth

				cacheSvc.DBCache.Delete("months_list")
				cacheSvc.DBCache.Delete("months_list_raw")
				slog.Info("[Notifier] months_list cache has been invalidated due to new stats.")
			}
		}
	}

	check()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("Starting proxy-api...")
	loadStats()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go saveStatsLoop(ctx)

	cacheSvc, err := cache.NewService()
	if err != nil {
		slog.Error("Failed to init cache", "error", err)
		os.Exit(1)
	}

	if err := cacheSvc.RestoreFromFile("cache-backup.jsonl"); err != nil {
		slog.Warn("Failed to restore cache", "error", err)
	}

	go saveCacheLoop(ctx, cacheSvc)
	go checkNewSmogonStatsLoop(ctx, cacheSvc)

	dsn := "user=ubuntu dbname=smogon_stats host=/var/run/postgresql pool_max_conns=20"
	dbSvc, err := db.NewService(dsn)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbSvc.Close()

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
	}))

	rateLimiter := httprate.Limit(
		200,
		1*time.Minute,
		httprate.WithKeyFuncs(httprate.KeyByIP),
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"statusCode":429,"error":"Too Many Requests","message":"Rate limit exceeded (max 200 req/min). Bulk automated scraping is disabled."}`, http.StatusTooManyRequests)
		}),
	)

	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") || strings.HasPrefix(r.RemoteAddr, "[::1]:") || r.Header.Get("X-Forwarded-For") == "127.0.0.1" {
				next.ServeHTTP(w, r)
				return
			}
			rateLimiter(next).ServeHTTP(w, r)
		})
	})

	h := api.NewHandler(cacheSvc, dbSvc)
	h.RegisterRoutes(r)

	port := ":9000"
	srv := &http.Server{
		Addr:         port,
		Handler:      r,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("Listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	slog.Info("Shutting down gracefully...")

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	slog.Info("Server stopped cleanly")
}
