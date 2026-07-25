package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/Alliance-Sky/proxy-api/internal/cache"
	"github.com/Alliance-Sky/proxy-api/internal/db"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	cache *cache.Service
	db    *db.Service
}

func NewHandler(cache *cache.Service, db *db.Service) *Handler {
	return &Handler{
		cache: cache,
		db:    db,
	}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/months", h.GetMonths)
	r.Get("/api/formats", h.GetFormats)
	r.Get("/api/viability", h.GetViability)
	r.Get("/api/usage", h.GetUsage)
	r.Get("/api/trend", h.GetTrend)
	r.Get("/api/leads", h.GetLeads)
	r.Get("/api/metagame", h.GetMetagame)
	r.Get("/api/format-stats", h.GetFormatStats)
	r.Get("/", h.GetProxy)

	r.Post("/_internal/restore", h.RestoreCache)
	r.Post("/_internal/backup", h.BackupCache)
}

func (h *Handler) RestoreCache(w http.ResponseWriter, r *http.Request) {
	if r.RemoteAddr != "127.0.0.1" && r.RemoteAddr != "::1" {
		http.Error(w, `{"error":"Forbidden"}`, http.StatusForbidden)
		return
	}

	if err := h.cache.RestoreFromFile("cache-backup.jsonl"); err != nil {
		http.Error(w, `{"error":"Failed to restore"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true,"message":"Manual restore complete"}`))
}

func (h *Handler) BackupCache(w http.ResponseWriter, r *http.Request) {
	if r.RemoteAddr != "127.0.0.1" && r.RemoteAddr != "::1" {
		http.Error(w, `{"error":"Forbidden"}`, http.StatusForbidden)
		return
	}

	if err := h.cache.BackupToFile("cache-backup.jsonl"); err != nil {
		http.Error(w, `{"error":"Failed to backup"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true,"message":"Manual backup complete"}`))
}

func (h *Handler) sendCached(w http.ResponseWriter, data []byte, cacheControl string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Encoding", "br")
	w.Header().Set("Cache-Control", cacheControl)
	w.Write(data)
}

func (h *Handler) GetMonths(w http.ResponseWriter, r *http.Request) {
	cacheKey := "months_list"
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, cached, "public, max-age=3600, s-maxage=3600")
		return
	}

	months, err := h.db.GetMonths(r.Context())
	if err != nil {
		http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		return
	}

	jsonData, _ := json.Marshal(months)
	compressed, err := compressBrotliFast(jsonData)
	if err == nil {
		h.cache.DBCache.Set(cacheKey, compressed)
	}

	h.sendCached(w, compressed, "public, max-age=3600, s-maxage=3600")
}

func (h *Handler) GetFormats(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if month == "" {
		http.Error(w, `{"error":"Missing parameters"}`, http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("formats_%s", month)
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, cached, "public, max-age=2592000, s-maxage=2592000, immutable")
		return
	}

	formats, err := h.db.GetFormatsByMonth(r.Context(), month)
	if err != nil {
		http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		return
	}

	formatsMap := make(map[string][]string)
	for _, f := range formats {
		formatsMap[f.Format] = append(formatsMap[f.Format], f.Rating)
	}

	for format, ratings := range formatsMap {
		sort.Slice(ratings, func(i, j int) bool {
			ni, _ := strconv.Atoi(ratings[i])
			nj, _ := strconv.Atoi(ratings[j])
			return ni < nj
		})

		if len(ratings) > 2 {
			formatsMap[format] = ratings[len(ratings)-2:]
		}
	}

	jsonData, _ := json.Marshal(formatsMap)
	compressed, _ := compressBrotliFast(jsonData)
	h.cache.DBCache.Set(cacheKey, compressed)

	h.sendCached(w, compressed, "public, max-age=2592000, s-maxage=2592000, immutable")
}

func (h *Handler) GetViability(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	format := r.URL.Query().Get("format")
	rating := r.URL.Query().Get("rating")

	if month == "" || format == "" || rating == "" {
		http.Error(w, `{"error":"Missing parameters"}`, http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("viability_%s_%s_%s", month, format, rating)
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, cached, "public, max-age=2592000, s-maxage=2592000, immutable")
		return
	}

	viability, err := h.db.GetViability(r.Context(), month, format, rating)
	if err != nil {
		http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		return
	}

	dataMap := make(map[string]json.RawMessage)
	for _, v := range viability {
		dataMap[v.Pokemon] = v.Viability
	}

	jsonData, _ := json.Marshal(dataMap)
	compressed, _ := compressBrotliFast(jsonData)
	h.cache.DBCache.Set(cacheKey, compressed)

	h.sendCached(w, compressed, "public, max-age=2592000, s-maxage=2592000, immutable")
}

func (h *Handler) GetUsage(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	format := r.URL.Query().Get("format")
	rating := r.URL.Query().Get("rating")

	if month == "" || format == "" || rating == "" {
		http.Error(w, `{"error":"Missing parameters"}`, http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("usage_%s_%s_%s", month, format, rating)
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, cached, "public, max-age=2592000, s-maxage=2592000, immutable")
		return
	}

	usage, err := h.db.GetUsage(r.Context(), month, format, rating)
	if err != nil {
		http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		return
	}

	type UsageResponse struct {
		Rank         int    `json:"rank"`
		Pokemon      string `json:"pokemon"`
		UsagePercent string `json:"usagePercent"`
	}

	data := make([]UsageResponse, 0)
	for i, u := range usage {
		data = append(data, UsageResponse{
			Rank:         i + 1,
			Pokemon:      u.Pokemon,
			UsagePercent: fmt.Sprintf("%.5f%%", u.UsagePercent),
		})
	}

	jsonData, _ := json.Marshal(data)
	compressed, _ := compressBrotliFast(jsonData)
	h.cache.DBCache.Set(cacheKey, compressed)

	h.sendCached(w, compressed, "public, max-age=2592000, s-maxage=2592000, immutable")
}
