package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL     string
	DataRoot        string
	HostDataRoot    string
	LocalImportRoot string
	BuilderImage    string
	BuildCPUs       string
	BuildCPUShares  string
	BuildMemory     string
	BuildPIDs       string
	BuildTimeout    time.Duration
	UpdateDelay     time.Duration
	PollInterval    time.Duration
	NtfyURL         string
	NtfyTopic       string
	NtfyToken       string
	RepositoryName  string
}

func LoadConfig() (Config, error) {
	cfg := Config{
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		DataRoot:        envOr("AURFORGE_DATA_ROOT", "/var/lib/aurforge"),
		HostDataRoot:    envOr("AURFORGE_HOST_DATA_ROOT", "/srv/aurforge"),
		LocalImportRoot: envOr("AURFORGE_LOCAL_IMPORT_ROOT", "/imports"),
		BuilderImage:    envOr("AURFORGE_BUILDER_IMAGE", "aurforge-builder:latest"),
		BuildCPUs:       envOr("AURFORGE_BUILD_CPU_LIMIT", "80%"),
		BuildCPUShares:  envOr("AURFORGE_BUILD_CPU_SHARES", "256"),
		BuildMemory:     envOr("AURFORGE_BUILD_MEMORY_LIMIT", "2g"),
		BuildPIDs:       envOr("AURFORGE_BUILD_PIDS_LIMIT", "512"),
		NtfyURL:         strings.TrimRight(os.Getenv("AURFORGE_NTFY_URL"), "/"),
		NtfyTopic:       os.Getenv("AURFORGE_NTFY_TOPIC"),
		NtfyToken:       os.Getenv("AURFORGE_NTFY_TOKEN"),
		RepositoryName:  envOr("AURFORGE_REPOSITORY_NAME", "aurforge"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	var err error
	if cfg.BuildTimeout, err = durationEnv("AURFORGE_BUILD_TIMEOUT", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.UpdateDelay, err = durationEnv("AURFORGE_UPDATE_DELAY", 12*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.PollInterval, err = durationEnv("AURFORGE_POLL_INTERVAL", 30*time.Minute); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) SourceRoot() string  { return filepath.Join(c.DataRoot, "sources") }
func (c Config) StagingRoot() string { return filepath.Join(c.DataRoot, "staging") }
func (c Config) RepoRoot() string    { return filepath.Join(c.DataRoot, "repo") }
func (c Config) CacheRoot() string   { return filepath.Join(c.DataRoot, "cache") }

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func ParseIntEnv(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}
