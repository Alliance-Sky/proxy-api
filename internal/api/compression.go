package api

import (
	"bytes"
	"io"
	"sync"

	"github.com/molecule-man/go-brrr"
)

var (
	// Pool for writers at level 6
	writerPoolL6 = sync.Pool{
		New: func() interface{} {
			w, _ := brrr.NewWriter(io.Discard, 6)
			return w
		},
	}
	// Pool for writers at level 3
	writerPoolL3 = sync.Pool{
		New: func() interface{} {
			w, _ := brrr.NewWriter(io.Discard, 3)
			return w
		},
	}
	// Pool for readers
	readerPool = sync.Pool{
		New: func() interface{} {
			return brrr.NewReader(bytes.NewReader(nil))
		},
	}
)

func compressBrotli(data []byte) ([]byte, error) {
	return compressBrotliWithLevel(data, 6)
}

func compressBrotliFast(data []byte) ([]byte, error) {
	return compressBrotliWithLevel(data, 3)
}

func compressBrotliWithLevel(data []byte, level int) ([]byte, error) {
	var buf bytes.Buffer
	var writer *brrr.Writer
	if level == 6 {
		writer = writerPoolL6.Get().(*brrr.Writer)
		defer writerPoolL6.Put(writer)
	} else if level == 3 {
		writer = writerPoolL3.Get().(*brrr.Writer)
		defer writerPoolL3.Put(writer)
	} else {
		writer, _ = brrr.NewWriter(&buf, level)
	}

	if writer != nil {
		writer.Reset(&buf)
	}

	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func decompressBrotli(data []byte) ([]byte, error) {
	r := bytes.NewReader(data)
	reader := readerPool.Get().(*brrr.Reader)
	defer readerPool.Put(reader)

	reader.Reset(r)
	return io.ReadAll(reader)
}
