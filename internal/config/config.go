package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr         string
	StoragePath  string
	MaxFileSize  int64
	MaxChunkSize int64
	TransferTTL  time.Duration
}

func Load() Config {
	return Config{
		Addr:         getEnv("QANAL_ADDR", ":8080"),
		StoragePath:  getEnv("QANAL_STORAGE", "./data"),
		MaxFileSize:  getEnvInt64("QANAL_MAX_FILE_SIZE", 100*1024*1024*1024), // 100 GB
		MaxChunkSize: getEnvInt64("QANAL_MAX_CHUNK_SIZE", 500*1024*1024),     // 500 MB
		TransferTTL:  getEnvDuration("QANAL_TRANSFER_TTL", 24*time.Hour),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
