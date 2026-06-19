package apigateway

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/uid"
)

// wsConn represents an active WebSocket connection.
type wsConn struct {
	id          string
	apiID       string
	stage       string
	conn        net.Conn
	br          *bufio.Reader
	mu          sync.Mutex // serialises writes
	connectedAt time.Time
}

func (c *wsConn) send(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return wsWrite(c.conn, wsOpText, data)
}

func (c *wsConn) closeGracefully(code uint16, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	wsWriteClose(c.conn, code, reason)
	c.conn.Close()
}

// wsRegistry is a thread-safe store of active WebSocket connections keyed by connectionId.
type wsRegistry struct{ m sync.Map }

func (reg *wsRegistry) add(c *wsConn) { reg.m.Store(c.id, c) }
func (reg *wsRegistry) del(id string) { reg.m.Delete(id) }
func (reg *wsRegistry) reset() {
	reg.m.Range(func(k, v any) bool {
		v.(*wsConn).conn.Close()
		reg.m.Delete(k)
		return true
	})
}
func (reg *wsRegistry) get(id string) (*wsConn, bool) {
	v, ok := reg.m.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*wsConn), true
}

// handleWebSocket upgrades the HTTP connection, dispatches $connect to Lambda,
// loops reading frames and dispatching them, then dispatches $disconnect on exit.
func (s *Service) handleWebSocket(w http.ResponseWriter, r *http.Request, apiID, stageName string) {
	api, ok := s.v2.getAPI(apiID)
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "NotFoundException", "API not found: "+apiID)
		return
	}

	conn, br, err := wsUpgrade(w, r)
	if err != nil {
		// wsUpgrade already hijacked or failed before hijack — just return.
		return
	}

	c := &wsConn{
		id:          uid.New(),
		apiID:       apiID,
		stage:       stageName,
		conn:        conn,
		br:          br,
		connectedAt: time.Now().UTC(),
	}
	s.wsConns.add(c)
	defer s.wsConns.del(c.id)

	// $connect — reject connection if Lambda returns non-2xx.
	if !s.invokeWSConnect(c, r) {
		c.closeGracefully(1008, "Policy violation")
		return
	}

	var closeCode uint16 = 1000
	for {
		frame, err := wsRead(c.br)
		if err != nil {
			break
		}
		switch frame.opcode {
		case wsOpPing:
			c.mu.Lock()
			wsWrite(c.conn, wsOpPong, frame.payload) //nolint:errcheck
			c.mu.Unlock()
		case wsOpClose:
			closeCode = wsCloseCode(frame.payload)
			c.mu.Lock()
			wsWriteClose(c.conn, 1000, "")
			c.mu.Unlock()
			goto disconnect
		case wsOpText, wsOpBinary:
			routeKey := s.selectWSRoute(api, frame.payload)
			s.invokeWSMessage(c, routeKey, frame.payload)
		}
	}

disconnect:
	s.invokeWSDisconnect(c, closeCode)
	conn.Close()
}

// selectWSRoute evaluates the routeSelectionExpression against the message body
// to pick a route key, falling back to $default.
func (s *Service) selectWSRoute(api *HTTPApi, body []byte) string {
	expr := api.RouteSelectionExpression
	if expr == "" || len(body) == 0 {
		return "$default"
	}
	// Expression format: "$request.body.<field>"
	field := strings.TrimPrefix(expr, "$request.body.")
	if field == expr {
		return "$default"
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return "$default"
	}
	val, _ := m[field].(string)
	if val == "" {
		return "$default"
	}
	// Check a named route exists; fall back to $default if not.
	routes, _ := s.v2.listRoutes(api.ApiId)
	for _, route := range routes {
		if route.RouteKey == val {
			return val
		}
	}
	return "$default"
}

