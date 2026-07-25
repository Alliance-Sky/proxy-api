package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Alliance-Sky/proxy-api/internal/cache"
)

var (
	TotalInboundBytes  uint64
	TotalOutboundBytes uint64
)

var httpClient = &http.Client{
	Timeout: 5 * time.Second,
}

func (h *Handler) GetProxy(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("url")

	if targetURL == "" {
		stats := map[string]interface{}{
			"inboundBytes":         atomic.LoadUint64(&TotalInboundBytes),
			"outboundBytes":        atomic.LoadUint64(&TotalOutboundBytes),
			"movesetCacheItems":    h.cache.MovesetCache.Len(),
			"movesetCacheCapacity": h.cache.MovesetCache.Capacity(),
			"dbCacheItems":         h.cache.DBCache.Len(),
			"dbCacheCapacity":      h.cache.DBCache.Capacity(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
		return
	}

	if !strings.HasPrefix(targetURL, "https://www.smogon.com/stats/") {
		http.Error(w, `{"error":"Access denied: You can only proxy Smogon stats URLs."}`, http.StatusForbidden)
		return
	}

	isBrowser := r.Header.Get("origin") != "" ||
		r.Header.Get("sec-fetch-mode") != "" ||
		r.Header.Get("sec-fetch-dest") != "" ||
		r.Header.Get("sec-ch-ua") != "" ||
		strings.Contains(r.Header.Get("user-agent"), "Mozilla")

	if !isBrowser {
		http.Error(w, `{"error":"Access denied: This proxy only works in a browser"}`, http.StatusForbidden)
		return
	}

	if cachedBytes, err := h.cache.MovesetCache.Get(targetURL); err == nil {
		var entry cache.MovesetCacheEntry
		if err := json.Unmarshal(cachedBytes, &entry); err == nil {
			for k, v := range entry.Headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(entry.StatusCode)
			w.Write(entry.Body)
			atomic.AddUint64(&TotalOutboundBytes, uint64(len(entry.Body)))
			return
		}
	}

	req, err := http.NewRequestWithContext(r.Context(), "GET", targetURL, nil)
	if err != nil {
		http.Error(w, `{"error":"Failed to create request"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36")

	client := &http.Client{
		Timeout: 35 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, `{"error":"Failed to fetch data"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, `{"error":"Failed to read response"}`, http.StatusInternalServerError)
		return
	}

	atomic.AddUint64(&TotalInboundBytes, uint64(len(bodyBytes)))

	savedHeaders := make(map[string]string)
	passThroughHeaders := []string{"Content-Type", "Etag", "Last-Modified"}
	for _, h := range passThroughHeaders {
		if val := resp.Header.Get(h); val != "" {
			w.Header().Set(h, val)
			savedHeaders[h] = val
		}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		isDirectory := strings.HasSuffix(targetURL, "/")
		if isDirectory {
			savedHeaders["Cache-Control"] = "public, max-age=60, s-maxage=60"
		} else {
			savedHeaders["Cache-Control"] = "public, max-age=31536000, s-maxage=31536000, immutable"
		}
		w.Header().Set("Cache-Control", savedHeaders["Cache-Control"])

		if strings.HasSuffix(targetURL, ".txt") && strings.Contains(targetURL, "/moveset/") {
			parsedJSON, err := ParseMoveset(bodyBytes)
			if err == nil {
				bodyBytes = parsedJSON
			}
		}

		compressed, err := compressBrotli(bodyBytes)
		if err == nil {
			savedHeaders["Content-Encoding"] = "br"
			w.Header().Set("Content-Encoding", "br")

			if !isDirectory && strings.Contains(targetURL, "/moveset/") {
				entry := cache.MovesetCacheEntry{
					StatusCode: resp.StatusCode,
					Headers:    savedHeaders,
					Body:       compressed,
				}
				entryBytes, _ := json.Marshal(entry)
				h.cache.MovesetCache.Set(targetURL, entryBytes)
			}
			w.WriteHeader(resp.StatusCode)
			w.Write(compressed)
			atomic.AddUint64(&TotalOutboundBytes, uint64(len(compressed)))
			return
		}
	}

	w.WriteHeader(resp.StatusCode)
	w.Write(bodyBytes)
	atomic.AddUint64(&TotalOutboundBytes, uint64(len(bodyBytes)))
}
