package sqs

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newTestService() *Service {
	return New("us-east-1")
}

// sqsRequest builds a form-encoded POST request simulating how the AWS SDK
// calls SQS — Action in the form body, QueueUrl as a form value.
func sqsRequest(t *testing.T, svc *Service, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

// createQueue is a test helper that creates a queue and returns its URL.
func createQueue(t *testing.T, svc *Service, name string) string {
	t.Helper()
	w := sqsRequest(t, svc, map[string]string{"Action": "CreateQueue", "QueueName": name})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateQueue %q: expected 200, got %d\n%s", name, w.Code, w.Body.String())
	}
	body := w.Body.String()
	// Extract QueueUrl from XML response
	start := strings.Index(body, "<QueueUrl>")
	end := strings.Index(body, "</QueueUrl>")
	if start == -1 || end == -1 {
		t.Fatalf("could not find QueueUrl in response: %s", body)
	}
	return body[start+len("<QueueUrl>") : end]
}

// --- Queue management ---

func TestCreateQueue(t *testing.T) {
	svc := newTestService()
	qURL := createQueue(t, svc, "my-queue")

	if qURL == "" {
		t.Fatal("expected non-empty queue URL")
	}
	if !strings.Contains(qURL, "my-queue") {
		t.Errorf("expected queue URL to contain queue name, got %q", qURL)
	}
	if !strings.Contains(qURL, accountID) {
		t.Errorf("expected queue URL to contain account ID, got %q", qURL)
	}
}

func TestCreateQueue_Idempotent(t *testing.T) {
	svc := newTestService()
	url1 := createQueue(t, svc, "my-queue")
	url2 := createQueue(t, svc, "my-queue")

	if url1 != url2 {
		t.Errorf("idempotent CreateQueue should return same URL: %q vs %q", url1, url2)
	}
}

