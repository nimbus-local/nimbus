package apigateway

import "testing"

// AWS assigns a WebSocket API a default API-key selection expression. Reporting
// it empty reads as an unset attribute, which produces a change on every plan.
func TestCreateAPI_WebSocketGetsDefaultAPIKeySelectionExpression(t *testing.T) {
	s := newV2Store()
	api := s.createAPI("ws", "", "WEBSOCKET", "$request.body.action", "", 4566)

	if api.ApiKeySelectionExpression != defaultAPIKeySelectionExpression {
		t.Errorf("expected %q, got %q",
			defaultAPIKeySelectionExpression, api.ApiKeySelectionExpression)
	}

	stored, ok := s.getAPI(api.ApiId)
	if !ok || stored.ApiKeySelectionExpression != defaultAPIKeySelectionExpression {
		t.Errorf("expected the default to survive read-back, got %+v", stored)
	}
}

func TestCreateAPI_ExplicitAPIKeySelectionExpressionWins(t *testing.T) {
	s := newV2Store()
	api := s.createAPI("ws", "", "WEBSOCKET", "$request.body.action", "$request.querystring.key", 4566)

	if api.ApiKeySelectionExpression != "$request.querystring.key" {
		t.Errorf("expected the supplied expression, got %q", api.ApiKeySelectionExpression)
	}
}

// HTTP APIs have no API-key selection; AWS reports none and so must Nimbus.
func TestCreateAPI_HTTPHasNoAPIKeySelectionExpression(t *testing.T) {
	s := newV2Store()
	api := s.createAPI("http", "", "HTTP", "", "", 4566)

	if api.ApiKeySelectionExpression != "" {
		t.Errorf("expected no expression for an HTTP API, got %q", api.ApiKeySelectionExpression)
	}
}
