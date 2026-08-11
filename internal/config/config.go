package config

import (
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	DatabaseURL string `envconfig:"DATABASE_URL" default:"postgres://ubuntu@/smogon_stats?host=/var/run/postgresql"`
	Port        string `envconfig:"PORT" default:":9000"`
	AdminToken  string `envconfig:"ADMIN_TOKEN"`
	SmogonURL   string `envconfig:"SMOGON_URL" default:"https://www.smogon.com/stats"`
}

func Load() (*Config, error) {
	var cfg Config
	err := envconfig.Process("", &cfg)
	if err != nil {
		return nil, err
	}
	if len(cfg.Port) > 0 && cfg.Port[0] != ':' {
		cfg.Port = ":" + cfg.Port
	}
	return &cfg, nil
}