func TestCreateQueue_MissingName(t *testing.T) {
	svc := newTestService()
	w := sqsRequest(t, svc, map[string]string{"Action": "CreateQueue"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing QueueName, got %d", w.Code)
	}
}

func TestGetQueueURL(t *testing.T) {
	svc := newTestService()
	created := createQueue(t, svc, "lookup-queue")

	w := sqsRequest(t, svc, map[string]string{
		"Action":    "GetQueueUrl",
		"QueueName": "lookup-queue",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("GetQueueUrl: expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, created) {
		t.Errorf("GetQueueUrl response should contain %q, got: %s", created, body)
	}
}

func TestGetQueueURL_NotFound(t *testing.T) {
	svc := newTestService()
	w := sqsRequest(t, svc, map[string]string{
		"Action":    "GetQueueUrl",
		"QueueName": "ghost-queue",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("GetQueueUrl missing: expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "NonExistentQueue") {
		t.Errorf("expected NonExistentQueue error: %s", w.Body.String())
	}
}

func TestDeleteQueue(t *testing.T) {
	svc := newTestService()
	qURL := createQueue(t, svc, "delete-me")

	w := sqsRequest(t, svc, map[string]string{
		"Action":   "DeleteQueue",
		"QueueUrl": qURL,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteQueue: expected 200, got %d", w.Code)
	}

	// Queue should no longer be findable
	w = sqsRequest(t, svc, map[string]string{
		"Action":    "GetQueueUrl",
		"QueueName": "delete-me",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("after delete: expected 400, got %d", w.Code)
	}
}

func TestListQueues(t *testing.T) {
	svc := newTestService()
	createQueue(t, svc, "queue-alpha")
	createQueue(t, svc, "queue-beta")
	createQueue(t, svc, "other-queue")

	w := sqsRequest(t, svc, map[string]string{"Action": "ListQueues"})
	if w.Code != http.StatusOK {
		t.Fatalf("ListQueues: expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	for _, name := range []string{"queue-alpha", "queue-beta", "other-queue"} {
		if !strings.Contains(body, name) {
			t.Errorf("expected %q in ListQueues response", name)
		}
	}
}

func TestListQueues_WithPrefix(t *testing.T) {
	svc := newTestService()
	createQueue(t, svc, "prod-orders")
	createQueue(t, svc, "prod-events")
	createQueue(t, svc, "dev-orders")

	w := sqsRequest(t, svc, map[string]string{
		"Action":          "ListQueues",
		"QueueNamePrefix": "prod-",
	})
	body := w.Body.String()

	if !strings.Contains(body, "prod-orders") {
		t.Errorf("expected prod-orders in response")
	}
	if !strings.Contains(body, "prod-events") {
		t.Errorf("expected prod-events in response")
	}
	if strings.Contains(body, "dev-orders") {
		t.Errorf("did not expect dev-orders in prefix-filtered response")
	}
}

// --- Message operations ---

func TestSendAndReceiveMessage(t *testing.T) {
	svc := newTestService()
	qURL := createQueue(t, svc, "msg-queue")

	// Send
	w := sqsRequest(t, svc, map[string]string{
		"Action":      "SendMessage",
		"QueueUrl":    qURL,
		"MessageBody": "hello from nimbus",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("SendMessage: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "MessageId") {
		t.Errorf("expected MessageId in SendMessage response")
	}

	// Receive
	w = sqsRequest(t, svc, map[string]string{
		"Action":              "ReceiveMessage",
		"QueueUrl":            qURL,
		"MaxNumberOfMessages": "1",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("ReceiveMessage: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, "hello from nimbus") {
		t.Errorf("expected message body in response: %s", body)
	}
	if !strings.Contains(body, "ReceiptHandle") {
		t.Errorf("expected ReceiptHandle in response: %s", body)
	}
}

func TestReceiveMessage_Empty(t *testing.T) {
	svc := newTestService()
	qURL := createQueue(t, svc, "empty-queue")

	w := sqsRequest(t, svc, map[string]string{
		"Action":   "ReceiveMessage",
		"QueueUrl": qURL,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("ReceiveMessage empty: expected 200, got %d", w.Code)
	}

	// Should return empty result, not an error
	if strings.Contains(w.Body.String(), "Error") {
		t.Errorf("expected no error for empty receive: %s", w.Body.String())
	}
}

func TestDeleteMessage(t *testing.T) {
	svc := newTestService()
	qURL := createQueue(t, svc, "del-queue")

	sqsRequest(t, svc, map[string]string{
		"Action":      "SendMessage",
		"QueueUrl":    qURL,
		"MessageBody": "to be deleted",
	})

	// Receive to get receipt handle
	rw := sqsRequest(t, svc, map[string]string{
		"Action":              "ReceiveMessage",
		"QueueUrl":            qURL,
		"MaxNumberOfMessages": "1",
		"VisibilityTimeout":   "30",
	})

	body := rw.Body.String()
	start := strings.Index(body, "<ReceiptHandle>")
	end := strings.Index(body, "</ReceiptHandle>")
	if start == -1 {
		t.Fatalf("no ReceiptHandle in receive response: %s", body)
	}
	receipt := body[start+len("<ReceiptHandle>") : end]

	// Delete
	w := sqsRequest(t, svc, map[string]string{
		"Action":        "DeleteMessage",
		"QueueUrl":      qURL,
		"ReceiptHandle": receipt,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteMessage: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestPurgeQueue(t *testing.T) {
	svc := newTestService()
	qURL := createQueue(t, svc, "purge-queue")

	// Send a few messages
	for i := 0; i < 5; i++ {
		sqsRequest(t, svc, map[string]string{
			"Action":      "SendMessage",
			"QueueUrl":    qURL,
			"MessageBody": "message",
		})
	}

	w := sqsRequest(t, svc, map[string]string{
		"Action":   "PurgeQueue",
		"QueueUrl": qURL,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PurgeQueue: expected 200, got %d", w.Code)
	}

	// Queue should be empty
	rw := sqsRequest(t, svc, map[string]string{
		"Action":              "ReceiveMessage",
		"QueueUrl":            qURL,
		"MaxNumberOfMessages": "10",
	})
	if strings.Contains(rw.Body.String(), "<Body>") {
		t.Errorf("expected empty queue after purge, but received messages: %s", rw.Body.String())
	}
}

func TestGetQueueAttributes(t *testing.T) {
	svc := newTestService()
	qURL := createQueue(t, svc, "attr-queue")

	w := sqsRequest(t, svc, map[string]string{
		"Action":   "GetQueueAttributes",
		"QueueUrl": qURL,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("GetQueueAttributes: expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "QueueArn") {
		t.Errorf("expected QueueArn attribute: %s", body)
	}
	if !strings.Contains(body, "VisibilityTimeout") {
		t.Errorf("expected VisibilityTimeout attribute: %s", body)
	}
}

func TestSendMultipleReceiveMultiple(t *testing.T) {
	svc := newTestService()
	qURL := createQueue(t, svc, "multi-queue")

	messages := []string{"msg-1", "msg-2", "msg-3"}
	for _, m := range messages {
		sqsRequest(t, svc, map[string]string{
			"Action":      "SendMessage",
			"QueueUrl":    qURL,
			"MessageBody": m,
		})
	}

	w := sqsRequest(t, svc, map[string]string{
		"Action":              "ReceiveMessage",
		"QueueUrl":            qURL,
		"MaxNumberOfMessages": "10",
	})

	body := w.Body.String()
	received := strings.Count(body, "<Body>")
	if received != 3 {
		t.Errorf("expected 3 messages, got %d: %s", received, body)
	}
}

func TestChangeMessageVisibility(t *testing.T) {
	svc := newTestService()
	qURL := createQueue(t, svc, "vis-queue")

	sqsRequest(t, svc, map[string]string{
		"Action":      "SendMessage",
		"QueueUrl":    qURL,
		"MessageBody": "visibility test",
	})

	rw := sqsRequest(t, svc, map[string]string{
		"Action":              "ReceiveMessage",
		"QueueUrl":            qURL,
		"MaxNumberOfMessages": "1",
		"VisibilityTimeout":   "30",
	})

	body := rw.Body.String()
	start := strings.Index(body, "<ReceiptHandle>")
	end := strings.Index(body, "</ReceiptHandle>")
	if start == -1 {
		t.Fatalf("no ReceiptHandle: %s", body)
	}
	receipt := body[start+len("<ReceiptHandle>") : end]

	w := sqsRequest(t, svc, map[string]string{
		"Action":            "ChangeMessageVisibility",
		"QueueUrl":          qURL,
		"ReceiptHandle":     receipt,
		"VisibilityTimeout": "0",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("ChangeMessageVisibility: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

// --- Service detection ---

func TestDetect(t *testing.T) {
	svc := newTestService()

	cases := []struct {
		name     string
		target   string
		action   string
		path     string
		expected bool
	}{
		{"AmazonSQS target", "AmazonSQS.SendMessage", "", "/", true},
		{"CreateQueue action", "", "CreateQueue", "/", true},
		{"SendMessage action", "", "SendMessage", "/", true},
		{"DynamoDB target - not SQS", "DynamoDB_20120810.ListTables", "", "/", false},
		{"unknown action - not SQS", "", "DoSomethingElse", "/", false},
		{"SQS path with account ID", "", "", "/" + accountID + "/my-queue", true},
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
	w := sqsRequest(t, svc, map[string]string{"Action": "NonExistentAction"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("unknown action: expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "InvalidAction") {
		t.Errorf("expected InvalidAction error: %s", w.Body.String())
	}
}
