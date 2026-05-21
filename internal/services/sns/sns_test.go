package sns

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newTestService() *Service {
	return New("us-east-1")
}

func snsRequest(t *testing.T, svc *Service, action string, params map[string]string) *httptest.ResponseRecorder {
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

func mustCreateTopic(t *testing.T, svc *Service, name string) string {
	t.Helper()
	w := snsRequest(t, svc, "CreateTopic", map[string]string{"Name": name})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateTopic %q: expected 200, got %d\n%s", name, w.Code, w.Body.String())
	}
	var resp struct {
		Result struct {
			TopicArn string `xml:"TopicArn"`
		} `xml:"CreateTopicResult"`
	}
	xml.NewDecoder(w.Body).Decode(&resp)
	return resp.Result.TopicArn
}

func mustSubscribe(t *testing.T, svc *Service, topicARN, protocol, endpoint string) string {
	t.Helper()
	w := snsRequest(t, svc, "Subscribe", map[string]string{
		"TopicArn": topicARN,
		"Protocol": protocol,
		"Endpoint": endpoint,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("Subscribe: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	var resp struct {
		Result struct {
			SubscriptionArn string `xml:"SubscriptionArn"`
		} `xml:"SubscribeResult"`
	}
	xml.NewDecoder(w.Body).Decode(&resp)
	return resp.Result.SubscriptionArn
}

// --- Detect ---

func TestDetect(t *testing.T) {
	svc := newTestService()
	cases := []struct {
		target   string
		action   string
		expected bool
	}{
		{"AmazonSimpleNotificationService.CreateTopic", "", true},
		{"AmazonSimpleNotificationService.Publish", "", true},
		{"", "CreateTopic", true},
		{"", "Publish", true},
		{"AmazonSQS.SendMessage", "", false},
		{"AmazonEventBridge.PutEvents", "", false},
		{"", "SendMessage", false},
		{"", "", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if tc.target != "" {
			req.Header.Set("X-Amz-Target", tc.target)
		}
		if tc.action != "" {
			req.URL.RawQuery = "Action=" + tc.action
		}
		if got := svc.Detect(req); got != tc.expected {
			t.Errorf("Detect(target=%q action=%q): expected %v, got %v", tc.target, tc.action, tc.expected, got)
		}
	}
}

// --- CreateTopic ---

func TestCreateTopic(t *testing.T) {
	svc := newTestService()

	w := snsRequest(t, svc, "CreateTopic", map[string]string{"Name": "my-topic"})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateTopic: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "my-topic") {
		t.Errorf("expected TopicArn to contain topic name: %s", w.Body.String())
	}
}

func TestCreateTopic_Idempotent(t *testing.T) {
	svc := newTestService()

	arn1 := mustCreateTopic(t, svc, "my-topic")
	arn2 := mustCreateTopic(t, svc, "my-topic")
	if arn1 != arn2 {
		t.Errorf("repeated CreateTopic returned different ARNs: %s vs %s", arn1, arn2)
	}
}

func TestCreateTopic_MissingName(t *testing.T) {
	svc := newTestService()

	w := snsRequest(t, svc, "CreateTopic", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", w.Code)
	}
}

// --- DeleteTopic ---

func TestDeleteTopic(t *testing.T) {
	svc := newTestService()

	arn := mustCreateTopic(t, svc, "temp-topic")
	w := snsRequest(t, svc, "DeleteTopic", map[string]string{"TopicArn": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteTopic: expected 200, got %d", w.Code)
	}

	// Topic should be gone from list
	lw := snsRequest(t, svc, "ListTopics", map[string]string{})
	if strings.Contains(lw.Body.String(), "temp-topic") {
		t.Error("expected topic to be removed from list")
	}
}

func TestDeleteTopic_RemovesSubscriptions(t *testing.T) {
	svc := newTestService()

	arn := mustCreateTopic(t, svc, "my-topic")
	mustSubscribe(t, svc, arn, "sqs", "arn:aws:sqs:us-east-1:000000000000:my-queue")

	snsRequest(t, svc, "DeleteTopic", map[string]string{"TopicArn": arn})

	// All subscriptions should be gone
	lw := snsRequest(t, svc, "ListSubscriptions", map[string]string{})
	if strings.Contains(lw.Body.String(), "my-topic") {
		t.Error("expected subscriptions to be removed when topic is deleted")
	}
}

// --- ListTopics ---

func TestListTopics_Empty(t *testing.T) {
	svc := newTestService()

	w := snsRequest(t, svc, "ListTopics", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("ListTopics empty: expected 200, got %d", w.Code)
	}
}

func TestListTopics_Multiple(t *testing.T) {
	svc := newTestService()

	mustCreateTopic(t, svc, "topic-a")
	mustCreateTopic(t, svc, "topic-b")
	mustCreateTopic(t, svc, "topic-c")

	w := snsRequest(t, svc, "ListTopics", map[string]string{})
	body := w.Body.String()
	for _, name := range []string{"topic-a", "topic-b", "topic-c"} {
		if !strings.Contains(body, name) {
			t.Errorf("expected %s in topic list", name)
		}
	}
}

// --- GetTopicAttributes / SetTopicAttributes ---

func TestGetTopicAttributes(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")

	w := snsRequest(t, svc, "GetTopicAttributes", map[string]string{"TopicArn": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("GetTopicAttributes: expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "TopicArn") {
		t.Errorf("expected TopicArn in attributes: %s", body)
	}
}

func TestGetTopicAttributes_NotFound(t *testing.T) {
	svc := newTestService()

	w := snsRequest(t, svc, "GetTopicAttributes", map[string]string{
		"TopicArn": "arn:aws:sns:us-east-1:000000000000:no-such-topic",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing topic, got %d", w.Code)
	}
}

func TestSetTopicAttributes(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")

	w := snsRequest(t, svc, "SetTopicAttributes", map[string]string{
		"TopicArn":       arn,
		"AttributeName":  "DisplayName",
		"AttributeValue": "My Topic Display",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("SetTopicAttributes: expected 200, got %d", w.Code)
	}

	gw := snsRequest(t, svc, "GetTopicAttributes", map[string]string{"TopicArn": arn})
	if !strings.Contains(gw.Body.String(), "My Topic Display") {
		t.Errorf("expected DisplayName to be updated: %s", gw.Body.String())
	}
}

// --- Subscribe / Unsubscribe ---

func TestSubscribe(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")

	w := snsRequest(t, svc, "Subscribe", map[string]string{
		"TopicArn": arn,
		"Protocol": "sqs",
		"Endpoint": "arn:aws:sqs:us-east-1:000000000000:my-queue",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("Subscribe: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "SubscriptionArn") {
		t.Errorf("expected SubscriptionArn in response: %s", w.Body.String())
	}
}

func TestSubscribe_TopicNotFound(t *testing.T) {
	svc := newTestService()

	w := snsRequest(t, svc, "Subscribe", map[string]string{
		"TopicArn": "arn:aws:sns:us-east-1:000000000000:no-topic",
		"Protocol": "sqs",
		"Endpoint": "arn:aws:sqs:us-east-1:000000000000:q",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing topic, got %d", w.Code)
	}
}

func TestUnsubscribe(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")
	subARN := mustSubscribe(t, svc, arn, "sqs", "arn:aws:sqs:us-east-1:000000000000:q")

	w := snsRequest(t, svc, "Unsubscribe", map[string]string{"SubscriptionArn": subARN})
	if w.Code != http.StatusOK {
		t.Fatalf("Unsubscribe: expected 200, got %d", w.Code)
	}

	lw := snsRequest(t, svc, "ListSubscriptionsByTopic", map[string]string{"TopicArn": arn})
	if strings.Contains(lw.Body.String(), subARN) {
		t.Error("expected subscription to be removed")
	}
}

// --- ListSubscriptions ---

func TestListSubscriptions_Empty(t *testing.T) {
	svc := newTestService()

	w := snsRequest(t, svc, "ListSubscriptions", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("ListSubscriptions empty: expected 200, got %d", w.Code)
	}
}

func TestListSubscriptions(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")

	mustSubscribe(t, svc, arn, "sqs", "arn:aws:sqs:us-east-1:000000000000:q1")
	mustSubscribe(t, svc, arn, "lambda", "arn:aws:lambda:us-east-1:000000000000:function:fn")

	w := snsRequest(t, svc, "ListSubscriptions", map[string]string{})
	body := w.Body.String()
	if !strings.Contains(body, "sqs") || !strings.Contains(body, "lambda") {
		t.Errorf("expected both subscriptions in list: %s", body)
	}
}

// --- ListSubscriptionsByTopic ---

func TestListSubscriptionsByTopic(t *testing.T) {
	svc := newTestService()
	arn1 := mustCreateTopic(t, svc, "topic-1")
	arn2 := mustCreateTopic(t, svc, "topic-2")

	mustSubscribe(t, svc, arn1, "sqs", "arn:aws:sqs:us-east-1:000000000000:q1")
	mustSubscribe(t, svc, arn2, "sqs", "arn:aws:sqs:us-east-1:000000000000:q2")

	w := snsRequest(t, svc, "ListSubscriptionsByTopic", map[string]string{"TopicArn": arn1})
	if w.Code != http.StatusOK {
		t.Fatalf("ListSubscriptionsByTopic: expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "q1") {
		t.Errorf("expected q1 subscription in topic-1 list: %s", body)
	}
	if strings.Contains(body, "q2") {
		t.Errorf("did not expect q2 subscription in topic-1 list: %s", body)
	}
}

func TestListSubscriptionsByTopic_NotFound(t *testing.T) {
	svc := newTestService()

	w := snsRequest(t, svc, "ListSubscriptionsByTopic", map[string]string{
		"TopicArn": "arn:aws:sns:us-east-1:000000000000:no-topic",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing topic, got %d", w.Code)
	}
}

// --- GetSubscriptionAttributes ---

func TestGetSubscriptionAttributes(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")
	subARN := mustSubscribe(t, svc, arn, "email", "user@example.com")

	w := snsRequest(t, svc, "GetSubscriptionAttributes", map[string]string{"SubscriptionArn": subARN})
	if w.Code != http.StatusOK {
		t.Fatalf("GetSubscriptionAttributes: expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "email") {
		t.Errorf("expected Protocol=email in attributes: %s", body)
	}
	if !strings.Contains(body, "user@example.com") {
		t.Errorf("expected endpoint in attributes: %s", body)
	}
}

func TestGetSubscriptionAttributes_NotFound(t *testing.T) {
	svc := newTestService()

	w := snsRequest(t, svc, "GetSubscriptionAttributes", map[string]string{
		"SubscriptionArn": "arn:aws:sns:us-east-1:000000000000:topic:no-such-sub",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing subscription, got %d", w.Code)
	}
}

// --- ConfirmSubscription ---

func TestConfirmSubscription(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")
	mustSubscribe(t, svc, arn, "http", "http://example.com/hook")

	w := snsRequest(t, svc, "ConfirmSubscription", map[string]string{
		"TopicArn": arn,
		"Token":    "some-token",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("ConfirmSubscription: expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SubscriptionArn") {
		t.Errorf("expected SubscriptionArn in response: %s", w.Body.String())
	}
}

// --- Publish ---

func TestPublish(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")

	w := snsRequest(t, svc, "Publish", map[string]string{
		"TopicArn": arn,
		"Message":  "Hello from Nimbus",
		"Subject":  "Test",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("Publish: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "MessageId") {
		t.Errorf("expected MessageId in response: %s", w.Body.String())
	}
}

func TestPublish_CapturesMessage(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")

	snsRequest(t, svc, "Publish", map[string]string{
		"TopicArn": arn,
		"Message":  `{"event":"order-placed"}`,
		"Subject":  "Order",
	})

	if svc.MessageCount() != 1 {
		t.Fatalf("expected 1 captured message, got %d", svc.MessageCount())
	}

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/sns/messages", nil)
	rw := httptest.NewRecorder()
	svc.MessagesHandler(rw, req)

	var msgs []*CapturedMessage
	json.NewDecoder(rw.Body).Decode(&msgs)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.Message != `{"event":"order-placed"}` {
		t.Errorf("unexpected message body: %q", m.Message)
	}
	if m.Subject != "Order" {
		t.Errorf("unexpected subject: %q", m.Subject)
	}
	if m.TopicARN != arn {
		t.Errorf("unexpected TopicARN: %q", m.TopicARN)
	}
	if m.MessageID == "" {
		t.Error("expected non-empty MessageId")
	}
}

func TestPublish_TopicNotFound(t *testing.T) {
	svc := newTestService()

	w := snsRequest(t, svc, "Publish", map[string]string{
		"TopicArn": "arn:aws:sns:us-east-1:000000000000:no-topic",
		"Message":  "hello",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing topic, got %d", w.Code)
	}
}

func TestPublish_MissingMessage(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")

	w := snsRequest(t, svc, "Publish", map[string]string{"TopicArn": arn})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing message, got %d", w.Code)
	}
}

func TestPublish_UniqueMessageIDs(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")

	for i := 0; i < 5; i++ {
		snsRequest(t, svc, "Publish", map[string]string{
			"TopicArn": arn,
			"Message":  "msg",
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/sns/messages", nil)
	rw := httptest.NewRecorder()
	svc.MessagesHandler(rw, req)

	var msgs []*CapturedMessage
	json.NewDecoder(rw.Body).Decode(&msgs)
	seen := map[string]bool{}
	for _, m := range msgs {
		if seen[m.MessageID] {
			t.Errorf("duplicate MessageId: %s", m.MessageID)
		}
		seen[m.MessageID] = true
	}
}

// --- PublishBatch ---

func TestPublishBatch(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")

	params := map[string]string{
		"TopicArn":                                    "irrelevant", // overridden below
		"PublishBatchRequestEntries.member.1.Id":      "1",
		"PublishBatchRequestEntries.member.1.Message": "msg-one",
		"PublishBatchRequestEntries.member.1.Subject": "First",
		"PublishBatchRequestEntries.member.2.Id":      "2",
		"PublishBatchRequestEntries.member.2.Message": "msg-two",
	}
	params["TopicArn"] = arn

	w := snsRequest(t, svc, "PublishBatch", params)
	if w.Code != http.StatusOK {
		t.Fatalf("PublishBatch: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if svc.MessageCount() != 2 {
		t.Errorf("expected 2 captured messages, got %d", svc.MessageCount())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<Id>1</Id>") || !strings.Contains(body, "<Id>2</Id>") {
		t.Errorf("expected both batch IDs in response: %s", body)
	}
}

func TestPublishBatch_TopicNotFound(t *testing.T) {
	svc := newTestService()

	w := snsRequest(t, svc, "PublishBatch", map[string]string{
		"TopicArn":                                    "arn:aws:sns:us-east-1:000000000000:no-topic",
		"PublishBatchRequestEntries.member.1.Id":      "1",
		"PublishBatchRequestEntries.member.1.Message": "msg",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing topic, got %d", w.Code)
	}
}

// --- GetTopicAttributes subscription count ---

func TestGetTopicAttributes_SubscriptionCount(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")

	mustSubscribe(t, svc, arn, "sqs", "arn:aws:sqs:us-east-1:000000000000:q1")
	mustSubscribe(t, svc, arn, "sqs", "arn:aws:sqs:us-east-1:000000000000:q2")

	w := snsRequest(t, svc, "GetTopicAttributes", map[string]string{"TopicArn": arn})
	if !strings.Contains(w.Body.String(), "<value>2</value>") {
		t.Errorf("expected SubscriptionsConfirmed=2 in attributes: %s", w.Body.String())
	}
}

// --- Inspection endpoints ---

func TestMessagesHandler_Empty(t *testing.T) {
	svc := newTestService()

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/sns/messages", nil)
	w := httptest.NewRecorder()
	svc.MessagesHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("MessagesHandler empty: expected 200, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if body != "[]" {
		t.Errorf("expected empty array, got %q", body)
	}
}

func TestClearMessagesHandler(t *testing.T) {
	svc := newTestService()
	arn := mustCreateTopic(t, svc, "my-topic")

	for i := 0; i < 3; i++ {
		snsRequest(t, svc, "Publish", map[string]string{"TopicArn": arn, "Message": "msg"})
	}
	if svc.MessageCount() != 3 {
		t.Fatalf("expected 3 messages before clear, got %d", svc.MessageCount())
	}

	req := httptest.NewRequest(http.MethodDelete, "/_nimbus/sns/messages", nil)
	w := httptest.NewRecorder()
	svc.ClearMessagesHandler(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("ClearMessages: expected 204, got %d", w.Code)
	}
	if svc.MessageCount() != 0 {
		t.Errorf("expected 0 messages after clear, got %d", svc.MessageCount())
	}
}

// --- Unknown action ---

func TestUnknownAction(t *testing.T) {
	svc := newTestService()

	w := snsRequest(t, svc, "CreatePlatformApplication", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown action, got %d", w.Code)
	}
}
