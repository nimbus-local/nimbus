package scheduler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// invokeTarget dispatches a schedule firing to the configured target.
// It runs in a goroutine so the ticker mutex is never held during the HTTP call.
func (s *Service) invokeTarget(name string, targetJSON []byte) {
	var tgt struct {
		Arn   string `json:"Arn"`
		Input string `json:"Input"`
	}
	if err := json.Unmarshal(targetJSON, &tgt); err != nil || tgt.Arn == "" {
		slog.Warn("scheduler: cannot parse target", "schedule", name, "err", err)
		return
	}

	// arn:aws:{service}:{region}:{account}:{resource}
	parts := strings.SplitN(tgt.Arn, ":", 6)
	if len(parts) < 6 {
		slog.Warn("scheduler: malformed target ARN", "schedule", name, "arn", tgt.Arn)
		return
	}
	service := parts[2]

	switch service {
	case "lambda":
		s.invokeLambdaTarget(name, tgt.Arn, tgt.Input)
	default:
		slog.Info("scheduler: target type not yet supported, skipping",
			"service", service, "schedule", name, "arn", tgt.Arn)
	}
}

// invokeLambdaTarget posts to the Nimbus Lambda invoke endpoint.
// ARN format: arn:aws:lambda:{region}:{account}:function:{name}[:{qualifier}]
func (s *Service) invokeLambdaTarget(scheduleName, arn, input string) {
	arnParts := strings.Split(arn, ":")
	if len(arnParts) < 7 || arnParts[5] != "function" {
		slog.Warn("scheduler: invalid lambda ARN", "schedule", scheduleName, "arn", arn)
		return
	}
	fnName := arnParts[6]
	if len(arnParts) >= 8 {
		fnName += ":" + arnParts[7]
	}

	if input == "" {
		input = "{}"
	}

	url := fmt.Sprintf("%s/2015-03-31/functions/%s/invocations", s.baseURL, fnName)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(input))
	if err != nil {
		slog.Warn("scheduler: failed to build lambda request", "schedule", scheduleName, "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// Use Event (async) invocation type matching real scheduler behaviour.
	req.Header.Set("X-Amz-Invocation-Type", "Event")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("scheduler: lambda invocation failed", "schedule", scheduleName, "fn", fnName, "err", err)
		return
	}
	defer resp.Body.Close()
	// Drain body so connection is reusable.
	io.Copy(io.Discard, resp.Body)

	slog.Info("scheduler: lambda invoked", "schedule", scheduleName, "fn", fnName, "status", resp.StatusCode)
}
