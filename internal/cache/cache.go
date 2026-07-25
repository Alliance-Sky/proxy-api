package cache

import (
	"context"
	"time"

	"github.com/allegro/bigcache/v3"
)

type Service struct {
	MovesetCache *bigcache.BigCache
	DBCache      *bigcache.BigCache
}

func NewService() (*Service, error) {
	movesetConfig := bigcache.DefaultConfig(24 * time.Hour)
	movesetConfig.HardMaxCacheSize = 3072
	movesetConfig.CleanWindow = 1 * time.Hour
	movesetConfig.MaxEntrySize = 1024 * 1024

	mc, err := bigcache.New(context.Background(), movesetConfig)
	if err != nil {
		return nil, err
	}

	dbConfig := bigcache.DefaultConfig(24 * time.Hour)
	dbConfig.HardMaxCacheSize = 1024
	dbConfig.CleanWindow = 1 * time.Hour
	dbConfig.MaxEntrySize = 512 * 1024

	dc, err := bigcache.New(context.Background(), dbConfig)
	if err != nil {
		return nil, err
	}

	return &Service{
		MovesetCache: mc,
		DBCache:      dc,
	}, nil
}
