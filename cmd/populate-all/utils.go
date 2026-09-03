package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"compress/gzip"
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

func fetchFileContent(ctx context.Context, client *http.Client, targetUrl string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", targetUrl, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("failed to fetch %s: status %d", targetUrl, resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if strings.HasSuffix(targetUrl, ".gz") || resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err == nil {
			defer gzReader.Close()
			reader = gzReader
		}
	}

	return io.ReadAll(reader)
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
