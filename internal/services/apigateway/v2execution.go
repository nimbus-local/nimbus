package apigateway

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/uid"
)

// executeV2 handles /apis/{apiId}/{stage}/_user_request_/{proxy+}.
func (s *Service) executeV2(w http.ResponseWriter, r *http.Request, apiID, stageName, proxyPath string) {
	if _, ok := s.v2.getAPI(apiID); !ok {
		jsonhttp.Error(w, http.StatusNotFound, "NotFoundException", "HTTP API not found: "+apiID)
		return
	}
	if _, ok := s.v2.getStage(apiID, stageName); !ok {
		jsonhttp.Error(w, http.StatusNotFound, "NotFoundException", "Stage not found: "+stageName)
		return
	}

	route, pathParams, ok := s.v2.findRouteForRequest(apiID, r.Method, proxyPath)
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "NotFoundException",
			"No route matching "+r.Method+" "+proxyPath)
		return
	}

	if route.Target == "" {
		jsonhttp.Error(w, http.StatusInternalServerError, "InternalServerErrorException",
			"Route has no integration target")
		return
	}

	integ, ok := s.v2.integrationForRoute(apiID, route)
	if !ok {
		jsonhttp.Error(w, http.StatusInternalServerError, "InternalServerErrorException",
			"Integration not found for route "+route.RouteKey)
		return
	}

	switch strings.ToUpper(integ.IntegrationType) {
	case "AWS_PROXY":
		s.executeLambdaProxyV2(w, r, apiID, stageName, route, pathParams, proxyPath, integ)
	default:
		jsonhttp.Error(w, http.StatusNotImplemented, "NotImplementedException",
			"Integration type "+integ.IntegrationType+" is not supported")
	}
}

// v2ProxyEvent is the payload sent to Lambda for HTTP API AWS_PROXY integrations
// with payloadFormatVersion "2.0".
type v2ProxyEvent struct {
	Version               string            `json:"version"`
	RouteKey              string            `json:"routeKey"`
	RawPath               string            `json:"rawPath"`
	RawQueryString        string            `json:"rawQueryString"`
	Cookies               []string          `json:"cookies,omitempty"`
	Headers               map[string]string `json:"headers"`
	QueryStringParameters map[string]string `json:"queryStringParameters,omitempty"`
	PathParameters        map[string]string `json:"pathParameters,omitempty"`
	StageVariables        map[string]string `json:"stageVariables,omitempty"`
	Body                  *string           `json:"body"`
	IsBase64Encoded       bool              `json:"isBase64Encoded"`
	RequestContext        v2RequestContext  `json:"requestContext"`
}

type v2RequestContext struct {
	AccountId    string    `json:"accountId"`
	ApiId        string    `json:"apiId"`
	DomainName   string    `json:"domainName"`
	DomainPrefix string    `json:"domainPrefix"`
	Http         v2HttpCtx `json:"http"`
	RequestId    string    `json:"requestId"`
	RouteKey     string    `json:"routeKey"`
	Stage        string    `json:"stage"`
	Time         string    `json:"time"`
	TimeEpoch    int64     `json:"timeEpoch"`
}

type v2HttpCtx struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	Protocol  string `json:"protocol"`
	SourceIp  string `json:"sourceIp"`
	UserAgent string `json:"userAgent"`
}

func (s *Service) executeLambdaProxyV2(
	w http.ResponseWriter,
	r *http.Request,
	apiID, stageName string,
	route *V2Route,
	pathParams map[string]string,
	proxyPath string,
	integ *V2Integration,
) {
	functionName := lambdaFunctionFromURI(integ.IntegrationUri)
	if functionName == "" {
		jsonhttp.Error(w, http.StatusInternalServerError, "InternalServerErrorException",
			"Could not resolve Lambda function from URI: "+integ.IntegrationUri)
		return
	}

	// Use v1 format if explicitly requested; default to v2.
	if integ.PayloadFormatVersion == "1.0" {
		s.executeLambdaProxyV2AsV1(w, r, apiID, stageName, route, pathParams, proxyPath, functionName)
		return
	}

	// Build body
	var body *string
	var isBase64 bool
	if r.Body != nil && r.ContentLength != 0 {
		raw, err := io.ReadAll(r.Body)
		if err == nil && len(raw) > 0 {
			if isText(raw) {
				s := string(raw)
				body = &s
			} else {
				encoded := base64.StdEncoding.EncodeToString(raw)
				body = &encoded
				isBase64 = true
			}
		}
	}

	// Headers (single-value)
	headers := make(map[string]string, len(r.Header))
	for k, vs := range r.Header {
		headers[strings.ToLower(k)] = vs[0]
	}

	// Cookies
	var cookies []string
	for _, c := range r.Cookies() {
		cookies = append(cookies, c.Name+"="+c.Value)
	}

	// Query string
	qsp := map[string]string{}
	for k, vs := range r.URL.Query() {
		qsp[k] = strings.Join(vs, ",")
	}
	if len(qsp) == 0 {
		qsp = nil
	}

	stage, _ := s.v2.getStage(apiID, stageName)
	var stageVars map[string]string
	if stage != nil {
		stageVars = stage.StageVariables
	}

	now := time.Now().UTC()
	sourceIP := r.RemoteAddr
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		sourceIP = strings.SplitN(ip, ",", 2)[0]
	}

	event := v2ProxyEvent{
		Version:               "2.0",
		RouteKey:              route.RouteKey,
		RawPath:               proxyPath,
		RawQueryString:        r.URL.RawQuery,
		Cookies:               cookies,
		Headers:               headers,
		QueryStringParameters: qsp,
		PathParameters:        pathParams,
		StageVariables:        stageVars,
		Body:                  body,
		IsBase64Encoded:       isBase64,
		RequestContext: v2RequestContext{
			AccountId:    defaultAccount,
			ApiId:        apiID,
			DomainName:   "localhost",
			DomainPrefix: "localhost",
			Http: v2HttpCtx{
				Method:    r.Method,
				Path:      proxyPath,
				Protocol:  r.Proto,
				SourceIp:  sourceIP,
				UserAgent: r.UserAgent(),
			},
			RequestId: uid.New(),
			RouteKey:  route.RouteKey,
			Stage:     stageName,
			Time:      now.Format("02/Jan/2006:15:04:05 -0700"),
			TimeEpoch: now.UnixMilli(),
		},
	}

	payload, err := json.Marshal(event)
	if err != nil {
		jsonhttp.Error(w, http.StatusInternalServerError, "InternalServerErrorException",
			"failed to marshal Lambda event: "+err.Error())
		return
	}

	respPayload, err := s.lambda.DirectInvoke(functionName, payload)
	if err != nil {
		jsonhttp.Error(w, http.StatusBadGateway, "InternalServerErrorException",
			"Lambda invocation failed: "+err.Error())
		return
	}

	writeLambdaProxyResponse(w, respPayload)
}

