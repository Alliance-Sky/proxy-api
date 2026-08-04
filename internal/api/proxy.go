package api

import (


	"context"
	"github.com/goccy/go-json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Alliance-Sky/proxy-api/internal/cache"
	"golang.org/x/sync/singleflight"
)

var (
	TotalInboundBytes  uint64
	TotalOutboundBytes uint64
	proxyGroup         singleflight.Group
	parseGroup         singleflight.Group
)

var httpClient = &http.Client{
	Timeout: 35 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	},
}

func (h *Handler) GetProxy(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("url")
	pokemon := r.URL.Query().Get("pokemon")
	h.serveProxy(w, r, targetURL, pokemon)
}

func (h *Handler) serveProxy(w http.ResponseWriter, r *http.Request, targetURL, pokemon string) {
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
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.Encode(stats)
		return
	}

	if !strings.HasPrefix(targetURL, "https://www.smogon.com/stats/") {
		http.Error(w, `{"error":"Access denied: You can only proxy Smogon stats URLs."}`, http.StatusForbidden)
		return
	}



	cacheKey := targetURL
	if pokemon != "" {
		cacheKey = targetURL + "|pokemon:" + pokemon
	}

	var entry *cache.MovesetCacheEntry

	if cachedBytes, err := h.cache.MovesetCache.Get(cacheKey); err == nil {
		var e cache.MovesetCacheEntry
		if err := e.Unmarshal(cachedBytes); err == nil {
			entry = &e
		}
	}

	if entry == nil {
		parsedURL, _ := url.Parse(targetURL)
		isMoveset := parsedURL != nil && strings.HasSuffix(parsedURL.Path, ".txt") && strings.Contains(parsedURL.Path, "/moveset/")

		v, err, _ := proxyGroup.Do(cacheKey, func() (interface{}, error) {
			if pokemon != "" && isMoveset {
				if fullCachedBytes, err := h.cache.MovesetCache.Get(targetURL); err == nil {
					var fullEntry cache.MovesetCacheEntry
					if err := fullEntry.Unmarshal(fullCachedBytes); err == nil {
						vMap, parseErr, _ := parseGroup.Do(targetURL, func() (interface{}, error) {
							uncompressed, err := decompressBrotli(fullEntry.Body)
							if err != nil {
								return nil, err
							}
							var fullMap map[string]*PokemonData
							if err := json.Unmarshal(uncompressed, &fullMap); err != nil {
								return nil, err
							}
							return fullMap, nil
						})

						if parseErr == nil {
							fullMap := vMap.(map[string]*PokemonData)
							if pokeData, ok := fullMap[pokemon]; ok {
								pokeBytes, _ := json.MarshalNoEscape(pokeData)
								pokeCompressed, err := compressBrotliFast(pokeBytes)
								if err == nil {
									e := cache.MovesetCacheEntry{
										StatusCode: fullEntry.StatusCode,
										Headers:    fullEntry.Headers,
										Body:       pokeCompressed,
									}
									eBytes, _ := e.Marshal()
									h.cache.MovesetCache.Set(cacheKey, eBytes)
									return &e, nil
								}
							} else {
								return nil, fmt.Errorf("pokemon_not_found")
							}
						}
					}
				}
			}

			req, err := http.NewRequestWithContext(context.Background(), "GET", targetURL, nil)
			if err != nil {
				return nil, fmt.Errorf("req_failed")
			}
			req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36")

			resp, err := httpClient.Do(req)
			if err != nil {
				return nil, fmt.Errorf("fetch_failed")
			}
			defer resp.Body.Close()

			bodyBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("read_failed")
			}

			atomic.AddUint64(&TotalInboundBytes, uint64(len(bodyBytes)))

			savedHeaders := make(map[string]string)
			passThroughHeaders := []string{"Content-Type", "Etag", "Last-Modified"}
			for _, hdr := range passThroughHeaders {
				if val := resp.Header.Get(hdr); val != "" {
					savedHeaders[hdr] = val
				}
			}

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				isDirectory := strings.HasSuffix(targetURL, "/")
				if isDirectory {
					savedHeaders["Cache-Control"] = "public, max-age=60, s-maxage=60"
				} else {
					savedHeaders["Cache-Control"] = "public, max-age=31536000, s-maxage=31536000, immutable"
				}

				var fullParsedJSON []byte
				var responseBytes = bodyBytes
				var fullMap map[string]*PokemonData

				if isMoveset {
					parsedJSON, err := ParseMoveset(bodyBytes)
					if err == nil {
						fullParsedJSON = parsedJSON
						responseBytes = parsedJSON
						json.Unmarshal(parsedJSON, &fullMap)
						savedHeaders["Content-Type"] = "application/json"
					}
				}

				if isMoveset && fullParsedJSON != nil {
					if fullCompressed, err := compressBrotliFast(fullParsedJSON); err == nil {
						hdrsCopy := make(map[string]string)
						for k, val := range savedHeaders {
							hdrsCopy[k] = val
						}
						hdrsCopy["Content-Encoding"] = "br"

						e := cache.MovesetCacheEntry{
							StatusCode: resp.StatusCode,
							Headers:    hdrsCopy,
							Body:       fullCompressed,
						}
						eBytes, _ := e.Marshal()
						h.cache.MovesetCache.Set(targetURL, eBytes)
					}
				}

				if isMoveset && fullMap != nil {
					hdrsCopy := make(map[string]string)
					for k, val := range savedHeaders {
						hdrsCopy[k] = val
					}
					hdrsCopy["Content-Encoding"] = "br"
					go func(full map[string]*PokemonData, tURL string, status int, hdrs map[string]string) {
						for pokeName, pData := range full {
							pBytes, err := json.MarshalNoEscape(pData)
							if err != nil {
								continue
							}
							pComp, err := compressBrotliFast(pBytes)
							if err != nil {
								continue
							}
							cEntry := cache.MovesetCacheEntry{
								StatusCode: status,
								Headers:    hdrs,
								Body:       pComp,
							}
							cEntryBytes, _ := cEntry.Marshal()
							h.cache.MovesetCache.Set(tURL+"|pokemon:"+pokeName, cEntryBytes)
						}
					}(fullMap, targetURL, resp.StatusCode, hdrsCopy)
				}

				if pokemon != "" && fullMap != nil {
					if pokeData, ok := fullMap[pokemon]; ok {
						pokeBytes, _ := json.MarshalNoEscape(pokeData)
						responseBytes = pokeBytes
					} else {
						return nil, fmt.Errorf("pokemon_not_found")
					}
				}

				compressed, err := compressBrotliFast(responseBytes)
				if err == nil {
					savedHeaders["Content-Encoding"] = "br"

					e := cache.MovesetCacheEntry{
						StatusCode: resp.StatusCode,
						Headers:    savedHeaders,
						Body:       compressed,
					}
					
					if !isDirectory && (isMoveset || pokemon != "") {
						eBytes, _ := e.Marshal()
						h.cache.MovesetCache.Set(cacheKey, eBytes)
					}
					return &e, nil
				}
			}

			return &cache.MovesetCacheEntry{
				StatusCode: resp.StatusCode,
				Headers:    savedHeaders,
				Body:       bodyBytes,
			}, nil
		})

		if err != nil {
			if err.Error() == "pokemon_not_found" {
				http.Error(w, `{"error":"Pokemon not found"}`, http.StatusNotFound)
			} else if err.Error() == "req_failed" {
				http.Error(w, `{"error":"Failed to create request"}`, http.StatusInternalServerError)
			} else if err.Error() == "fetch_failed" {
				http.Error(w, `{"error":"Failed to fetch data"}`, http.StatusBadGateway)
			} else if err.Error() == "read_failed" {
				http.Error(w, `{"error":"Failed to read response"}`, http.StatusInternalServerError)
			} else {
				http.Error(w, `{"error":"Internal Server Error"}`, http.StatusInternalServerError)
			}
			return
		}
		
		entry = v.(*cache.MovesetCacheEntry)
	}

	acceptEncoding := r.Header.Get("Accept-Encoding")
	isBrotli := entry.Headers["Content-Encoding"] == "br"

	for k, v := range entry.Headers {
		if k == "Content-Encoding" && v == "br" && !strings.Contains(acceptEncoding, "br") {
			continue
		}
		w.Header().Set(k, v)
	}

	if isBrotli {
		if strings.Contains(acceptEncoding, "br") {
			w.WriteHeader(entry.StatusCode)
			w.Write(entry.Body)
			atomic.AddUint64(&TotalOutboundBytes, uint64(len(entry.Body)))
		} else {
			uncompressed, _ := decompressBrotli(entry.Body)
			w.WriteHeader(entry.StatusCode)
			w.Write(uncompressed)
			atomic.AddUint64(&TotalOutboundBytes, uint64(len(uncompressed)))
		}
	} else {
		w.WriteHeader(entry.StatusCode)
		w.Write(entry.Body)
		atomic.AddUint64(&TotalOutboundBytes, uint64(len(entry.Body)))
	}
}
