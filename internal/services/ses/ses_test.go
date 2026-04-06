package ses

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newTestService() *Service {
	return New("us-east-1")
}

// sesv1Request sends a form-encoded SES v1 request
func sesv1Request(t *testing.T, svc *Service, action string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	form.Set("Action", action)
	for k, v := range params {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

// sesv2Request sends a JSON SES v2 request
func sesv2Request(t *testing.T, svc *Service, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, "/v2/email/outbound-emails", &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

// capturedMessages fetches messages from the inspection handler
func capturedMessages(t *testing.T, svc *Service) []CapturedEmail {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/_nimbus/ses/messages", nil)
	w := httptest.NewRecorder()
	svc.MessagesHandler(w, req)

	var messages []CapturedEmail
	if err := json.NewDecoder(w.Body).Decode(&messages); err != nil {
		// Empty array returns "[]" which decodes fine
		return nil
	}
	return messages
}

// --- SendEmail (v1) ---

func TestSendEmail(t *testing.T) {
	svc := newTestService()

	w := sesv1Request(t, svc, "SendEmail", map[string]string{
		"Source":                           "sender@example.com",
		"Destination.ToAddresses.member.1": "recipient@example.com",
		"Message.Subject.Data":             "Hello from Nimbus",
		"Message.Body.Text.Data":           "This is a test email.",
		"Message.Body.Html.Data":           "<p>This is a test email.</p>",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("SendEmail: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	if !strings.Contains(w.Body.String(), "MessageId") {
		t.Error("expected MessageId in response")
	}
}

func TestSendEmail_CapturesMessage(t *testing.T) {
	svc := newTestService()

	sesv1Request(t, svc, "SendEmail", map[string]string{
		"Source":                           "sender@example.com",
		"Destination.ToAddresses.member.1": "to@example.com",
		"Destination.CcAddresses.member.1": "cc@example.com",
		"Message.Subject.Data":             "Test Subject",
		"Message.Body.Text.Data":           "Text body",
		"Message.Body.Html.Data":           "<b>HTML body</b>",
	})

	messages := capturedMessages(t, svc)
	if len(messages) != 1 {
		t.Fatalf("expected 1 captured message, got %d", len(messages))
	}

	msg := messages[0]
	if msg.From != "sender@example.com" {
		t.Errorf("expected From=sender@example.com, got %q", msg.From)
	}
	if len(msg.To) != 1 || msg.To[0] != "to@example.com" {
		t.Errorf("expected To=[to@example.com], got %v", msg.To)
	}
	if len(msg.CC) != 1 || msg.CC[0] != "cc@example.com" {
		t.Errorf("expected CC=[cc@example.com], got %v", msg.CC)
	}
	if msg.Subject != "Test Subject" {
		t.Errorf("expected Subject=Test Subject, got %q", msg.Subject)
	}
	if msg.Body.Text != "Text body" {
		t.Errorf("expected text body, got %q", msg.Body.Text)
	}
	if msg.Body.HTML != "<b>HTML body</b>" {
		t.Errorf("expected html body, got %q", msg.Body.HTML)
	}
	if msg.MessageID == "" {
		t.Error("expected non-empty MessageId")
	}
}

func TestSendEmail_MultipleRecipients(t *testing.T) {
	svc := newTestService()

	sesv1Request(t, svc, "SendEmail", map[string]string{
		"Source":                           "sender@example.com",
		"Destination.ToAddresses.member.1": "a@example.com",
		"Destination.ToAddresses.member.2": "b@example.com",
		"Destination.ToAddresses.member.3": "c@example.com",
		"Message.Subject.Data":             "Bulk",
		"Message.Body.Text.Data":           "Body",
	})

	messages := capturedMessages(t, svc)
	if len(messages[0].To) != 3 {
		t.Errorf("expected 3 recipients, got %d", len(messages[0].To))
	}
}

func TestSendEmail_MissingSource(t *testing.T) {
	svc := newTestService()

	w := sesv1Request(t, svc, "SendEmail", map[string]string{
		"Destination.ToAddresses.member.1": "to@example.com",
		"Message.Subject.Data":             "Test",
		"Message.Body.Text.Data":           "Body",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing Source, got %d", w.Code)
	}
}

func TestSendEmail_MissingRecipient(t *testing.T) {
	svc := newTestService()

	w := sesv1Request(t, svc, "SendEmail", map[string]string{
		"Source":                 "sender@example.com",
		"Message.Subject.Data":   "Test",
		"Message.Body.Text.Data": "Body",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing recipient, got %d", w.Code)
	}
}

func TestSendEmail_UniqueMessageIDs(t *testing.T) {
	svc := newTestService()

	for i := 0; i < 5; i++ {
		sesv1Request(t, svc, "SendEmail", map[string]string{
			"Source":                           "sender@example.com",
			"Destination.ToAddresses.member.1": "to@example.com",
			"Message.Subject.Data":             "Test",
			"Message.Body.Text.Data":           "Body",
		})
	}

	messages := capturedMessages(t, svc)
	seen := map[string]bool{}
	for _, m := range messages {
		if seen[m.MessageID] {
			t.Errorf("duplicate MessageId: %s", m.MessageID)
		}
		seen[m.MessageID] = true
	}
}

// --- SendRawEmail (v1) ---

func TestSendRawEmail(t *testing.T) {
	svc := newTestService()

	raw := "From: sender@example.com\r\nTo: to@example.com\r\nSubject: Raw\r\n\r\nBody"
	w := sesv1Request(t, svc, "SendRawEmail", map[string]string{
		"Source":                "sender@example.com",
		"RawMessage.Data":       raw,
		"Destinations.member.1": "to@example.com",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("SendRawEmail: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	messages := capturedMessages(t, svc)
	if len(messages) != 1 {
		t.Fatalf("expected 1 captured message, got %d", len(messages))
	}
	if messages[0].Raw != raw {
		t.Errorf("expected raw message to be captured")
	}
}

// --- SES v2 SendEmail ---

func TestSendEmailV2(t *testing.T) {
	svc := newTestService()

	w := sesv2Request(t, svc, map[string]interface{}{
		"FromEmailAddress": "sender@example.com",
		"Destination": map[string]interface{}{
			"ToAddresses": []string{"to@example.com"},
		},
		"Content": map[string]interface{}{
			"Simple": map[string]interface{}{
				"Subject": map[string]string{"Data": "v2 subject"},
				"Body": map[string]interface{}{
					"Text": map[string]string{"Data": "v2 text body"},
					"Html": map[string]string{"Data": "<p>v2 html body</p>"},
				},
			},
		},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("SendEmail v2: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["MessageId"] == "" {
		t.Error("expected MessageId in v2 response")
	}
}

func TestSendEmailV2_CapturesMessage(t *testing.T) {
	svc := newTestService()

	sesv2Request(t, svc, map[string]interface{}{
		"FromEmailAddress": "v2sender@example.com",
		"Destination": map[string]interface{}{
			"ToAddresses":  []string{"to@example.com"},
			"CcAddresses":  []string{"cc@example.com"},
			"BccAddresses": []string{"bcc@example.com"},
		},
		"ReplyToAddresses": []string{"reply@example.com"},
		"Content": map[string]interface{}{
			"Simple": map[string]interface{}{
				"Subject": map[string]string{"Data": "V2 Test"},
				"Body": map[string]interface{}{
					"Text": map[string]string{"Data": "v2 body"},
				},
			},
		},
	})

	messages := capturedMessages(t, svc)
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}

	msg := messages[0]
	if msg.From != "v2sender@example.com" {
		t.Errorf("expected From=v2sender@example.com, got %q", msg.From)
	}
	if msg.Subject != "V2 Test" {
		t.Errorf("expected Subject=V2 Test, got %q", msg.Subject)
	}
	if len(msg.CC) != 1 || msg.CC[0] != "cc@example.com" {
		t.Errorf("expected CC=[cc@example.com], got %v", msg.CC)
	}
	if len(msg.BCC) != 1 || msg.BCC[0] != "bcc@example.com" {
		t.Errorf("expected BCC=[bcc@example.com], got %v", msg.BCC)
	}
}

// --- VerifyEmailIdentity ---

func TestVerifyEmailIdentity(t *testing.T) {
	svc := newTestService()

	w := sesv1Request(t, svc, "VerifyEmailIdentity", map[string]string{
		"EmailAddress": "verified@example.com",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("VerifyEmailIdentity: expected 200, got %d", w.Code)
	}
}

func TestVerifyEmailIdentity_AppearsInList(t *testing.T) {
	svc := newTestService()

	sesv1Request(t, svc, "VerifyEmailIdentity", map[string]string{
		"EmailAddress": "verified@example.com",
	})

	w := sesv1Request(t, svc, "ListIdentities", map[string]string{})
	if !strings.Contains(w.Body.String(), "verified@example.com") {
		t.Errorf("expected verified@example.com in identity list: %s", w.Body.String())
	}
}

// --- ListIdentities ---

func TestListIdentities_Empty(t *testing.T) {
	svc := newTestService()

	w := sesv1Request(t, svc, "ListIdentities", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("ListIdentities: expected 200, got %d", w.Code)
	}
}

func TestListIdentities_Multiple(t *testing.T) {
	svc := newTestService()

	for _, addr := range []string{"a@example.com", "b@example.com", "example.com"} {
		sesv1Request(t, svc, "VerifyEmailIdentity", map[string]string{
			"EmailAddress": addr,
		})
	}

	w := sesv1Request(t, svc, "ListIdentities", map[string]string{})
	body := w.Body.String()

	for _, addr := range []string{"a@example.com", "b@example.com", "example.com"} {
		if !strings.Contains(body, addr) {
			t.Errorf("expected %s in identity list", addr)
		}
	}
}

// --- DeleteIdentity ---

func TestDeleteIdentity(t *testing.T) {
	svc := newTestService()

	sesv1Request(t, svc, "VerifyEmailIdentity", map[string]string{
		"EmailAddress": "delete-me@example.com",
	})

	w := sesv1Request(t, svc, "DeleteIdentity", map[string]string{
		"Identity": "delete-me@example.com",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("DeleteIdentity: expected 200, got %d", w.Code)
	}

	lw := sesv1Request(t, svc, "ListIdentities", map[string]string{})
	if strings.Contains(lw.Body.String(), "delete-me@example.com") {
		t.Error("expected identity to be removed from list")
	}
}

// --- GetSendQuota ---

func TestGetSendQuota(t *testing.T) {
	svc := newTestService()

	w := sesv1Request(t, svc, "GetSendQuota", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("GetSendQuota: expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Max24HourSend") {
		t.Errorf("expected Max24HourSend in response: %s", body)
	}
	if !strings.Contains(body, "MaxSendRate") {
		t.Errorf("expected MaxSendRate in response: %s", body)
	}
}

func TestGetSendQuota_ReflectsSentCount(t *testing.T) {
	svc := newTestService()

	// Send 3 emails
	for i := 0; i < 3; i++ {
		sesv1Request(t, svc, "SendEmail", map[string]string{
			"Source":                           "sender@example.com",
			"Destination.ToAddresses.member.1": "to@example.com",
			"Message.Subject.Data":             "Test",
			"Message.Body.Text.Data":           "Body",
		})
	}

	w := sesv1Request(t, svc, "GetSendQuota", map[string]string{})
	if !strings.Contains(w.Body.String(), "3") {
		t.Errorf("expected SentLast24Hours=3 in response: %s", w.Body.String())
	}
}

// --- GetSendStatistics ---

func TestGetSendStatistics(t *testing.T) {
	svc := newTestService()

	w := sesv1Request(t, svc, "GetSendStatistics", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("GetSendStatistics: expected 200, got %d", w.Code)
	}
}

// --- Messages inspection endpoint ---

func TestMessagesHandler_Empty(t *testing.T) {
	svc := newTestService()

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/ses/messages", nil)
	w := httptest.NewRecorder()
	svc.MessagesHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("MessagesHandler empty: expected 200, got %d", w.Code)
	}

	body := strings.TrimSpace(w.Body.String())
	if body != "[]" {
		t.Errorf("expected empty array [], got %q", body)
	}
}

func TestClearMessagesHandler(t *testing.T) {
	svc := newTestService()

	// Send some emails
	for i := 0; i < 3; i++ {
		sesv1Request(t, svc, "SendEmail", map[string]string{
			"Source":                           "sender@example.com",
			"Destination.ToAddresses.member.1": "to@example.com",
			"Message.Subject.Data":             "Test",
			"Message.Body.Text.Data":           "Body",
		})
	}

	if svc.MessageCount() != 3 {
		t.Fatalf("expected 3 messages before clear, got %d", svc.MessageCount())
	}

	// Clear
	req := httptest.NewRequest(http.MethodDelete, "/_nimbus/ses/messages", nil)
	w := httptest.NewRecorder()
	svc.ClearMessagesHandler(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("ClearMessages: expected 204, got %d", w.Code)
	}

	if svc.MessageCount() != 0 {
		t.Errorf("expected 0 messages after clear, got %d", svc.MessageCount())
	}
}

func TestMessageCount(t *testing.T) {
	svc := newTestService()

	if svc.MessageCount() != 0 {
		t.Errorf("expected 0 initial messages, got %d", svc.MessageCount())
	}

	sesv1Request(t, svc, "SendEmail", map[string]string{
		"Source":                           "sender@example.com",
		"Destination.ToAddresses.member.1": "to@example.com",
		"Message.Subject.Data":             "Test",
		"Message.Body.Text.Data":           "Body",
	})

	if svc.MessageCount() != 1 {
		t.Errorf("expected 1 message, got %d", svc.MessageCount())
	}
}

// --- Detect ---

func TestDetect(t *testing.T) {
	svc := newTestService()

	cases := []struct {
		name     string
		target   string
		action   string
		path     string
		expected bool
	}{
		{"v1 target", "AmazonSimpleEmailService.SendEmail", "", "/", true},
		{"v1 action", "", "SendEmail", "/", true},
		{"v1 verify action", "", "VerifyEmailIdentity", "/", true},
		{"v2 path", "", "", "/v2/email/outbound-emails", true},
		{"SQS - not SES", "AmazonSQS.SendMessage", "", "/", false},
		{"DynamoDB - not SES", "DynamoDB_20120810.ListTables", "", "/", false},
		{"SSM - not SES", "AmazonSSM.GetParameter", "", "/", false},
		{"empty", "", "", "/", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			if tc.target != "" {
				req.Header.Set("X-Amz-Target", tc.target)
			}
			if tc.action != "" {
				q := req.URL.Query()
				q.Set("Action", tc.action)
				req.URL.RawQuery = q.Encode()
			}
			got := svc.Detect(req)
			if got != tc.expected {
				t.Errorf("Detect(%q): expected %v, got %v", tc.name, tc.expected, got)
			}
		})
	}
}

// --- Unknown action ---

func TestUnknownAction(t *testing.T) {
	svc := newTestService()

	w := sesv1Request(t, svc, "CreateReceiptRule", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown action, got %d", w.Code)
	}
}

// suppress unused import
var _ = io.Discard
