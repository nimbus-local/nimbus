package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/nimbus-local/nimbus/internal/config"
	"github.com/nimbus-local/nimbus/internal/router"
	"github.com/nimbus-local/nimbus/internal/services/acm"
	"github.com/nimbus-local/nimbus/internal/services/alb"
	"github.com/nimbus-local/nimbus/internal/services/apigateway"
	"github.com/nimbus-local/nimbus/internal/services/appsync"
	"github.com/nimbus-local/nimbus/internal/services/cloudfront"
	"github.com/nimbus-local/nimbus/internal/services/cloudwatchlogs"
	"github.com/nimbus-local/nimbus/internal/services/cloudwatchmetrics"
	"github.com/nimbus-local/nimbus/internal/services/cognito"
	"github.com/nimbus-local/nimbus/internal/services/dynamodb"
	"github.com/nimbus-local/nimbus/internal/services/ec2"
	"github.com/nimbus-local/nimbus/internal/services/ecr"
	"github.com/nimbus-local/nimbus/internal/services/ecs"
	"github.com/nimbus-local/nimbus/internal/services/efs"
	"github.com/nimbus-local/nimbus/internal/services/elasticache"
	"github.com/nimbus-local/nimbus/internal/services/eventbridge"
	"github.com/nimbus-local/nimbus/internal/services/iam"
	"github.com/nimbus-local/nimbus/internal/services/kinesis"
	"github.com/nimbus-local/nimbus/internal/services/kms"
	"github.com/nimbus-local/nimbus/internal/services/lambda"
	"github.com/nimbus-local/nimbus/internal/services/pi"
	"github.com/nimbus-local/nimbus/internal/services/rds"
	"github.com/nimbus-local/nimbus/internal/services/route53"
	"github.com/nimbus-local/nimbus/internal/services/s3"
	"github.com/nimbus-local/nimbus/internal/services/s3control"
	"github.com/nimbus-local/nimbus/internal/services/scheduler"
	"github.com/nimbus-local/nimbus/internal/services/secretsmanager"
	"github.com/nimbus-local/nimbus/internal/services/ses"
	"github.com/nimbus-local/nimbus/internal/services/sfn"
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
	acmSvc := acm.New(cfg.DefaultRegion)
	r.Register(acmSvc)
	cfSvc := cloudfront.New(cfg.DefaultRegion)
	r.Register(cfSvc)
	iamSvc := iam.New()
	r.Register(iamSvc)
	cwlSvc := cloudwatchlogs.New(cfg.DefaultRegion)
	r.Register(cwlSvc)
	cwmSvc := cloudwatchmetrics.New(cfg.DefaultRegion)
	r.Register(cwmSvc)
	r.Register(dynamodb.New(cfg.DynamoDBEndpoint, logger))
	lambdaSvc := lambda.New(cfg.DefaultRegion)
	// Container-image functions run as real containers when Docker is reachable.
	lambdaSvc.EnableContainers(cfg.DataDir)
	r.Register(lambdaSvc)
	appSyncSvc := appsync.New(cfg.DefaultRegion, lambdaSvc.Invocation)
	r.Register(appSyncSvc)
	apiGwSvc := apigateway.New(cfg.DefaultRegion, lambdaSvc.Invocation)
	r.Register(apiGwSvc)
	sesSvc := ses.New(cfg.DefaultRegion)
	r.Register(sesSvc)
	ecrSvc := ecr.New(cfg.DefaultRegion)
	r.Register(ecrSvc)
	ecsSvc := ecs.New(cfg.DefaultRegion)
	r.Register(ecsSvc)
	smSvc := secretsmanager.New(cfg.DefaultRegion)
	r.Register(smSvc)
	kmsSvc := kms.New(cfg.DefaultRegion)
	r.Register(kmsSvc)
	ssmSvc := ssm.New(cfg.DefaultRegion)
	r.Register(ssmSvc)
	sqsSvc := sqs.New(cfg.DefaultRegion, cfg.Port, cfg.ExternalURL)
	r.Register(sqsSvc)
	snsSvc := sns.New(cfg.DefaultRegion)
	r.Register(snsSvc)
	nimbusBaseURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	kinesisSvc := kinesis.New(cfg.DefaultRegion)
	r.Register(kinesisSvc)
	kinesisSvc.StartESMRunner(func() []kinesis.ESMInfo {
		raw := lambdaSvc.EventSources.ListKinesisESMs()
		out := make([]kinesis.ESMInfo, len(raw))
		for i, m := range raw {
			bs := m.BatchSize
			if bs <= 0 {
				bs = 100
			}
			out[i] = kinesis.ESMInfo{
				UUID:             m.UUID,
				FunctionName:     m.FunctionName,
				EventSourceArn:   m.EventSourceArn,
				BatchSize:        bs,
				StartingPosition: m.StartingPosition,
			}
		}
		return out
	}, nimbusBaseURL)
	schedSvc := scheduler.New(cfg.DefaultRegion, nimbusBaseURL)
	r.Register(schedSvc)
	ec2Svc := ec2.New(cfg.DefaultRegion)
	albSvc := alb.New(cfg.DefaultRegion, ec2Svc.SubnetAZ)
	r.Register(albSvc)
	rdsSvc := rds.New(cfg.DefaultRegion, cfg.PostgresHost, cfg.PostgresPort)
	r.Register(rdsSvc)
	piSvc := pi.New(rdsSvc.HasResourceID)
	r.Register(piSvc)
	r53Svc := route53.New()
	r.Register(r53Svc)
	ecSvc := elasticache.New(cfg.DefaultRegion, cfg.ValkeyHost, cfg.ValkeyPort)
	r.Register(ecSvc)
	ebSvc := eventbridge.New(cfg.DefaultRegion)
	r.Register(ebSvc)
	cognitoSvc := cognito.New(cfg.DefaultRegion)
	r.Register(cognitoSvc)
	sfnSvc := sfn.New(cfg.DefaultRegion, nimbusBaseURL)
	r.Register(sfnSvc)
	efsSvc := efs.New(cfg.DefaultRegion)
	r.Register(efsSvc) // EFS (/2015-02-01/) must precede the S3 catch-all
	s3ControlSvc := s3control.New()
	r.Register(s3ControlSvc) // S3 Control (/v20180820/) must precede the S3 catch-all
	r.Register(ec2Svc)       // EC2 (POST / form-encoded) must precede the S3 catch-all
	s3Svc := s3.New(cfg.DataDir)
	r.Register(s3Svc) // S3 is the catch-all, register last

	// Standard endpoints
	mux := http.NewServeMux()
	healthHandler := func(w http.ResponseWriter, req *http.Request) {
		type healthResponse struct {
			Status     string   `json:"status"`
			Services   []string `json:"services"`
			ActiveESMs int      `json:"active_esms"`
		}
		resp := healthResponse{
			Status:     "running",
			Services:   r.ServiceNames(),
			ActiveESMs: lambdaSvc.EventSources.ActiveCount(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
	mux.HandleFunc("/_nimbus/health", healthHandler)
	mux.HandleFunc("/_localstack/health", healthHandler) // LocalStack-compatible alias

	// CloudWatch Logs inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/logs/", cwlSvc.LogsHandler)

	// CloudWatch Metrics inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/metrics", cwmSvc.MetricsHandler)

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

	// Lambda invocations inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/lambda/invocations", lambdaSvc.Invocation.InvocationsHandler)

	// Lambda live function registration — not AWS API, Nimbus-specific (forge dev tunnel)
	mux.HandleFunc("/_nimbus/lambda/register", lambdaSvc.Invocation.RegisterHandler)
	mux.HandleFunc("/_nimbus/lambda/register/", lambdaSvc.Invocation.RegisterHandler)

	// Running container-image function containers
	mux.HandleFunc("/_nimbus/lambda/containers", lambdaSvc.Invocation.ContainersHandler)

	// AppSync inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/appsync/apis", appSyncSvc.APIsHandler)

	// ACM inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/acm/certs/", acmSvc.CertHandler)

	// CloudFront inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/cloudfront/distributions", cfSvc.DistributionsHandler)

	// RDS inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/rds/clusters", rdsSvc.ClustersHandler)

	// ElastiCache inspection endpoint — not AWS API, Nimbus-specific
	mux.HandleFunc("/_nimbus/elasticache/clusters", ecSvc.ClustersHandler)

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

	// /_nimbus/state — dump in-memory state counts for all services
	mux.HandleFunc("/_nimbus/state", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		type stateResponse struct {
			Functions           []string          `json:"functions"`
			LiveEndpoints       map[string]string `json:"live_endpoints"`
			EventSourceMappings int               `json:"event_source_mappings"`
			SNSTopics           int               `json:"sns_topics"`
			SQSQueues           int               `json:"sqs_queues"`
			SSMParameters       int               `json:"ssm_parameters"`
			KinesisStreams      int               `json:"kinesis_streams"`
			SchedulerSchedules  int               `json:"scheduler_schedules"`
			EventBridgeBuses    int               `json:"eventbridge_buses"`
			SFNStateMachines    int               `json:"sfn_state_machines"`
			Secrets             int               `json:"secrets"`
			EFSFileSystems      int               `json:"efs_file_systems"`
		}
		resp := stateResponse{
			Functions:           lambdaSvc.CRUD.FunctionNames(),
			LiveEndpoints:       lambdaSvc.Invocation.LiveEndpoints(),
			EventSourceMappings: lambdaSvc.EventSources.ActiveCount(),
			SNSTopics:           snsSvc.TopicCount(),
			SQSQueues:           sqsSvc.QueueCount(),
			SSMParameters:       ssmSvc.ParameterCount(),
			KinesisStreams:      kinesisSvc.StreamCount(),
			SchedulerSchedules:  schedSvc.ScheduleCount(),
			EventBridgeBuses:    ebSvc.BusCount(),
			SFNStateMachines:    sfnSvc.StateMachineCount(),
			Secrets:             smSvc.SecretCount(),
			EFSFileSystems:      efsSvc.FileSystemCount(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	})

	// /_nimbus/reset — clear all in-memory state across every service
	mux.HandleFunc("/_nimbus/reset", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.ResetAll() // calls Reset() on every registered service
		w.WriteHeader(http.StatusNoContent)
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
	// Function containers are children of this process in spirit, not in the
	// process tree — nothing else will reap them.
	lambdaSvc.Invocation.StopContainers()
}
