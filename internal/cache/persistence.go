package cache

import (
	"bufio"
	"encoding/binary"
	"io"
	"log"
	"os"
)

//go:generate msgp
type MovesetCacheEntry struct {
	StatusCode int               `msg:"status_code"`
	Headers    map[string]string `msg:"headers"`
	Body       []byte            `msg:"body"`
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
