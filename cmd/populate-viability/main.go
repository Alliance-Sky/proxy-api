package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const BaseURL = "https://www.smogon.com/stats/"

func fetchHTML(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("failed to fetch %s: status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func parseLinks(html string) []string {
	var links []string
	re := regexp.MustCompile(`<a href="([^"]+)">`)
	matches := re.FindAllStringSubmatch(html, -1)
	for _, match := range matches {
		if len(match) > 1 {
			href := match[1]
			if href != "../" && href != "/" {
				links = append(links, strings.TrimSuffix(href, "/"))
			}
		}
	}
	return links
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx := context.Background()

	dbURL := "postgres://ubuntu@/smogon_stats?host=/var/run/postgresql"
	if envURL := os.Getenv("DATABASE_URL"); envURL != "" {
		dbURL = envURL
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	slog.Info("Fetching existing records from DB to skip...")
	rows, err := pool.Query(ctx, "SELECT DISTINCT month, format, rating FROM viability_stats")
	if err != nil {
		slog.Error("Failed to fetch existing records", "error", err)
		os.Exit(1)
	}

	existingKeys := make(map[string]bool)
	for rows.Next() {
		var month, format, rating string
		if err := rows.Scan(&month, &format, &rating); err == nil {
			existingKeys[fmt.Sprintf("%s_%s_%s", month, format, rating)] = true
		}
	}
	rows.Close()

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	slog.Info("Fetching months from Smogon...")
	rootHTML, err := fetchHTML(ctx, client, BaseURL)
	if err != nil {
		slog.Error("Failed to fetch root HTML", "error", err)
		os.Exit(1)
	}

	rawMonths := parseLinks(rootHTML)
	var months []string
	monthRegex := regexp.MustCompile(`^(\d{4})-\d{2}$`)
	for _, m := range rawMonths {
		matches := monthRegex.FindStringSubmatch(m)
		if len(matches) > 1 {
			year, _ := strconv.Atoi(matches[1])
			if year >= 2014 && year <= 2026 {
				months = append(months, m)
			}
		}
	}

	for i, j := 0, len(months)-1; i < j; i, j = i+1, j-1 {
		months[i], months[j] = months[j], months[i]
	}

	insertQuery := `
		INSERT INTO viability_stats (month, format, rating, pokemon, viability) 
		SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[], $4::text[], $5::jsonb[])
		ON CONFLICT (month, format, rating, pokemon) DO NOTHING
	`

	var currentYear string

	for _, month := range months {
		year := month[:4]
		if currentYear != "" && currentYear != year {
			slog.Info(fmt.Sprintf("Finished processing year %s. Waiting for 10 seconds...", currentYear))
			time.Sleep(10 * time.Second)
		}
		currentYear = year

		slog.Info(fmt.Sprintf("Processing month: %s", month))
		chaosHTML, err := fetchHTML(ctx, client, BaseURL+month+"/chaos/")
		if err != nil {
			slog.Warn(fmt.Sprintf("Failed to fetch chaos directory for %s, skipping.", month))
			continue
		}

		chaosFiles := []string{}
		for _, f := range parseLinks(chaosHTML) {
			if strings.HasSuffix(f, ".json") {
				chaosFiles = append(chaosFiles, f)
			}
		}

		formatRatings := make(map[string][]string)
		for _, file := range chaosFiles {
			nameWithoutExt := strings.TrimSuffix(file, ".json")
			lastDash := strings.LastIndex(nameWithoutExt, "-")
			if lastDash == -1 {
				continue
			}
			format := nameWithoutExt[:lastDash]
			rating := nameWithoutExt[lastDash+1:]
			formatRatings[format] = append(formatRatings[format], rating)
		}

		var targetFiles []string
		for format, ratings := range formatRatings {
			sort.Slice(ratings, func(i, j int) bool {
				a, _ := strconv.Atoi(ratings[i])
				b, _ := strconv.Atoi(ratings[j])
				return a < b
			})
			if len(ratings) > 2 {
				ratings = ratings[len(ratings)-2:]
			}
			for _, rating := range ratings {
				targetFiles = append(targetFiles, fmt.Sprintf("%s-%s.json", format, rating))
			}
		}

		concurrency := 15
		var mu sync.Mutex

		for i := 0; i < len(targetFiles); i += concurrency {
			end := i + concurrency
			if end > len(targetFiles) {
				end = len(targetFiles)
			}
			batch := targetFiles[i:end]

			var wg sync.WaitGroup
			for _, file := range batch {
				wg.Add(1)
				go func(file string) {
					defer wg.Done()
					nameWithoutExt := strings.TrimSuffix(file, ".json")
					lastDash := strings.LastIndex(nameWithoutExt, "-")
					if lastDash == -1 {
						return
					}
					format := nameWithoutExt[:lastDash]
					rating := nameWithoutExt[lastDash+1:]

					key := fmt.Sprintf("%s_%s_%s", month, format, rating)
					mu.Lock()
					exists := existingKeys[key]
					mu.Unlock()

					if exists {
						return
					}

					slog.Info(fmt.Sprintf("Fetching %s %s %s...", month, format, rating))
					targetUrl := BaseURL + month + "/chaos/" + file

					var jsonBytes []byte
					var fetchErr error
					for retries := 3; retries > 0; retries-- {
						req, _ := http.NewRequestWithContext(ctx, "GET", targetUrl, nil)
						resp, err := client.Do(req)
						if err == nil && resp.StatusCode == 200 {
							jsonBytes, fetchErr = io.ReadAll(resp.Body)
							resp.Body.Close()
							if fetchErr == nil {
								break
							}
						} else if resp != nil {
							resp.Body.Close()
						}
						time.Sleep(1 * time.Second)
					}

					if len(jsonBytes) == 0 {
						slog.Error("Failed to fetch JSON", "url", targetUrl)
						return
					}

					var data struct {
						Data map[string]map[string]json.RawMessage `json:"data"`
					}
					if err := json.Unmarshal(jsonBytes, &data); err != nil {
						slog.Error("Failed to parse JSON", "url", targetUrl, "error", err)
						return
					}

					var validPokemon []string
					var viabilitiesArr []string

					for p, pData := range data.Data {
						if v, ok := pData["Viability Ceiling"]; ok {
							validPokemon = append(validPokemon, p)
							viabilitiesArr = append(viabilitiesArr, string(v))
						}
					}

					count := len(validPokemon)
					if count > 0 {
						monthsArr := make([]string, count)
						formatsArr := make([]string, count)
						ratingsArr := make([]string, count)
						for k := 0; k < count; k++ {
							monthsArr[k] = month
							formatsArr[k] = format
							ratingsArr[k] = rating
						}

						_, err := pool.Exec(ctx, insertQuery, monthsArr, formatsArr, ratingsArr, validPokemon, viabilitiesArr)
						if err != nil {
							slog.Error("Failed to insert", "url", targetUrl, "error", err)
							return
						}
					}

					slog.Info(fmt.Sprintf("Inserted viability for %d pokemon in %s.", count, key))
					mu.Lock()
					existingKeys[key] = true
					mu.Unlock()

				}(file)
			}
			wg.Wait()
		}
	}

	slog.Info("Finished populating viability stats.")
}
