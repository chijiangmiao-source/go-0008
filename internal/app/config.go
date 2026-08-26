package app

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"grain-fumigation-interlock/internal/domain"
)

type Config struct {
	Address   string
	DataDir   string
	Anchor    time.Time
	Standards domain.Standards
}

func LoadConfig() Config {
	addr := env("GFI_ADDR", ":8080")
	data := env("GFI_DATA_DIR", filepath.Join(os.TempDir(), "grain-fumigation-interlock"))
	anchor := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	if raw := os.Getenv("GFI_ANCHOR_UNIX"); raw != "" {
		if sec, err := strconv.ParseInt(raw, 10, 64); err == nil {
			anchor = time.Unix(sec, 0).UTC()
		}
	}
	return Config{Address: addr, DataDir: data, Anchor: anchor, Standards: domain.Standards{Versions: domain.DefaultStandards(anchor)}}
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
