package api

import (
	"bytes"

	"github.com/andybalholm/brotli"
)

func compressBrotli(data []byte) ([]byte, error) {
	return compressBrotliWithLevel(data, 6)
}

func compressBrotliFast(data []byte) ([]byte, error) {
	return compressBrotliWithLevel(data, 3)
}

func compressBrotliWithLevel(data []byte, level int) ([]byte, error) {
	var buf bytes.Buffer
	writer := brotli.NewWriterLevel(&buf, level)

	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
