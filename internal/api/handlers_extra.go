package api

import (
	"bytes"
	"github.com/goccy/go-json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

var (
	cachedMonthsSlice []string
	cachedMonthsRaw   []byte
	cachedMonthsMu    sync.RWMutex
)

func (h *Handler) GetTrend(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	rating := r.URL.Query().Get("rating")
	pokemon := r.URL.Query().Get("pokemon")
	monthsStr := r.URL.Query().Get("months")

	if format == "" || rating == "" || pokemon == "" {
		http.Error(w, `{"error":"Missing parameters"}`, http.StatusBadRequest)
		return
	}

	limit := 12
	if m, err := strconv.Atoi(monthsStr); err == nil && m > 0 {
		limit = m
	}

	cacheKey := fmt.Sprintf("trend_%s_%s_%s_%d", format, rating, pokemon, limit)
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, r, cached, "public, max-age=3600, s-maxage=3600")
		return
	}

	pokemonList := strings.Split(pokemon, ",")
	for i, p := range pokemonList {
		pokemonList[i] = strings.TrimSpace(p)
	}

	var allMonths []string
	if cached, err := h.cache.DBCache.Get("months_list_raw"); err == nil {
		cachedMonthsMu.RLock()
		if len(cachedMonthsRaw) > 0 && bytes.Equal(cachedMonthsRaw, cached) {
			allMonths = cachedMonthsSlice
			cachedMonthsMu.RUnlock()
		} else {
			cachedMonthsMu.RUnlock()
			json.Unmarshal(cached, &allMonths)
			cachedMonthsMu.Lock()
			cachedMonthsRaw = append([]byte(nil), cached...)
			cachedMonthsSlice = allMonths
			cachedMonthsMu.Unlock()
		}
	} else {
		allMonths, _ = h.db.GetMonths(r.Context())
		rawBytes, _ := json.MarshalNoEscape(allMonths)
		h.cache.DBCache.Set("months_list_raw", rawBytes)
		cachedMonthsMu.Lock()
		cachedMonthsRaw = append([]byte(nil), rawBytes...)
		cachedMonthsSlice = allMonths
		cachedMonthsMu.Unlock()
	}

	if len(allMonths) > limit {
		allMonths = allMonths[:limit]
	}
	if len(allMonths) == 0 {
		w.Write([]byte("[]"))
		return
	}

	trendRows, err := h.db.GetTrend(r.Context(), format, rating, pokemonList, allMonths)
	if err != nil {
		http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		return
	}

	type MonthUsage struct {
		Month        string  `json:"month"`
		UsagePercent float64 `json:"usagePercent"`
	}

	data := make(map[string][]MonthUsage)
	for _, p := range pokemonList {
		data[p] = []MonthUsage{}
	}

	for _, t := range trendRows {
		data[t.Pokemon] = append(data[t.Pokemon], MonthUsage{
			Month:        t.Month,
			UsagePercent: t.UsagePercent,
		})
	}

	jsonData, _ := json.MarshalNoEscape(data)
	compressed, _ := compressBrotliFast(jsonData)
	h.cache.DBCache.Set(cacheKey, compressed)

	h.sendCached(w, r, compressed, "public, max-age=3600, s-maxage=3600")
}


func (h *Handler) GetMetagame(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	format := r.URL.Query().Get("format")
	rating := r.URL.Query().Get("rating")

	if month == "" || format == "" || rating == "" {
		http.Error(w, `{"error":"Missing parameters"}`, http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("metagame_%s_%s_%s", month, format, rating)
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, r, cached, "public, max-age=2592000, s-maxage=2592000, immutable")
		return
	}

	metaRow, err := h.db.GetMetagame(r.Context(), month, format, rating)

	type MetaResponse struct {
		Stalliness float64         `json:"stalliness"`
		Playstyles json.RawMessage `json:"playstyles"`
	}

	resp := MetaResponse{Stalliness: 0, Playstyles: []byte("{}")}
	if err == nil {
		resp.Stalliness = metaRow.Stalliness
		if len(metaRow.Playstyles) > 0 {
			resp.Playstyles = metaRow.Playstyles
		}
	}

	jsonData, _ := json.MarshalNoEscape(resp)
	compressed, _ := compressBrotli(jsonData)
	h.cache.DBCache.Set(cacheKey, compressed)

	h.sendCached(w, r, compressed, "public, max-age=2592000, s-maxage=2592000, immutable")
}

func (h *Handler) GetFormatStats(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	format := r.URL.Query().Get("format")
	rating := r.URL.Query().Get("rating")

	if month == "" || format == "" || rating == "" {
		http.Error(w, `{"error":"Missing parameters"}`, http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("format_stats_%s_%s_%s", month, format, rating)
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, r, cached, "public, max-age=2592000, s-maxage=2592000, immutable")
		return
	}

	totalBattles, err := h.db.GetFormatStats(r.Context(), month, format, rating)

	type FormatStatsResponse struct {
		TotalBattles int `json:"totalBattles"`
	}
	resp := FormatStatsResponse{TotalBattles: 0}
	if err == nil {
		resp.TotalBattles = totalBattles
	}

	jsonData, _ := json.MarshalNoEscape(resp)
	compressed, _ := compressBrotli(jsonData)
	h.cache.DBCache.Set(cacheKey, compressed)

	h.sendCached(w, r, compressed, "public, max-age=2592000, s-maxage=2592000, immutable")
}
