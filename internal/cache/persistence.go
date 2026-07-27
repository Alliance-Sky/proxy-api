package cache

import (
	"bufio"
	"encoding/binary"
	"github.com/goccy/go-json"
	"errors"
	"io"
	"log"
	"os"
)

type MovesetCacheEntry struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}

func (e *MovesetCacheEntry) Marshal() ([]byte, error) {
	headerBytes, err := json.MarshalNoEscape(e.Headers)
	if err != nil {
		return nil, err
	}

	headerLen := uint32(len(headerBytes))
	buf := make([]byte, 8+headerLen+uint32(len(e.Body)))

	binary.LittleEndian.PutUint32(buf[0:4], uint32(e.StatusCode))
	binary.LittleEndian.PutUint32(buf[4:8], headerLen)
	copy(buf[8:], headerBytes)
	copy(buf[8+headerLen:], e.Body)

	return buf, nil
}

func (e *MovesetCacheEntry) Unmarshal(data []byte) error {
	if len(data) < 8 {
		return errors.New("data too short")
	}

	e.StatusCode = int(binary.LittleEndian.Uint32(data[0:4]))
	headerLen := binary.LittleEndian.Uint32(data[4:8])

	if uint32(len(data)) < 8+headerLen {
		return errors.New("invalid header length")
	}

	e.Headers = make(map[string]string)
	if headerLen > 0 {
		if err := json.Unmarshal(data[8:8+headerLen], &e.Headers); err != nil {
			return err
		}
	}

	e.Body = data[8+headerLen:]
	return nil
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
	reader := bufio.NewReader(file)

	movesetCount := 0
	dbCount := 0

	for {
		typeByte, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		var keyLen uint32
		if err := binary.Read(reader, binary.LittleEndian, &keyLen); err != nil {
			return err
		}

		keyBytes := make([]byte, keyLen)
		if _, err := io.ReadFull(reader, keyBytes); err != nil {
			return err
		}

		var valLen uint32
		if err := binary.Read(reader, binary.LittleEndian, &valLen); err != nil {
			return err
		}

		valBytes := make([]byte, valLen)
		if _, err := io.ReadFull(reader, valBytes); err != nil {
			return err
		}

		if typeByte == 0 { // moveset
			s.MovesetCache.Set(string(keyBytes), valBytes)
			movesetCount++
		} else if typeByte == 1 { // db
			s.DBCache.Set(string(keyBytes), valBytes)
			dbCount++
		}
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

	writeRecord := func(typeByte byte, key string, val []byte) error {
		if err := writer.WriteByte(typeByte); err != nil {
			return err
		}

		var lenBuf [4]byte
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(key)))
		if _, err := writer.Write(lenBuf[:]); err != nil {
			return err
		}
		if _, err := writer.WriteString(key); err != nil {
			return err
		}

		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(val)))
		if _, err := writer.Write(lenBuf[:]); err != nil {
			return err
		}
		if _, err := writer.Write(val); err != nil {
			return err
		}
		return nil
	}

	if mcIter := s.MovesetCache.Iterator(); mcIter != nil {
		for mcIter.SetNext() {
			record, err := mcIter.Value()
			if err != nil {
				continue
			}
			
			if err := writeRecord(0, record.Key(), record.Value()); err != nil {
				continue
			}
		}
	}

	if dbIter := s.DBCache.Iterator(); dbIter != nil {
		for dbIter.SetNext() {
			record, err := dbIter.Value()
			if err != nil {
				continue
			}
			
			if err := writeRecord(1, record.Key(), record.Value()); err != nil {
				continue
			}
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
