package main

import (
	"context"
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

func parseTotalBattles(text string) int {
	re := regexp.MustCompile(`(?i)Total battles:\s*(\d+)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) > 1 {
		val, _ := strconv.Atoi(matches[1])
		return val
	}
	return 0
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

	slog.Info("Creating format_stats table if it doesn't exist...")
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS format_stats (
			id SERIAL PRIMARY KEY,
			month VARCHAR(20),
			format VARCHAR(100),
			rating VARCHAR(20),
			total_battles INTEGER,
			UNIQUE(month, format, rating)
		)
	`)
	if err != nil {
		slog.Error("Failed to create table", "error", err)
		os.Exit(1)
	}

	slog.Info("Fetching existing format_stats records to skip...")
	rows, err := pool.Query(ctx, "SELECT DISTINCT month, format, rating FROM format_stats")
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
		INSERT INTO format_stats (month, format, rating, total_battles) 
		SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[], $4::integer[])
		ON CONFLICT (month, format, rating) DO NOTHING
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
		monthHTML, err := fetchHTML(ctx, client, BaseURL+month+"/")
		if err != nil {
			slog.Warn(fmt.Sprintf("Failed to fetch root directory for %s, skipping.", month))
			continue
		}

		txtFiles := []string{}
		for _, f := range parseLinks(monthHTML) {
			if strings.HasSuffix(f, ".txt") {
				txtFiles = append(txtFiles, f)
			}
		}

		formatRatings := make(map[string][]string)
		for _, file := range txtFiles {
			nameWithoutExt := strings.TrimSuffix(file, ".txt")
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
				targetFiles = append(targetFiles, fmt.Sprintf("%s-%s.txt", format, rating))
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
			var insertMonths []string
			var insertFormats []string
			var insertRatings []string
			var insertBattles []int

			for _, file := range batch {
				wg.Add(1)
				go func(file string) {
					defer wg.Done()
					nameWithoutExt := strings.TrimSuffix(file, ".txt")
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

					slog.Info(fmt.Sprintf("Fetching battles for %s %s %s...", month, format, rating))
					targetUrl := BaseURL + month + "/" + file

					var text string
					var fetchErr error
					for retries := 3; retries > 0; retries-- {
						req, _ := http.NewRequestWithContext(ctx, "GET", targetUrl, nil)
						resp, err := client.Do(req)
						if err == nil && resp.StatusCode == 200 {
							b, e := io.ReadAll(resp.Body)
							resp.Body.Close()
							if e == nil {
								text = string(b)
								fetchErr = nil
								break
							}
						} else if resp != nil {
							resp.Body.Close()
						}
						fetchErr = fmt.Errorf("failed fetch")
						time.Sleep(1 * time.Second)
					}

					if fetchErr != nil {
						slog.Error("Failed to fetch text", "url", targetUrl)
						return
					}

					totalBattles := parseTotalBattles(text)

					if totalBattles > 0 {
						mu.Lock()
						insertMonths = append(insertMonths, month)
						insertFormats = append(insertFormats, format)
						insertRatings = append(insertRatings, rating)
						insertBattles = append(insertBattles, totalBattles)
						existingKeys[key] = true
						mu.Unlock()
					}

				}(file)
			}
			wg.Wait()

			if len(insertMonths) > 0 {
				_, err := pool.Exec(ctx, insertQuery, insertMonths, insertFormats, insertRatings, insertBattles)
				if err != nil {
					slog.Error("Failed to insert format stats", "error", err)
				} else {
					slog.Info(fmt.Sprintf("Inserted %d format stats.", len(insertMonths)))
				}
			}
		}
	}

	slog.Info("Finished populating format stats.")
}
