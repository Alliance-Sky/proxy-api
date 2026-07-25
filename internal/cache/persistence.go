package cache

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"log"
	"os"
)

type BackupRow struct {
	Type       string            `json:"type"`
	Key        string            `json:"key,omitempty"`
	URL        string            `json:"url,omitempty"`
	StatusCode int               `json:"statusCode,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body"` // Base64 encoded
}

type MovesetCacheEntry struct {
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
}

func (s *Service) RestoreFromFile(filepath string) error {
	file, err := os.Open(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("No cache backup file found. Starting fresh.")
			return nil
		}
		return err
	}
	defer file.Close()

	log.Println("Restoring cache from File...")
	scanner := bufio.NewScanner(file)

	const maxCapacity = 50 * 1024 * 1024 // 50MB
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	movesetCount := 0
	dbCount := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var row BackupRow
		if err := json.Unmarshal(line, &row); err != nil {
			log.Printf("Failed to unmarshal row: %v", err)
			continue
		}

		bodyBytes, err := base64.StdEncoding.DecodeString(row.Body)
		if err != nil {
			log.Printf("Failed to decode base64 body: %v", err)
			continue
		}

		if row.Type == "moveset" {
			entry := MovesetCacheEntry{
				StatusCode: row.StatusCode,
				Headers:    row.Headers,
				Body:       bodyBytes,
			}
			entryBytes, _ := json.Marshal(entry)
			s.MovesetCache.Set(row.URL, entryBytes)
			movesetCount++
		} else if row.Type == "db" {
			s.DBCache.Set(row.Key, bodyBytes)
			dbCount++
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	log.Printf("Successfully restored %d moveset items and %d DB items from file.", movesetCount, dbCount)
	return nil
}

func (s *Service) BackupToFile(filepath string) error {
	tmpFilepath := filepath + ".tmp"
	file, err := os.OpenFile(tmpFilepath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	writer := bufio.NewWriter(file)

	if mcIter := s.MovesetCache.Iterator(); mcIter != nil {
		for mcIter.SetNext() {
			record, err := mcIter.Value()
			if err != nil {
				continue
			}

			var entry MovesetCacheEntry
			if err := json.Unmarshal(record.Value(), &entry); err != nil {
				continue
			}

			row := BackupRow{
				Type:       "moveset",
				URL:        record.Key(),
				StatusCode: entry.StatusCode,
				Headers:    entry.Headers,
				Body:       base64.StdEncoding.EncodeToString(entry.Body),
			}
			rowBytes, _ := json.Marshal(row)
			writer.Write(rowBytes)
			writer.WriteByte('\n')
		}
	}

	if dbIter := s.DBCache.Iterator(); dbIter != nil {
		for dbIter.SetNext() {
			record, err := dbIter.Value()
			if err != nil {
				continue
			}

			row := BackupRow{
				Type: "db",
				Key:  record.Key(),
				Body: base64.StdEncoding.EncodeToString(record.Value()),
			}
			rowBytes, _ := json.Marshal(row)
			writer.Write(rowBytes)
			writer.WriteByte('\n')
		}
	}

	if err := writer.Flush(); err != nil {
		file.Close()
		return err
	}

	if err := file.Close(); err != nil {
		return err
	}

	return os.Rename(tmpFilepath, filepath)
}