// wsEventPayload is the JSON body sent to Lambda for WebSocket invocations.
type wsEventPayload struct {
	RequestContext  wsRequestContext  `json:"requestContext"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	IsBase64Encoded bool              `json:"isBase64Encoded"`
}

type wsRequestContext struct {
	ConnectionID         string `json:"connectionId"`
	EventType            string `json:"eventType"`
	RouteKey             string `json:"routeKey"`
	ApiId                string `json:"apiId"`
	Stage                string `json:"stage"`
	DomainName           string `json:"domainName"`
	ConnectedAt          int64  `json:"connectedAt"`
	RequestTimeEpoch     int64  `json:"requestTimeEpoch"`
	RequestId            string `json:"requestId"`
	ExtendedRequestId    string `json:"extendedRequestId"`
	MessageId            string `json:"messageId,omitempty"`
	DisconnectStatusCode int    `json:"disconnectStatusCode,omitempty"`
	DisconnectReason     string `json:"disconnectReason,omitempty"`
}

func (s *Service) wsRequestCtx(c *wsConn, eventType, routeKey string) wsRequestContext {
	return wsRequestContext{
		ConnectionID:      c.id,
		EventType:         eventType,
		RouteKey:          routeKey,
		ApiId:             c.apiID,
		Stage:             c.stage,
		DomainName:        "localhost:4566",
		ConnectedAt:       c.connectedAt.UnixMilli(),
		RequestTimeEpoch:  time.Now().UnixMilli(),
		RequestId:         uid.New(),
		ExtendedRequestId: uid.New(),
	}
}

// invokeWSConnect dispatches the $connect event and returns true if the connection is allowed.
func (s *Service) invokeWSConnect(c *wsConn, r *http.Request) bool {
	integ := s.findWSIntegration(c.apiID, "$connect")
	if integ == nil {
		return true // no integration = allow by default
	}
	fn := lambdaFunctionFromURI(integ.IntegrationUri)
	if fn == "" {
		return true
	}

	headers := make(map[string]string, len(r.Header))
	for k, vs := range r.Header {
		headers[strings.ToLower(k)] = vs[0]
	}

	ctx := s.wsRequestCtx(c, "CONNECT", "$connect")
	event := wsEventPayload{RequestContext: ctx, Headers: headers}
	payload, _ := json.Marshal(event)

	resp, err := s.lambda.DirectInvoke(fn, payload)
	if err != nil {
		return false
	}
	var result struct {
		StatusCode int `json:"statusCode"`
	}
	if json.Unmarshal(resp, &result) == nil && result.StatusCode != 0 && result.StatusCode >= 300 {
		return false
	}
	return true
}

func (s *Service) invokeWSMessage(c *wsConn, routeKey string, body []byte) {
	integ := s.findWSIntegration(c.apiID, routeKey)
	if integ == nil {
		return
	}
	fn := lambdaFunctionFromURI(integ.IntegrationUri)
	if fn == "" {
		return
	}
	ctx := s.wsRequestCtx(c, "MESSAGE", routeKey)
	ctx.MessageId = uid.New()
	event := wsEventPayload{RequestContext: ctx, Body: string(body)}
	payload, _ := json.Marshal(event)
	s.lambda.DirectInvoke(fn, payload) //nolint:errcheck
}

func (s *Service) invokeWSDisconnect(c *wsConn, code uint16) {
	integ := s.findWSIntegration(c.apiID, "$disconnect")
	if integ == nil {
		return
	}
	fn := lambdaFunctionFromURI(integ.IntegrationUri)
	if fn == "" {
		return
	}
	ctx := s.wsRequestCtx(c, "DISCONNECT", "$disconnect")
	ctx.DisconnectStatusCode = int(code)
	event := wsEventPayload{RequestContext: ctx}
	payload, _ := json.Marshal(event)
	s.lambda.DirectInvoke(fn, payload) //nolint:errcheck
}

// findWSIntegration finds the integration for routeKey, falling back to $default.
func (s *Service) findWSIntegration(apiID, routeKey string) *V2Integration {
	routes, _ := s.v2.listRoutes(apiID)
	var defaultInteg *V2Integration
	for _, route := range routes {
		if route.Target == "" {
			continue
		}
		integ, ok := s.v2.integrationForRoute(apiID, route)
		if !ok {
			continue
		}
		if route.RouteKey == routeKey {
			return integ
		}
		if route.RouteKey == "$default" {
			defaultInteg = integ
		}
	}
	if routeKey != "$default" {
		return defaultInteg
	}
	return nil
}

// serveConnections handles the @connections management API used by Lambda to push
// messages to or disconnect WebSocket clients.
// Path: (any prefix)/@connections/{connectionId}
func (s *Service) serveConnections(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	idx := strings.Index(p, "@connections/")
	if idx == -1 {
		apiError(w, http.StatusNotFound, "NotFoundException", "not found")
		return
	}
	connectionID := strings.TrimSuffix(p[idx+len("@connections/"):], "/")

	c, ok := s.wsConns.get(connectionID)
	if !ok {
		apiError(w, http.StatusGone, "GoneException", "Connection "+connectionID+" is gone")
		return
	}

	switch r.Method {
	case http.MethodPost:
		data, _ := io.ReadAll(r.Body)
		if err := c.send(data); err != nil {
			s.wsConns.del(connectionID)
			apiError(w, http.StatusGone, "GoneException", "Connection gone")
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		c.closeGracefully(1001, "Going Away")
		s.wsConns.del(connectionID)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		jsonhttp.Write(w, http.StatusOK, map[string]any{
			"connectionId": c.id,
			"connectedAt":  c.connectedAt.Format(time.RFC3339),
			"lastActiveAt": time.Now().UTC().Format(time.RFC3339),
		})
	default:
		apiError(w, http.StatusMethodNotAllowed, "MethodNotAllowedException", "Method not allowed")
	}
}
