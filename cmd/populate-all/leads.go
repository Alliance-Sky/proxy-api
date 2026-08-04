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



type LeadStat struct {
	Pokemon     string
	LeadPercent float64
}

func parseLeadsStats(text string) []LeadStat {
	lines := strings.Split(text, "\n")
	var data []LeadStat

	startIndex := 0
	for i, line := range lines {
		if strings.HasPrefix(line, " + ") {
			continue
		}
		if strings.Contains(line, "Rank") && strings.Contains(line, "Pokemon") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			startIndex = i
			break
		}
	}

	for i := startIndex; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "+") || line == "" {
			continue
		}

		parts := strings.Split(line, "|")
		for j := range parts {
			parts[j] = strings.TrimSpace(parts[j])
		}

		if len(parts) >= 5 {
			leadStr := strings.TrimSuffix(parts[3], "%")
			if leadPercent, err := strconv.ParseFloat(leadStr, 64); err == nil && leadPercent > 0 {
				data = append(data, LeadStat{
					Pokemon:     parts[2],
					LeadPercent: leadPercent,
				})
			}
		}
	}
	return data
}

func populateLeads() {
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

	slog.Info("Creating leads_stats table if it doesn't exist...")
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS leads_stats (
			id SERIAL PRIMARY KEY,
			month VARCHAR(20),
			format VARCHAR(50),
			rating VARCHAR(20),
			pokemon VARCHAR(100),
			lead_percent REAL,
			UNIQUE(month, format, rating, pokemon)
		)
	`)
	if err != nil {
		slog.Error("Failed to create table", "error", err)
		os.Exit(1)
	}

	slog.Info("Fetching existing leads records to skip...")
	rows, err := pool.Query(ctx, "SELECT DISTINCT month, format, rating FROM leads_stats")
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
		INSERT INTO leads_stats (month, format, rating, pokemon, lead_percent) 
		SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[], $4::text[], $5::real[])
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
		monthHTML, err := fetchHTML(ctx, client, BaseURL+month+"/leads/")
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

					slog.Info(fmt.Sprintf("Fetching %s %s %s...", month, format, rating))
					targetUrl := BaseURL + month + "/leads/" + file

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

					data := parseLeadsStats(text)
					count := len(data)

					if count > 0 {
						monthsArr := make([]string, count)
						formatsArr := make([]string, count)
						ratingsArr := make([]string, count)
						pokemonsArr := make([]string, count)
						leadPercentsArr := make([]float64, count)

						for k := 0; k < count; k++ {
							monthsArr[k] = month
							formatsArr[k] = format
							ratingsArr[k] = rating
							pokemonsArr[k] = data[k].Pokemon
							leadPercentsArr[k] = data[k].LeadPercent
						}

						_, err := pool.Exec(ctx, insertQuery, monthsArr, formatsArr, ratingsArr, pokemonsArr, leadPercentsArr)
						if err != nil {
							slog.Error("Failed to insert", "url", targetUrl, "error", err)
							return
						}
					}

					slog.Info(fmt.Sprintf("Inserted lead stats for %d pokemon in %s.", count, key))
					mu.Lock()
					existingKeys[key] = true
					mu.Unlock()

				}(file)
			}
			wg.Wait()
		}
	}

	slog.Info("Finished populating lead stats.")
}