// executeLambdaProxyV2AsV1 sends a v1 proxy event for integrations with payloadFormatVersion "1.0".
func (s *Service) executeLambdaProxyV2AsV1(
	w http.ResponseWriter,
	r *http.Request,
	apiID, stageName string,
	route *V2Route,
	pathParams map[string]string,
	proxyPath, functionName string,
) {
	_, routePath, _ := parseRouteKey(route.RouteKey)

	headers := make(map[string]string, len(r.Header))
	for k, vs := range r.Header {
		headers[k] = vs[0]
	}

	qsp := map[string]string{}
	for k, vs := range r.URL.Query() {
		qsp[k] = vs[0]
	}
	if len(qsp) == 0 {
		qsp = nil
	}

	stage, _ := s.v2.getStage(apiID, stageName)
	var stageVars map[string]string
	if stage != nil {
		stageVars = stage.StageVariables
	}

	var body *string
	var isBase64 bool
	if r.Body != nil && r.ContentLength != 0 {
		raw, err := io.ReadAll(r.Body)
		if err == nil && len(raw) > 0 {
			if isText(raw) {
				b := string(raw)
				body = &b
			} else {
				encoded := base64.StdEncoding.EncodeToString(raw)
				body = &encoded
				isBase64 = true
			}
		}
	}

	event := lambdaProxyEvent{
		Version:               "1.0",
		Resource:              routePath,
		Path:                  proxyPath,
		HttpMethod:            r.Method,
		Headers:               headers,
		QueryStringParameters: qsp,
		PathParameters:        pathParams,
		StageVariables:        stageVars,
		Body:                  body,
		IsBase64Encoded:       isBase64,
		RequestContext: map[string]any{
			"accountId":    defaultAccount,
			"apiId":        apiID,
			"httpMethod":   r.Method,
			"path":         proxyPath,
			"resourcePath": routePath,
			"stage":        stageName,
			"requestId":    uid.New(),
		},
	}

	payload, err := json.Marshal(event)
	if err != nil {
		jsonhttp.Error(w, http.StatusInternalServerError, "InternalServerErrorException",
			"failed to marshal Lambda event: "+err.Error())
		return
	}

	respPayload, err := s.lambda.DirectInvoke(functionName, payload)
	if err != nil {
		jsonhttp.Error(w, http.StatusBadGateway, "InternalServerErrorException",
			"Lambda invocation failed: "+err.Error())
		return
	}

	writeLambdaProxyResponse(w, respPayload)
}

// writeLambdaProxyResponse decodes a Lambda proxy response and writes it to w.
// Shared by both v1 and v2 execute paths since the response shape is the same.
func writeLambdaProxyResponse(w http.ResponseWriter, respPayload []byte) {
	var lambdaResp lambdaProxyResponse
	if err := json.Unmarshal(respPayload, &lambdaResp); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(respPayload)
		return
	}

	for k, v := range lambdaResp.Headers {
		w.Header().Set(k, v)
	}
	for k, vs := range lambdaResp.MultiValueHeaders {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}

	status := lambdaResp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)

	if lambdaResp.Body != "" {
		if lambdaResp.IsBase64Encoded {
			if decoded, err := base64.StdEncoding.DecodeString(lambdaResp.Body); err == nil {
				w.Write(decoded)
				return
			}
		}
		w.Write([]byte(lambdaResp.Body))
	}
}
