package main

import (
	"context"
	"github.com/goccy/go-json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
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

func loadStats(h *api.Handler) {
	if data, err := os.ReadFile(statsFile); err == nil {
		var stats struct {
			InboundBytes  uint64 `json:"inboundBytes"`
			OutboundBytes uint64 `json:"outboundBytes"`
		}
		if err := json.Unmarshal(data, &stats); err == nil {
			atomic.StoreUint64(&h.TotalInboundBytes, stats.InboundBytes)
			atomic.StoreUint64(&h.TotalOutboundBytes, stats.OutboundBytes)
		}
	}
}

func saveStatsLoop(ctx context.Context, wg *sync.WaitGroup, h *api.Handler) {
	defer wg.Done()
	
	var lastInbound, lastOutbound uint64
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			currInbound := atomic.LoadUint64(&h.TotalInboundBytes)
			currOutbound := atomic.LoadUint64(&h.TotalOutboundBytes)
			data, _ := json.MarshalNoEscape(map[string]uint64{
				"inboundBytes":  currInbound,
				"outboundBytes": currOutbound,
			})
			os.WriteFile(statsFile, data, 0644)
			return
		case <-ticker.C:
			currInbound := atomic.LoadUint64(&h.TotalInboundBytes)
			currOutbound := atomic.LoadUint64(&h.TotalOutboundBytes)

			if currInbound != lastInbound || currOutbound != lastOutbound {
				data, _ := json.MarshalNoEscape(map[string]uint64{
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

func saveCacheLoop(ctx context.Context, cacheSvc *cache.Service, wg *sync.WaitGroup) {
	defer wg.Done()
	
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutting down, backing up cache...")
			cacheSvc.BackupToFile("cache-backup.bin")
			return
		case <-ticker.C:
			slog.Info("Running scheduled cache backup...")
			cacheSvc.BackupToFile("cache-backup.bin")
		}
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("Starting proxy-api...")
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	cacheSvc, err := cache.NewService()
	if err != nil {
		slog.Error("Failed to init cache", "error", err)
		os.Exit(1)
	}

	if err := cacheSvc.RestoreFromFile("cache-backup.bin"); err != nil {
		slog.Warn("Failed to restore cache", "error", err)
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

	loadStats(h)
	wg.Add(1)
	go saveStatsLoop(ctx, &wg, h)
	wg.Add(1)
	go saveCacheLoop(ctx, cacheSvc, &wg)

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
			isLocalhost := strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") || strings.HasPrefix(r.RemoteAddr, "[::1]:")
			isDirectConnection := r.Header.Get("X-Forwarded-For") == "" && r.Header.Get("X-Real-IP") == ""
			
			adminToken := os.Getenv("ADMIN_TOKEN")
			if (isLocalhost && isDirectConnection) || (adminToken != "" && r.Header.Get("X-Admin-Token") == adminToken) {
				next.ServeHTTP(w, r)
				return
			}
			rateLimiter(next).ServeHTTP(w, r)
		})
	})

	h.RegisterRoutes(r)

	port := os.Getenv("PORT")
	if port == "" {
		port = "9000"
	}
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}
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

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	cancel()

	slog.Info("Waiting for background tasks (cache backup) to complete...")
	waitCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		slog.Info("All background tasks finished cleanly")
	case <-time.After(45 * time.Second):
		slog.Warn("Timed out waiting for background tasks")
	}

	slog.Info("Server stopped cleanly")
}
