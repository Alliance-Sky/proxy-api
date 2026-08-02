package db

import (
	"context"
)

func (s *Service) GetMonths(ctx context.Context) ([]string, error) {
	rows, err := s.Pool.Query(ctx, "SELECT DISTINCT month FROM usage_stats ORDER BY month DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var months []string
	for rows.Next() {
		var month string
		if err := rows.Scan(&month); err != nil {
			return nil, err
		}
		months = append(months, month)
	}
	return months, rows.Err()
}

type FormatRating struct {
	Format string
	Rating string
}

func (s *Service) GetFormatsByMonth(ctx context.Context, month string) ([]FormatRating, error) {
	rows, err := s.Pool.Query(ctx, "SELECT DISTINCT format, rating FROM usage_stats WHERE month = $1", month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FormatRating
	for rows.Next() {
		var f FormatRating
		if err := rows.Scan(&f.Format, &f.Rating); err != nil {
			return nil, err
		}
		results = append(results, f)
	}
	return results, rows.Err()
}

type ViabilityRow struct {
	Pokemon   string
	Viability []byte
}

func (s *Service) GetViability(ctx context.Context, month, format, rating string) ([]ViabilityRow, error) {
	rows, err := s.Pool.Query(ctx, "SELECT pokemon, viability FROM viability_stats WHERE month = $1 AND format = $2 AND rating = $3", month, format, rating)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ViabilityRow
	for rows.Next() {
		var v ViabilityRow
		if err := rows.Scan(&v.Pokemon, &v.Viability); err != nil {
			return nil, err
		}
		results = append(results, v)
	}
	return results, rows.Err()
}

type UsageRow struct {
	Pokemon      string
	UsagePercent float64
}

func (s *Service) GetUsage(ctx context.Context, month, format, rating string) ([]UsageRow, error) {
	rows, err := s.Pool.Query(ctx, "SELECT pokemon, usage_percent FROM usage_stats WHERE month = $1 AND format = $2 AND rating = $3 ORDER BY usage_percent DESC", month, format, rating)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []UsageRow
	for rows.Next() {
		var u UsageRow
		if err := rows.Scan(&u.Pokemon, &u.UsagePercent); err != nil {
			return nil, err
		}
		results = append(results, u)
	}
	return results, rows.Err()
}

type MonthFormatRating struct {
	Month  string
	Format string
	Rating string
}

func (s *Service) GetAllFormatRatings(ctx context.Context) ([]MonthFormatRating, error) {
	rows, err := s.Pool.Query(ctx, "SELECT DISTINCT month, format, rating FROM usage_stats ORDER BY month DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MonthFormatRating
	for rows.Next() {
		var f MonthFormatRating
		if err := rows.Scan(&f.Month, &f.Format, &f.Rating); err != nil {
			return nil, err
		}
		results = append(results, f)
	}
	return results, rows.Err()
}
