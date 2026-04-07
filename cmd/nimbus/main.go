package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/nimbus-local/nimbus/internal/config"
	"github.com/nimbus-local/nimbus/internal/router"
	"github.com/nimbus-local/nimbus/internal/services/dynamodb"
	"github.com/nimbus-local/nimbus/internal/services/s3"
	"github.com/nimbus-local/nimbus/internal/services/secretsmanager"
	"github.com/nimbus-local/nimbus/internal/services/ses"
	"github.com/nimbus-local/nimbus/internal/services/sqs"
	"github.com/nimbus-local/nimbus/internal/services/ssm"
)

func main() {
	cfg := config.Load()

	// Configure structured logging
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		logger.Error("failed to create data directory", "path", cfg.DataDir, "err", err)
		os.Exit(1)
	}

	logger.Info("starting Nimbus",
		"port", cfg.Port,
		"data_dir", cfg.DataDir,
		"region", cfg.DefaultRegion,
	)

	// Build the edge router
	r := router.New(logger)

	// Register services — order matters: more specific detectors first
	r.Register(dynamodb.New(cfg.DynamoDBEndpoint, logger))
	sesSvc := ses.New(cfg.DefaultRegion)
	r.Register(sesSvc)
	r.Register(secretsmanager.New(cfg.DefaultRegion))
	r.Register(ssm.New(cfg.DefaultRegion))
	r.Register(sqs.New(cfg.DefaultRegion))
	r.Register(s3.New(cfg.DataDir)) // S3 is the catch-all, register last

	// Standard endpoints
	mux := http.NewServeMux()
	mux.HandleFunc("/_nimbus/health", r.HealthHandler)
	mux.HandleFunc("/_localstack/health", r.HealthHandler) // LocalStack-compatible alias

	// SES inspection endpoints — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/ses/messages", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			sesSvc.MessagesHandler(w, req)
		case http.MethodDelete:
			sesSvc.ClearMessagesHandler(w, req)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.Handle("/", r)

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("Nimbus is ready", "endpoint", fmt.Sprintf("http://localhost:%d", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-done
	logger.Info("shutting down")
}
