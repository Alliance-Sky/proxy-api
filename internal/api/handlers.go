package api

import (

	"github.com/goccy/go-json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

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
	r.Get("/api/v3/months", h.GetMonths)
	r.Get("/api/v3/formats", h.GetFormats)
	r.Get("/api/v3/viability", h.GetViability)
	r.Get("/api/v3/usage", h.GetUsage)
	r.Get("/api/v3/trend", h.GetTrend)
	r.Get("/api/v3/leads", h.GetLeads)
	r.Get("/api/v3/metagame", h.GetMetagame)
	r.Get("/api/v3/format-stats", h.GetFormatStats)
	r.Get("/api/v3/stats", h.GetAggregatedStatsTuple)
	r.Get("/api/v3/init", h.GetInit)
	r.Get("/api/v3/details", h.GetDetails)
	r.Get("/", h.GetProxy)

	r.Post("/_internal/restore", h.RestoreCache)
	r.Post("/_internal/backup", h.BackupCache)
	r.Post("/_internal/invalidate-months", h.InvalidateMonthsCache)
}

func (h *Handler) RestoreCache(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") && !strings.HasPrefix(r.RemoteAddr, "[::1]:") && r.RemoteAddr != "127.0.0.1" && r.RemoteAddr != "::1" {
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
	if !strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") && !strings.HasPrefix(r.RemoteAddr, "[::1]:") && r.RemoteAddr != "127.0.0.1" && r.RemoteAddr != "::1" {
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

func (h *Handler) InvalidateMonthsCache(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") && !strings.HasPrefix(r.RemoteAddr, "[::1]:") && r.RemoteAddr != "127.0.0.1" && r.RemoteAddr != "::1" {
		http.Error(w, `{"error":"Forbidden"}`, http.StatusForbidden)
		return
	}

	h.cache.DBCache.Delete("months_list")
	h.cache.DBCache.Delete("months_list_raw")
	h.cache.DBCache.Delete("init_v3")

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true,"message":"Months and init caches invalidated"}`))
}

func (h *Handler) sendCached(w http.ResponseWriter, r *http.Request, data []byte, cacheControl string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", cacheControl)

	acceptEncoding := r.Header.Get("Accept-Encoding")

	if strings.Contains(acceptEncoding, "br") {
		w.Header().Set("Content-Encoding", "br")
		w.Write(data)
		return
	}

	uncompressed, err := decompressBrotli(data)
	if err != nil {
		http.Error(w, `{"error":"Internal Server Error"}`, http.StatusInternalServerError)
		return
	}

	w.Write(uncompressed)
}

func (h *Handler) GetMonths(w http.ResponseWriter, r *http.Request) {
	cacheKey := "months_list"
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, r, cached, "public, max-age=3600, s-maxage=3600")
		return
	}

	months, err := h.db.GetMonths(r.Context())
	if err != nil {
		http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		return
	}

	jsonData, _ := json.MarshalNoEscape(months)
	compressed, err := compressBrotliFast(jsonData)
	if err == nil {
		h.cache.DBCache.Set(cacheKey, compressed)
	}

	h.sendCached(w, r, compressed, "public, max-age=3600, s-maxage=3600")
}

func (h *Handler) GetFormats(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if month == "" {
		http.Error(w, `{"error":"Missing parameters"}`, http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("formats_v2_%s", month)
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, r, cached, "public, max-age=2592000, s-maxage=2592000, immutable")
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

	type FormatItem struct {
		Format   string   `json:"format"`
		Ratings  []string `json:"ratings"`
		genNum   int
		tierRank int
	}
	var formatItems []FormatItem

	tierOrder := []string{"ou", "ubers", "uu", "ru", "nu", "pu", "lc", "monotype", "doublesou", "randombattle"}
	getTierRank := func(f string) int {
		clean := f
		if len(f) > 4 && f[:3] == "gen" {
			i := 3
			for i < len(f) && f[i] >= '0' && f[i] <= '9' {
				i++
			}
			clean = f[i:]
		}
		for idx, t := range tierOrder {
			if t == clean {
				return idx
			}
		}
		return 999
	}
	getGenNum := func(f string) int {
		if len(f) > 3 && f[:3] == "gen" {
			i := 3
			for i < len(f) && f[i] >= '0' && f[i] <= '9' {
				i++
			}
			if gen, err := strconv.Atoi(f[3:i]); err == nil {
				return gen
			}
		}
		return 0
	}

	for format, ratings := range formatsMap {
		sort.Slice(ratings, func(i, j int) bool {
			ni, _ := strconv.Atoi(ratings[i])
			nj, _ := strconv.Atoi(ratings[j])
			return ni < nj
		})

		if len(ratings) > 2 {
			ratings = ratings[len(ratings)-2:]
		}
		formatItems = append(formatItems, FormatItem{
			Format:   format,
			Ratings:  ratings,
			genNum:   getGenNum(format),
			tierRank: getTierRank(format),
		})
	}

	sort.Slice(formatItems, func(i, j int) bool {
		if formatItems[i].genNum != formatItems[j].genNum {
			return formatItems[i].genNum > formatItems[j].genNum
		}
		if formatItems[i].tierRank != formatItems[j].tierRank {
			return formatItems[i].tierRank < formatItems[j].tierRank
		}
		return formatItems[i].Format < formatItems[j].Format
	})

	jsonData, _ := json.MarshalNoEscape(formatItems)
	compressed, _ := compressBrotliFast(jsonData)
	h.cache.DBCache.Set(cacheKey, compressed)

	h.sendCached(w, r, compressed, "public, max-age=2592000, s-maxage=2592000, immutable")
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
		h.sendCached(w, r, cached, "public, max-age=2592000, s-maxage=2592000, immutable")
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

	jsonData, _ := json.MarshalNoEscape(dataMap)
	compressed, _ := compressBrotliFast(jsonData)
	h.cache.DBCache.Set(cacheKey, compressed)

	h.sendCached(w, r, compressed, "public, max-age=2592000, s-maxage=2592000, immutable")
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
		h.sendCached(w, r, cached, "public, max-age=2592000, s-maxage=2592000, immutable")
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

	jsonData, _ := json.MarshalNoEscape(data)
	compressed, _ := compressBrotliFast(jsonData)
	h.cache.DBCache.Set(cacheKey, compressed)

	h.sendCached(w, r, compressed, "public, max-age=2592000, s-maxage=2592000, immutable")
}

func (h *Handler) GetAggregatedStats(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	format := r.URL.Query().Get("format")
	rating := r.URL.Query().Get("rating")

	if month == "" || format == "" || rating == "" {
		http.Error(w, `{"error":"Missing parameters"}`, http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("aggregated_stats_%s_%s_%s", month, format, rating)
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, r, cached, "public, max-age=2592000, s-maxage=2592000, immutable")
		return
	}

	usage, err := h.db.GetUsage(r.Context(), month, format, rating)
	if err != nil {
		http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		return
	}

	viability, _ := h.db.GetViability(r.Context(), month, format, rating)
	leads, _ := h.db.GetLeads(r.Context(), month, format, rating)

	viabilityMap := make(map[string]json.RawMessage)
	for _, v := range viability {
		viabilityMap[v.Pokemon] = v.Viability
	}

	leadsMap := make(map[string]string)
	for _, l := range leads {
		leadsMap[l.Pokemon] = fmt.Sprintf("%.5f%%", l.LeadPercent)
	}

	type AggregatedStatsResponse struct {
		Rank         int             `json:"rank"`
		Pokemon      string          `json:"pokemon"`
		UsagePercent string          `json:"usagePercent"`
		LeadPercent  string          `json:"leadPercent"`
		Viability    json.RawMessage `json:"viability"`
	}

	dataAgg := make([]AggregatedStatsResponse, 0, len(usage))
	for i, u := range usage {
		leadPct, exists := leadsMap[u.Pokemon]
		if !exists {
			leadPct = "0.00000%"
		}
		
		viab := viabilityMap[u.Pokemon]

		dataAgg = append(dataAgg, AggregatedStatsResponse{
			Rank:         i + 1,
			Pokemon:      u.Pokemon,
			UsagePercent: fmt.Sprintf("%.5f%%", u.UsagePercent),
			LeadPercent:  leadPct,
			Viability:    viab,
		})
	}

	jsonDataAgg, _ := json.MarshalNoEscape(dataAgg)
	compressedAgg, _ := compressBrotliFast(jsonDataAgg)
	h.cache.DBCache.Set(cacheKey, compressedAgg)

	h.sendCached(w, r, compressedAgg, "public, max-age=2592000, s-maxage=2592000, immutable")
}

func (h *Handler) GetAggregatedStatsTuple(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	format := r.URL.Query().Get("format")
	rating := r.URL.Query().Get("rating")

	if month == "" || format == "" || rating == "" {
		http.Error(w, `{"error":"Missing parameters"}`, http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("aggregated_stats_v3_%s_%s_%s", month, format, rating)
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, r, cached, "public, max-age=2592000, s-maxage=2592000, immutable")
		return
	}

	usage, err := h.db.GetUsage(r.Context(), month, format, rating)
	if err != nil {
		http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		return
	}

	viability, _ := h.db.GetViability(r.Context(), month, format, rating)
	leads, _ := h.db.GetLeads(r.Context(), month, format, rating)

	viabilityMap := make(map[string]json.RawMessage)
	for _, v := range viability {
		viabilityMap[v.Pokemon] = v.Viability
	}

	leadsMap := make(map[string]float64)
	for _, l := range leads {
		leadsMap[l.Pokemon] = l.LeadPercent
	}

	dataAgg := make([][]interface{}, 0, len(usage))
	for i, u := range usage {
		leadPct, exists := leadsMap[u.Pokemon]
		if !exists {
			leadPct = 0.0
		}
		
		viab := viabilityMap[u.Pokemon]

		dataAgg = append(dataAgg, []interface{}{
			i + 1,
			u.Pokemon,
			u.UsagePercent,
			leadPct,
			viab,
		})
	}

	jsonDataAgg, _ := json.MarshalNoEscape(dataAgg)
	compressedAgg, _ := compressBrotliFast(jsonDataAgg)
	h.cache.DBCache.Set(cacheKey, compressedAgg)

	h.sendCached(w, r, compressedAgg, "public, max-age=2592000, s-maxage=2592000, immutable")
}

func (h *Handler) GetInit(w http.ResponseWriter, r *http.Request) {
	cacheKey := "init_v3"
	if cached, err := h.cache.DBCache.Get(cacheKey); err == nil {
		h.sendCached(w, r, cached, "public, max-age=3600, s-maxage=3600")
		return
	}

	months, err := h.db.GetMonths(r.Context())
	if err != nil || len(months) == 0 {
		http.Error(w, `{"error":"Database error or no months"}`, http.StatusInternalServerError)
		return
	}
	latestMonth := months[0]

	formatsData, _ := h.db.GetFormatsByMonth(r.Context(), latestMonth)
	formatsMap := make(map[string][]string)
	for _, f := range formatsData {
		formatsMap[f.Format] = append(formatsMap[f.Format], f.Rating)
	}
	
	type FormatItem struct {
		Format   string   `json:"format"`
		Ratings  []string `json:"ratings"`
		genNum   int
		tierRank int
	}
	var formatItems []FormatItem

	tierOrder := []string{"ou", "ubers", "uu", "ru", "nu", "pu", "lc", "monotype", "doublesou", "randombattle"}
	getTierRank := func(f string) int {
		clean := f
		if len(f) > 4 && f[:3] == "gen" {
			i := 3
			for i < len(f) && f[i] >= '0' && f[i] <= '9' {
				i++
			}
			clean = f[i:]
		}
		for idx, t := range tierOrder {
			if t == clean {
				return idx
			}
		}
		return 999
	}
	getGenNum := func(f string) int {
		if len(f) > 3 && f[:3] == "gen" {
			i := 3
			for i < len(f) && f[i] >= '0' && f[i] <= '9' {
				i++
			}
			if gen, err := strconv.Atoi(f[3:i]); err == nil {
				return gen
			}
		}
		return 0
	}

	for format, ratings := range formatsMap {
		sort.Slice(ratings, func(i, j int) bool {
			ni, _ := strconv.Atoi(ratings[i])
			nj, _ := strconv.Atoi(ratings[j])
			return ni < nj
		})
		if len(ratings) > 2 {
			ratings = ratings[len(ratings)-2:]
		}
		formatItems = append(formatItems, FormatItem{
			Format:   format,
			Ratings:  ratings,
			genNum:   getGenNum(format),
			tierRank: getTierRank(format),
		})
	}

	sort.Slice(formatItems, func(i, j int) bool {
		if formatItems[i].genNum != formatItems[j].genNum {
			return formatItems[i].genNum > formatItems[j].genNum
		}
		if formatItems[i].tierRank != formatItems[j].tierRank {
			return formatItems[i].tierRank < formatItems[j].tierRank
		}
		return formatItems[i].Format < formatItems[j].Format
	})

	var defaultFormat string
	var defaultRating string
	if len(formatItems) > 0 {
		defaultFormat = formatItems[0].Format
		defaultRating = formatItems[0].Ratings[0]
	}

	var dataAgg [][]interface{}
	if defaultFormat != "" && defaultRating != "" {
		usage, _ := h.db.GetUsage(r.Context(), latestMonth, defaultFormat, defaultRating)
		viability, _ := h.db.GetViability(r.Context(), latestMonth, defaultFormat, defaultRating)
		leads, _ := h.db.GetLeads(r.Context(), latestMonth, defaultFormat, defaultRating)

		viabilityMap := make(map[string]json.RawMessage)
		for _, v := range viability {
			viabilityMap[v.Pokemon] = v.Viability
		}
		leadsMap := make(map[string]float64)
		for _, l := range leads {
			leadsMap[l.Pokemon] = l.LeadPercent
		}
		for i, u := range usage {
			leadPct, exists := leadsMap[u.Pokemon]
			if !exists {
				leadPct = 0.0
			}
			dataAgg = append(dataAgg, []interface{}{
				i + 1,
				u.Pokemon,
				u.UsagePercent,
				leadPct,
				viabilityMap[u.Pokemon],
			})
		}
	}

	monthStrs := months

	resp := map[string]interface{}{
		"months":        monthStrs,
		"formats":       formatItems,
		"defaultMonth":  latestMonth,
		"defaultFormat": defaultFormat,
		"defaultRating": defaultRating,
		"stats":         dataAgg,
	}

	jsonDataAgg, _ := json.MarshalNoEscape(resp)
	compressedAgg, _ := compressBrotliFast(jsonDataAgg)
	h.cache.DBCache.Set(cacheKey, compressedAgg)

	h.sendCached(w, r, compressedAgg, "public, max-age=3600, s-maxage=3600")
}

func (h *Handler) GetDetails(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	format := r.URL.Query().Get("format")
	rating := r.URL.Query().Get("rating")

	if month == "" || format == "" || rating == "" {
		http.Error(w, `{"error":"Missing parameters"}`, http.StatusBadRequest)
		return
	}

	targetURL := fmt.Sprintf("https://www.smogon.com/stats/%s/moveset/%s-%s.txt", month, format, rating)

	q := r.URL.Query()
	q.Set("url", targetURL)
	r.URL.RawQuery = q.Encode()

	h.GetProxy(w, r)
}
