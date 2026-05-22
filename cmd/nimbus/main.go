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
	"github.com/nimbus-local/nimbus/internal/services/alb"
	"github.com/nimbus-local/nimbus/internal/services/apigateway"
	"github.com/nimbus-local/nimbus/internal/services/cloudfront"
	"github.com/nimbus-local/nimbus/internal/services/cloudwatchlogs"
	"github.com/nimbus-local/nimbus/internal/services/dynamodb"
	"github.com/nimbus-local/nimbus/internal/services/ecr"
	"github.com/nimbus-local/nimbus/internal/services/ecs"
	"github.com/nimbus-local/nimbus/internal/services/eventbridge"
	"github.com/nimbus-local/nimbus/internal/services/iam"
	"github.com/nimbus-local/nimbus/internal/services/kms"
	"github.com/nimbus-local/nimbus/internal/services/lambda"
	"github.com/nimbus-local/nimbus/internal/services/rds"
	"github.com/nimbus-local/nimbus/internal/services/s3"
	"github.com/nimbus-local/nimbus/internal/services/scheduler"
	"github.com/nimbus-local/nimbus/internal/services/secretsmanager"
	"github.com/nimbus-local/nimbus/internal/services/ses"
	"github.com/nimbus-local/nimbus/internal/services/sns"
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
	cfSvc := cloudfront.New(cfg.DefaultRegion)
	r.Register(cfSvc)
	r.Register(iam.New())
	cwlSvc := cloudwatchlogs.New(cfg.DefaultRegion)
	r.Register(cwlSvc)
	r.Register(dynamodb.New(cfg.DynamoDBEndpoint, logger))
	lambdaSvc := lambda.New(cfg.DefaultRegion)
	r.Register(lambdaSvc)
	r.Register(apigateway.New(cfg.DefaultRegion, lambdaSvc.Invocation))
	sesSvc := ses.New(cfg.DefaultRegion)
	r.Register(sesSvc)
	r.Register(ecr.New(cfg.DefaultRegion))
	ecsSvc := ecs.New(cfg.DefaultRegion)
	r.Register(ecsSvc)
	r.Register(secretsmanager.New(cfg.DefaultRegion))
	r.Register(kms.New(cfg.DefaultRegion))
	r.Register(ssm.New(cfg.DefaultRegion))
	r.Register(sqs.New(cfg.DefaultRegion))
	snsSvc := sns.New(cfg.DefaultRegion)
	r.Register(snsSvc)
	schedSvc := scheduler.New(cfg.DefaultRegion, fmt.Sprintf("http://127.0.0.1:%d", cfg.Port))
	r.Register(schedSvc)
	albSvc := alb.New(cfg.DefaultRegion)
	r.Register(albSvc)
	rdsSvc := rds.New(cfg.DefaultRegion, cfg.PostgresHost, cfg.PostgresPort)
	r.Register(rdsSvc)
	ebSvc := eventbridge.New(cfg.DefaultRegion)
	r.Register(ebSvc)
	r.Register(s3.New(cfg.DataDir)) // S3 is the catch-all, register last

	// Standard endpoints
	mux := http.NewServeMux()
	mux.HandleFunc("/_nimbus/health", r.HealthHandler)
	mux.HandleFunc("/_localstack/health", r.HealthHandler) // LocalStack-compatible alias

	// CloudWatch Logs inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/logs/", cwlSvc.LogsHandler)

	// ECS inspection endpoints — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/ecs/tasks/", ecsSvc.LogsHandler)

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

	// SNS inspection endpoints — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/sns/messages", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			snsSvc.MessagesHandler(w, req)
		case http.MethodDelete:
			snsSvc.ClearMessagesHandler(w, req)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// CloudFront inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/cloudfront/distributions", cfSvc.DistributionsHandler)

	// RDS inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/rds/clusters", rdsSvc.ClustersHandler)

	// ALB inspection endpoints — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/alb/loadbalancers", albSvc.LoadBalancersHandler)
	mux.HandleFunc("/_nimbus/alb/targetgroups", albSvc.TargetGroupsHandler)
	mux.HandleFunc("/_nimbus/alb/listeners", albSvc.ListenersHandler)

	// Scheduler inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/scheduler/schedules", schedSvc.SchedulesHandler)

	// EventBridge inspection endpoints — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/eventbridge/events", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			ebSvc.EventsHandler(w, req)
		case http.MethodDelete:
			ebSvc.ClearEventsHandler(w, req)
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
