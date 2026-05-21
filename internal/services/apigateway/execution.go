package apigateway

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/uid"
)

// execute handles /restapis/{apiId}/{stage}/_user_request_/{proxy+}.
func (s *Service) execute(w http.ResponseWriter, r *http.Request, apiID, stageName, proxyPath string) {
	if _, ok := s.db.getAPI(apiID); !ok {
		jsonhttp.Error(w, http.StatusNotFound, "NotFoundException", "REST API not found: "+apiID)
		return
	}
	if _, ok := s.db.getStage(apiID, stageName); !ok {
		jsonhttp.Error(w, http.StatusNotFound, "NotFoundException", "Stage not found: "+stageName)
		return
	}

	resource, pathParams, ok := s.db.findResourceForPath(apiID, proxyPath)
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "NotFoundException",
			"No resource matching path: "+proxyPath)
		return
	}

	httpMethod := r.Method
	method, ok := resource.ResourceMethods[httpMethod]
	if !ok {
		// Try ANY
		method, ok = resource.ResourceMethods["ANY"]
		if !ok {
			jsonhttp.Error(w, http.StatusMethodNotAllowed, "MethodNotAllowedException",
				"HTTP method "+httpMethod+" not configured on resource "+resource.Path)
			return
		}
	}

	if method.MethodIntegration == nil {
		jsonhttp.Error(w, http.StatusInternalServerError, "InternalServerErrorException",
			"No integration configured for method "+httpMethod+" on "+resource.Path)
		return
	}

	switch strings.ToUpper(method.MethodIntegration.Type) {
	case "AWS_PROXY":
		s.executeLambdaProxy(w, r, apiID, stageName, resource, pathParams, method)
	case "MOCK":
		s.executeMock(w, method)
	default:
		jsonhttp.Error(w, http.StatusNotImplemented, "NotImplementedException",
			"Integration type "+method.MethodIntegration.Type+" is not supported")
	}
}

// lambdaProxyEvent is the event shape sent to Lambda for AWS_PROXY integrations.
type lambdaProxyEvent struct {
	Version               string            `json:"version"`
	Resource              string            `json:"resource"`
	Path                  string            `json:"path"`
	HttpMethod            string            `json:"httpMethod"`
	Headers               map[string]string `json:"headers"`
	QueryStringParameters map[string]string `json:"queryStringParameters"`
	PathParameters        map[string]string `json:"pathParameters"`
	StageVariables        map[string]string `json:"stageVariables"`
	Body                  *string           `json:"body"`
	IsBase64Encoded       bool              `json:"isBase64Encoded"`
	RequestContext        map[string]any    `json:"requestContext"`
}

type lambdaProxyResponse struct {
	StatusCode        int                 `json:"statusCode"`
	Headers           map[string]string   `json:"headers"`
	MultiValueHeaders map[string][]string `json:"multiValueHeaders"`
	Body              string              `json:"body"`
	IsBase64Encoded   bool                `json:"isBase64Encoded"`
}

func (s *Service) executeLambdaProxy(
	w http.ResponseWriter,
	r *http.Request,
	apiID, stageName string,
	resource *Resource,
	pathParams map[string]string,
	method *Method,
) {
	functionName := lambdaFunctionFromURI(method.MethodIntegration.Uri)
	if functionName == "" {
		jsonhttp.Error(w, http.StatusInternalServerError, "InternalServerErrorException",
			"Could not resolve Lambda function from URI: "+method.MethodIntegration.Uri)
		return
	}

	// Read body
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

	// Build headers map (single-value)
	headers := make(map[string]string, len(r.Header))
	for k, vs := range r.Header {
		headers[k] = vs[0]
	}

	// Build query string map
	qsp := map[string]string{}
	for k, vs := range r.URL.Query() {
		qsp[k] = vs[0]
	}
	if len(qsp) == 0 {
		qsp = nil
	}

	stage, _ := s.db.getStage(apiID, stageName)
	var stageVars map[string]string
	if stage != nil {
		stageVars = stage.Variables
	}

	event := lambdaProxyEvent{
		Version:               "1.0",
		Resource:              resource.Path,
		Path:                  r.URL.Path,
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
			"path":         r.URL.Path,
			"resourcePath": resource.Path,
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

	var lambdaResp lambdaProxyResponse
	if err := json.Unmarshal(respPayload, &lambdaResp); err != nil {
		// If the Lambda returned non-proxy-format JSON, pass it through as 200.
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
			decoded, err := base64.StdEncoding.DecodeString(lambdaResp.Body)
			if err == nil {
				w.Write(decoded)
				return
			}
		}
		w.Write([]byte(lambdaResp.Body))
	}
}

// executeMock returns a MOCK integration response (default 200 empty body).
func (s *Service) executeMock(w http.ResponseWriter, method *Method) {
	integ := method.MethodIntegration
	// Find the default response (status code "200" or first one)
	statusCode := 200
	var body string
	if integ.IntegrationResponses != nil {
		if ir, ok := integ.IntegrationResponses["200"]; ok {
			statusCode, _ = strconv.Atoi(ir.StatusCode)
			if ir.ResponseTemplates != nil {
				if b, ok := ir.ResponseTemplates["application/json"]; ok {
					body = b
				}
			}
		} else {
			for sc, ir := range integ.IntegrationResponses {
				statusCode, _ = strconv.Atoi(sc)
				if ir.ResponseTemplates != nil {
					if b, ok := ir.ResponseTemplates["application/json"]; ok {
						body = b
					}
				}
				break
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if body != "" {
		w.Write([]byte(body))
	}
}

// lambdaFunctionFromURI extracts the function name from an API Gateway Lambda URI.
// URI format: arn:aws:apigateway:{region}:lambda:path/2015-03-31/functions/{functionArn}/invocations
func lambdaFunctionFromURI(uri string) string {
	// Find "functions/" segment
	const marker = "functions/"
	idx := strings.Index(uri, marker)
	if idx < 0 {
		return ""
	}
	rest := uri[idx+len(marker):]
	// rest is "{functionArn}/invocations" or just the function name
	rest = strings.TrimSuffix(rest, "/invocations")
	// rest is the full function ARN: arn:aws:lambda:{region}:{account}:function:{name}[:qualifier]
	// or just a function name
	parts := strings.Split(rest, ":")
	if len(parts) >= 7 {
		// arn:aws:lambda:region:account:function:name[:qualifier]
		return parts[6]
	}
	// Treat as bare function name
	return rest
}

// isText reports whether b looks like UTF-8 text (no control bytes below 0x09).
func isText(b []byte) bool {
	for _, c := range b {
		if c < 0x09 {
			return false
		}
	}
	return true
}
