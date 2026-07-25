package db

import (
	"context"
)

type TrendRow struct {
	Month        string
	Pokemon      string
	UsagePercent float64
}

func (s *Service) GetTrend(ctx context.Context, format, rating string, pokemons []string, months []string) ([]TrendRow, error) {
	query := "SELECT month, pokemon, usage_percent FROM usage_stats WHERE format = $1 AND rating = $2 AND pokemon = ANY($3) AND month = ANY($4) ORDER BY month ASC"
	rows, err := s.Pool.Query(ctx, query, format, rating, pokemons, months)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []TrendRow
	for rows.Next() {
		var t TrendRow
		if err := rows.Scan(&t.Month, &t.Pokemon, &t.UsagePercent); err != nil {
			return nil, err
		}
		results = append(results, t)
	}
	return results, rows.Err()
}

type LeadRow struct {
	Pokemon     string
	LeadPercent float64
}

func (s *Service) GetLeads(ctx context.Context, month, format, rating string) ([]LeadRow, error) {
	rows, err := s.Pool.Query(ctx, "SELECT pokemon, lead_percent FROM leads_stats WHERE month = $1 AND format = $2 AND rating = $3 ORDER BY lead_percent DESC", month, format, rating)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []LeadRow
	for rows.Next() {
		var l LeadRow
		if err := rows.Scan(&l.Pokemon, &l.LeadPercent); err != nil {
			return nil, err
		}
		results = append(results, l)
	}
	return results, rows.Err()
}

type MetagameRow struct {
	Stalliness float64
	Playstyles []byte
}

func (s *Service) GetMetagame(ctx context.Context, month, format, rating string) (MetagameRow, error) {
	var row MetagameRow
	err := s.Pool.QueryRow(ctx, "SELECT stalliness, playstyles FROM metagame_stats WHERE month = $1 AND format = $2 AND rating = $3", month, format, rating).Scan(&row.Stalliness, &row.Playstyles)
	return row, err
}

func (s *Service) GetFormatStats(ctx context.Context, month, format, rating string) (int, error) {
	var totalBattles int
	err := s.Pool.QueryRow(ctx, "SELECT total_battles FROM format_stats WHERE month = $1 AND format = $2 AND rating = $3", month, format, rating).Scan(&totalBattles)
	return totalBattles, err
}
