package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port             int
	DataDir          string
	DefaultRegion    string
	DynamoDBEndpoint string
	LogLevel         string
	Services         string
	PostgresHost     string
	PostgresPort     int
}

func Load() Config {
	c := Config{
		Port:             4566,
		DataDir:          envOr("NIMBUS_DATA_DIR", defaultDataDir()),
		DefaultRegion:    envOr("AWS_DEFAULT_REGION", "us-east-1"),
		DynamoDBEndpoint: envOr("NIMBUS_DYNAMODB_ENDPOINT", "http://dynamodb-local:8000"),
		LogLevel:         envOr("NIMBUS_LOG_LEVEL", "info"),
		Services:         envOr("SERVICES", ""),
		PostgresHost:     envOr("NIMBUS_POSTGRES_HOST", "postgres"),
		PostgresPort:     5432,
	}

	if portStr := os.Getenv("NIMBUS_PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			c.Port = p
		}
	}
	if portStr := os.Getenv("NIMBUS_POSTGRES_PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			c.PostgresPort = p
		}
	}

	return c
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultDataDir() string {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "/var/lib/nimbus"
	}
	return ".nimbus"
}
