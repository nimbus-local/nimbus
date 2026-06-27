package appsync

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Service) executeGraphQL(w http.ResponseWriter, r *http.Request) {
	apiID := s.extractAPIID(r)

	s.mu.RLock()
	api := s.apis[apiID]
	s.mu.RUnlock()
	if api == nil {
		gqlError(w, "API not found: "+apiID)
		return
	}

	if api.AuthenticationType == "API_KEY" {
		key := r.Header.Get("x-api-key")
		if key == "" {
			key = r.Header.Get("X-Api-Key")
		}
		if !s.validateAPIKey(apiID, key) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"errors": []map[string]string{{"message": "UnauthorizedException"}},
			})
			return
		}
	}

	var req struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		gqlError(w, "invalid request body")
		return
	}

	typeName, fieldName, args, err := parseGraphQL(req.Query, req.Variables)
	if err != nil {
		gqlError(w, "invalid GraphQL: "+err.Error())
		return
	}

	s.mu.RLock()
	res := s.resolvers[resolverKey(apiID, typeName, fieldName)]
	s.mu.RUnlock()
	if res == nil {
		gqlData(w, fieldName, json.RawMessage("null"))
		return
	}

	s.mu.RLock()
	ds := s.sources[apiID+"/"+res.DataSourceName]
	s.mu.RUnlock()

	var result json.RawMessage = json.RawMessage("null")

	switch {
	case ds != nil && ds.Type == "AWS_LAMBDA" && ds.LambdaConfig != nil && s.lambda != nil:
		payload, terr := evalRequestTemplate(res.RequestMappingTemplate, typeName, fieldName, args)
		if terr != nil {
			gqlError(w, "request template: "+terr.Error())
			return
		}
		fnName := lambdaFuncName(ds.LambdaConfig.LambdaFunctionArn)
		raw, ierr := s.lambda.DirectInvoke(fnName, payload)
		if ierr != nil {
			gqlError(w, "lambda: "+ierr.Error())
			return
		}
		result, err = evalResponseTemplate(res.ResponseMappingTemplate, raw)
		if err != nil {
			gqlError(w, "response template: "+err.Error())
			return
		}
	case ds != nil && ds.Type == "NONE":
		result, _ = evalResponseTemplate(res.ResponseMappingTemplate, nil)
	}

	gqlData(w, fieldName, result)
}

func (s *Service) extractAPIID(r *http.Request) string {
	p := r.URL.Path
	if strings.HasPrefix(p, "/_appsync/") {
		rest := strings.TrimPrefix(p, "/_appsync/")
		id, _, _ := strings.Cut(rest, "/")
		return id
	}
	// Host: <apiId>.appsync-api.<region>.nimbus.local[:port]
	host := r.Host
	if i := strings.LastIndex(host, ":"); i != -1 {
		host = host[:i]
	}
	id, _, _ := strings.Cut(host, ".")
	return id
}

func (s *Service) validateAPIKey(apiID, key string) bool {
	if key == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := apiID + "/"
	for k, ak := range s.apiKeys {
		if strings.HasPrefix(k, prefix) && ak.ID == key {
			return true
		}
	}
	return false
}

// evalRequestTemplate substitutes the common VTL patterns used in AppSync
// request mapping templates. Handles $util.toJson($context.arguments) and
// $context.info.fieldName; unknown VTL constructs are left as-is.
func evalRequestTemplate(tmpl, typeName, fieldName string, args map[string]interface{}) ([]byte, error) {
	if tmpl == "" {
		payload := map[string]interface{}{"field": fieldName, "args": args}
		return json.Marshal(payload)
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	argsStr := string(argsJSON)

	out := tmpl
	out = strings.ReplaceAll(out, "$util.toJson($context.arguments)", argsStr)
	out = strings.ReplaceAll(out, "$util.toJson($ctx.arguments)", argsStr)
	out = strings.ReplaceAll(out, "$context.arguments", argsStr)
	out = strings.ReplaceAll(out, "$ctx.arguments", argsStr)

	fieldJSON, _ := json.Marshal(fieldName)
	out = strings.ReplaceAll(out, "$context.info.fieldName", string(fieldJSON))
	out = strings.ReplaceAll(out, "$ctx.info.fieldName", string(fieldJSON))

	typeJSON, _ := json.Marshal(typeName)
	out = strings.ReplaceAll(out, "$context.info.parentTypeName", string(typeJSON))
	out = strings.ReplaceAll(out, "$ctx.info.parentTypeName", string(typeJSON))

	return []byte(out), nil
}

// evalResponseTemplate evaluates an AppSync response mapping template.
// Supports $util.toJson($context.result) and direct $context.result references.
func evalResponseTemplate(tmpl string, result json.RawMessage) (json.RawMessage, error) {
	t := strings.TrimSpace(tmpl)
	switch t {
	case "", "$util.toJson($context.result)", "$util.toJson($ctx.result)",
		"$context.result", "$ctx.result":
		if result == nil {
			return json.RawMessage("null"), nil
		}
		return result, nil
	}

	resultStr := "null"
	if result != nil {
		resultStr = string(result)
	}
	out := strings.ReplaceAll(t, "$util.toJson($context.result)", resultStr)
	out = strings.ReplaceAll(out, "$util.toJson($ctx.result)", resultStr)
	out = strings.ReplaceAll(out, "$context.result", resultStr)
	out = strings.ReplaceAll(out, "$ctx.result", resultStr)

	var v json.RawMessage
	if err := json.Unmarshal([]byte(out), &v); err == nil {
		return v, nil
	}
	// Wrap non-JSON output as a JSON string.
	s, _ := json.Marshal(out)
	return s, nil
}

// lambdaFuncName extracts the function name from a Lambda ARN or returns it
// unchanged if it is already a plain name.
func lambdaFuncName(arn string) string {
	if !strings.HasPrefix(arn, "arn:") {
		return arn
	}
	parts := strings.Split(arn, ":")
	if len(parts) >= 7 {
		return parts[6]
	}
	return arn
}

func gqlError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)                      // GraphQL errors still use 200
	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
		"errors": []map[string]string{{"message": msg}},
	})
}

func gqlData(w http.ResponseWriter, fieldName string, value json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
		"data": map[string]json.RawMessage{fieldName: value},
	})
}
