package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	Pool *pgxpool.Pool
}

func NewService(dsn string) (*Service, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	config.MaxConns = 100

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(context.Background()); err != nil {
		return nil, err
	}

	return &Service{
		Pool: pool,
	}, nil
}

func (s *Service) Close() {
	if s.Pool != nil {
		s.Pool.Close()
	}
}
