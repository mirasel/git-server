package main

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port           string
	Host           string
	RepoDir        string
	BackupDir      string
	InternalServer string
	HTTPTimeout    time.Duration
	SSHKeyPath     string
}

func loadConfig() Config {
	return Config{
		Port:           getEnvOrDefault("GIT_SERVER_PORT", "2222"),
		Host:           getEnvOrDefault("GIT_SERVER_HOST", "0.0.0.0"),
		RepoDir:        getEnvOrDefault("GIT_SERVER_REPO_DIR", "repos"),
		BackupDir:      getEnvOrDefault("GIT_SERVER_BACKUP_DIR", "repo_backups"),
		InternalServer: getEnvOrDefault("GIT_SERVER_AUTHORIZATION_SERVER_URL", "http://0.0.0.0:3000"),
		HTTPTimeout:    getDurationEnvOrDefault("GIT_SERVER_HTTP_TIMEOUT", 10*time.Second),
		SSHKeyPath:     getEnvOrDefault("GIT_SERVER_SSH_KEY_PATH", ".ssh/id_ed25519"),
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getDurationEnvOrDefault(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil {
			return time.Duration(seconds) * time.Second
		}
	}
	return defaultValue
}
