package main

import (
	"github.com/goccy/go-json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/molecule-man/go-brrr"
)

const baseURL = "http://127.0.0.1:9000/api"
const concurrency = 50

type Client struct {
	http *http.Client
}

func newClient() *Client {
	return &Client{
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) getJSON(url string, target interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept-Encoding", "br")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	// We don't actually need to parse the response for the leaf endpoints.
	// We just need to make the request so the server caches it.
	if target == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}

	if _, err := io.ReadAll(resp.Body); err != nil {
		return err
	}

	// Note: We are ignoring the brotli compression here for the setup endpoints
	// because we just need to hit the Go proxy which compresses it anyway, but we
	// can also just NOT send Accept-Encoding for setup requests so we can parse easily.
	return nil
}

func (c *Client) getUncompressedJSON(url string, target interface{}) error {
	resp, err := c.http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	reader := brrr.NewReader(resp.Body)
	return json.NewDecoder(reader).Decode(target)
}

func main() {
	start := time.Now()
	client := newClient()
	log.Println("Starting warmup...")

	var months []string
	if err := client.getUncompressedJSON(baseURL+"/months", &months); err != nil {
		log.Fatalf("Failed to fetch months: %v", err)
	}
	log.Printf("Found %d months", len(months))

	type Task struct {
		Month  string
		Format string
		Rating string
	}
	var tasks []Task

	for _, month := range months {
		var formats []struct {
			Format  string   `json:"format"`
			Ratings []string `json:"ratings"`
		}
		if err := client.getUncompressedJSON(baseURL+"/formats?month="+month, &formats); err != nil {
			log.Printf("Failed to fetch formats for %s: %v", month, err)
			continue
		}
		for _, f := range formats {
			for _, rating := range f.Ratings {
				tasks = append(tasks, Task{Month: month, Format: f.Format, Rating: rating})
			}
		}
	}

	log.Printf("Generated %d permutations to fetch", len(tasks))

	taskCh := make(chan Task, len(tasks))
	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)

	var wg sync.WaitGroup
	var completed int32

	endpoints := []string{"usage", "metagame", "format-stats", "stats"}

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskCh {
				for _, ep := range endpoints {
					url := fmt.Sprintf("%s/%s?month=%s&format=%s&rating=%s", baseURL, ep, t.Month, t.Format, t.Rating)
					err := client.getJSON(url, nil)
					if err != nil {
						log.Printf("Error fetching %s: %v", url, err)
					}
				}
				

				// Request the moveset file to precache it as well, but ONLY for recent years (2025, 2026)
				if strings.HasPrefix(t.Month, "2026") || strings.HasPrefix(t.Month, "2025") {
					movesetURL := fmt.Sprintf("http://127.0.0.1:9000/?url=https://www.smogon.com/stats/%s/moveset/%s-%s.txt", t.Month, t.Format, t.Rating)
					err := client.getJSON(movesetURL, nil)
					if err != nil {
						log.Printf("Error fetching moveset %s: %v", movesetURL, err)
					}
				}

				atomic.AddInt32(&completed, 1)
				if completed%1000 == 0 {
					log.Printf("Progress: %d / %d", completed, len(tasks))
				}
			}
		}()
	}

	wg.Wait()
	log.Printf("Warmup completed in %v", time.Since(start))
}
