package sns

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const (
	xmlNS     = "https://sns.amazonaws.com/doc/2010-03-31/"
	xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>`
	accountID = "000000000000"
)

// Service implements the AWS SNS emulator.
// Topics and subscriptions are stored in-memory. Published messages are
// captured and never delivered to real endpoints.
// Captured messages are available at GET /_nimbus/sns/messages.
type Service struct {
	mu            sync.RWMutex
	topics        map[string]*topic        // ARN -> topic
	byName        map[string]string        // name -> ARN
	subscriptions map[string]*subscription // subARN -> subscription
	messages      []*CapturedMessage
	region        string
}

type topic struct {
	name        string
	arn         string
	displayName string
	subARNs     []string
}

type subscription struct {
	arn      string
	topicARN string
	protocol string
	endpoint string
}

// CapturedMessage is a message published via Publish or PublishBatch.
// Available at GET /_nimbus/sns/messages.
type CapturedMessage struct {
	MessageID   string            `json:"MessageId"`
	TopicARN    string            `json:"TopicArn"`
	Subject     string            `json:"Subject,omitempty"`
	Message     string            `json:"Message"`
	Attributes  map[string]string `json:"Attributes,omitempty"`
	PublishedAt time.Time         `json:"PublishedAt"`
}

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region:        region,
		topics:        map[string]*topic{},
		byName:        map[string]string{},
		subscriptions: map[string]*subscription{},
	}
}

func (s *Service) Name() string { return "sns" }

// Detect identifies SNS requests by X-Amz-Target header or Action param.
func (s *Service) Detect(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("X-Amz-Target"), "AmazonSimpleNotificationService.") {
		return true
	}
	r.ParseForm()
	return isSNSAction(r.Form.Get("Action"))
}

func isSNSAction(action string) bool {
	switch action {
	case "CreateTopic", "DeleteTopic", "ListTopics", "GetTopicAttributes", "SetTopicAttributes",
		"Subscribe", "Unsubscribe", "ListSubscriptions", "ListSubscriptionsByTopic",
		"GetSubscriptionAttributes", "ConfirmSubscription",
		"Publish", "PublishBatch", "ListTagsForResource", "TagResource":
		return true
	}
	return false
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameter", "could not parse form")
		return
	}

	action := r.FormValue("Action")
	if action == "" {
		target := r.Header.Get("X-Amz-Target")
		if idx := strings.LastIndex(target, "."); idx != -1 {
			action = target[idx+1:]
		}
	}

	switch action {
	case "CreateTopic":
		s.createTopic(w, r)
	case "DeleteTopic":
		s.deleteTopic(w, r)
	case "ListTopics":
		s.listTopics(w, r)
	case "GetTopicAttributes":
		s.getTopicAttributes(w, r)
	case "SetTopicAttributes":
		s.setTopicAttribute(w, r)
	case "Subscribe":
		s.subscribe(w, r)
	case "Unsubscribe":
		s.unsubscribe(w, r)
	case "ListSubscriptions":
		s.listSubscriptions(w, r)
	case "ListSubscriptionsByTopic":
		s.listSubscriptionsByTopic(w, r)
	case "GetSubscriptionAttributes":
		s.getSubscriptionAttributes(w, r)
	case "ConfirmSubscription":
		s.confirmSubscription(w, r)
	case "Publish":
		s.publish(w, r)
	case "PublishBatch":
		s.publishBatch(w, r)
	case "ListTagsForResource":
		s.listTagsForResource(w, r)
	case "TagResource":
		s.tagResource(w, r)
	default:
		s.xmlError(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("Action %s is not valid.", action))
	}
}

// --- Topic operations ---

func (s *Service) topicARN(name string) string {
	return fmt.Sprintf("arn:aws:sns:%s:%s:%s", s.region, accountID, name)
}

func (s *Service) createTopic(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("Name")
	if name == "" {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameter", "Name is required")
		return
	}

	arn := s.topicARN(name)

	s.mu.Lock()
	if _, exists := s.topics[arn]; !exists {
		s.topics[arn] = &topic{name: name, arn: arn}
		s.byName[name] = arn
	}
	s.mu.Unlock()

	type result struct {
		XMLName xml.Name `xml:"CreateTopicResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			TopicArn string `xml:"TopicArn"`
		} `xml:"CreateTopicResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var resp result
	resp.Xmlns = xmlNS
	resp.Result.TopicArn = arn
	resp.Metadata.RequestID = uid.New()
	xmlWrite(w, http.StatusOK, resp)
}

func (s *Service) deleteTopic(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("TopicArn")

	s.mu.Lock()
	if t, ok := s.topics[arn]; ok {
		for _, subARN := range t.subARNs {
			delete(s.subscriptions, subARN)
		}
		delete(s.byName, t.name)
		delete(s.topics, arn)
	}
	s.mu.Unlock()

	type result struct {
		XMLName  xml.Name         `xml:"DeleteTopicResponse"`
		Xmlns    string           `xml:"xmlns,attr"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	resp := result{Xmlns: xmlNS, Metadata: responseMetadata{RequestID: uid.New()}}
	xmlWrite(w, http.StatusOK, resp)
}

func (s *Service) listTopics(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type member struct {
		TopicArn string `xml:"TopicArn"`
	}
	type result struct {
		XMLName xml.Name `xml:"ListTopicsResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			Topics []member `xml:"Topics>member"`
		} `xml:"ListTopicsResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}

	var resp result
	resp.Xmlns = xmlNS
	resp.Metadata.RequestID = uid.New()
	for _, t := range s.topics {
		resp.Result.Topics = append(resp.Result.Topics, member{TopicArn: t.arn})
	}
	xmlWrite(w, http.StatusOK, resp)
}

func (s *Service) getTopicAttributes(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("TopicArn")

	s.mu.RLock()
	t, ok := s.topics[arn]
	var subCount int
	if ok {
		subCount = len(t.subARNs)
	}
	s.mu.RUnlock()

	if !ok {
		s.xmlError(w, http.StatusBadRequest, "NotFound", "Topic does not exist")
		return
	}

	type entry struct {
		Key   string `xml:"key"`
		Value string `xml:"value"`
	}
	type result struct {
		XMLName xml.Name `xml:"GetTopicAttributesResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			Attributes []entry `xml:"Attributes>entry"`
		} `xml:"GetTopicAttributesResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}

	defaultPolicy := fmt.Sprintf(
		`{"Version":"2012-10-17","Id":"%s/SQSDefaultPolicy","Statement":[]}`,
		t.arn,
	)

	var resp result
	resp.Xmlns = xmlNS
	resp.Metadata.RequestID = uid.New()
	resp.Result.Attributes = []entry{
		{Key: "TopicArn", Value: t.arn},
		{Key: "DisplayName", Value: t.displayName},
		{Key: "SubscriptionsConfirmed", Value: strconv.Itoa(subCount)},
		{Key: "SubscriptionsPending", Value: "0"},
		{Key: "SubscriptionsDeleted", Value: "0"},
		{Key: "Owner", Value: accountID},
		{Key: "Policy", Value: defaultPolicy},
	}
	xmlWrite(w, http.StatusOK, resp)
}

func (s *Service) setTopicAttribute(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("TopicArn")
	attrName := r.FormValue("AttributeName")
	attrValue := r.FormValue("AttributeValue")

	s.mu.Lock()
	t, ok := s.topics[arn]
	if ok && attrName == "DisplayName" {
		t.displayName = attrValue
	}
	s.mu.Unlock()

	if !ok {
		s.xmlError(w, http.StatusBadRequest, "NotFound", "Topic does not exist")
		return
	}

	type result struct {
		XMLName  xml.Name         `xml:"SetTopicAttributesResponse"`
		Xmlns    string           `xml:"xmlns,attr"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	xmlWrite(w, http.StatusOK, result{Xmlns: xmlNS, Metadata: responseMetadata{RequestID: uid.New()}})
}

// --- Subscription operations ---

func (s *Service) subscribe(w http.ResponseWriter, r *http.Request) {
	topicARN := r.FormValue("TopicArn")
	protocol := r.FormValue("Protocol")
	endpoint := r.FormValue("Endpoint")

	if topicARN == "" || protocol == "" {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameter", "TopicArn and Protocol are required")
		return
	}

	s.mu.Lock()
	t, ok := s.topics[topicARN]
	var subARN string
	if ok {
		subARN = fmt.Sprintf("%s:%s", topicARN, uid.New())
		sub := &subscription{
			arn:      subARN,
			topicARN: topicARN,
			protocol: protocol,
			endpoint: endpoint,
		}
		s.subscriptions[subARN] = sub
		t.subARNs = append(t.subARNs, subARN)
	}
	s.mu.Unlock()

	if !ok {
		s.xmlError(w, http.StatusBadRequest, "NotFound", "Topic does not exist")
		return
	}

	type result struct {
		XMLName xml.Name `xml:"SubscribeResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			SubscriptionArn string `xml:"SubscriptionArn"`
		} `xml:"SubscribeResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var resp result
	resp.Xmlns = xmlNS
	resp.Result.SubscriptionArn = subARN
	resp.Metadata.RequestID = uid.New()
	xmlWrite(w, http.StatusOK, resp)
}

func (s *Service) unsubscribe(w http.ResponseWriter, r *http.Request) {
	subARN := r.FormValue("SubscriptionArn")

	s.mu.Lock()
	if sub, ok := s.subscriptions[subARN]; ok {
		if t, ok := s.topics[sub.topicARN]; ok {
			filtered := t.subARNs[:0]
			for _, a := range t.subARNs {
				if a != subARN {
					filtered = append(filtered, a)
				}
			}
			t.subARNs = filtered
		}
		delete(s.subscriptions, subARN)
	}
	s.mu.Unlock()

	type result struct {
		XMLName  xml.Name         `xml:"UnsubscribeResponse"`
		Xmlns    string           `xml:"xmlns,attr"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	xmlWrite(w, http.StatusOK, result{Xmlns: xmlNS, Metadata: responseMetadata{RequestID: uid.New()}})
}

func (s *Service) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type member struct {
		SubscriptionArn string `xml:"SubscriptionArn"`
		TopicArn        string `xml:"TopicArn"`
		Protocol        string `xml:"Protocol"`
		Endpoint        string `xml:"Endpoint"`
		Owner           string `xml:"Owner"`
	}
	type result struct {
		XMLName xml.Name `xml:"ListSubscriptionsResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			Subscriptions []member `xml:"Subscriptions>member"`
		} `xml:"ListSubscriptionsResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}

	var resp result
	resp.Xmlns = xmlNS
	resp.Metadata.RequestID = uid.New()
	for _, sub := range s.subscriptions {
		resp.Result.Subscriptions = append(resp.Result.Subscriptions, member{
			SubscriptionArn: sub.arn,
			TopicArn:        sub.topicARN,
			Protocol:        sub.protocol,
			Endpoint:        sub.endpoint,
			Owner:           accountID,
		})
	}
	xmlWrite(w, http.StatusOK, resp)
}

func (s *Service) listSubscriptionsByTopic(w http.ResponseWriter, r *http.Request) {
	topicARN := r.FormValue("TopicArn")

	s.mu.RLock()
	t, ok := s.topics[topicARN]
	var subs []*subscription
	if ok {
		for _, subARN := range t.subARNs {
			if sub, ok := s.subscriptions[subARN]; ok {
				subs = append(subs, sub)
			}
		}
	}
	s.mu.RUnlock()

	if !ok {
		s.xmlError(w, http.StatusBadRequest, "NotFound", "Topic does not exist")
		return
	}

	type member struct {
		SubscriptionArn string `xml:"SubscriptionArn"`
		TopicArn        string `xml:"TopicArn"`
		Protocol        string `xml:"Protocol"`
		Endpoint        string `xml:"Endpoint"`
		Owner           string `xml:"Owner"`
	}
	type result struct {
		XMLName xml.Name `xml:"ListSubscriptionsByTopicResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			Subscriptions []member `xml:"Subscriptions>member"`
		} `xml:"ListSubscriptionsByTopicResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}

	var resp result
	resp.Xmlns = xmlNS
	resp.Metadata.RequestID = uid.New()
	for _, sub := range subs {
		resp.Result.Subscriptions = append(resp.Result.Subscriptions, member{
			SubscriptionArn: sub.arn,
			TopicArn:        sub.topicARN,
			Protocol:        sub.protocol,
			Endpoint:        sub.endpoint,
			Owner:           accountID,
		})
	}
	xmlWrite(w, http.StatusOK, resp)
}

func (s *Service) getSubscriptionAttributes(w http.ResponseWriter, r *http.Request) {
	subARN := r.FormValue("SubscriptionArn")

	s.mu.RLock()
	sub, ok := s.subscriptions[subARN]
	s.mu.RUnlock()

	if !ok {
		s.xmlError(w, http.StatusBadRequest, "NotFound", "Subscription does not exist")
		return
	}

	type entry struct {
		Key   string `xml:"key"`
		Value string `xml:"value"`
	}
	type result struct {
		XMLName xml.Name `xml:"GetSubscriptionAttributesResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			Attributes []entry `xml:"Attributes>entry"`
		} `xml:"GetSubscriptionAttributesResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}

	var resp result
	resp.Xmlns = xmlNS
	resp.Metadata.RequestID = uid.New()
	resp.Result.Attributes = []entry{
		{Key: "SubscriptionArn", Value: sub.arn},
		{Key: "TopicArn", Value: sub.topicARN},
		{Key: "Protocol", Value: sub.protocol},
		{Key: "Endpoint", Value: sub.endpoint},
		{Key: "Owner", Value: accountID},
		{Key: "RawMessageDelivery", Value: "false"},
		{Key: "PendingConfirmation", Value: "false"},
		{Key: "ConfirmationWasAuthenticated", Value: "true"},
	}
	xmlWrite(w, http.StatusOK, resp)
}

// ConfirmSubscription is a no-op — subscriptions are auto-confirmed.
func (s *Service) confirmSubscription(w http.ResponseWriter, r *http.Request) {
	topicARN := r.FormValue("TopicArn")
	token := r.FormValue("Token")

	// Find a subscription on this topic that matches the token suffix (best-effort).
	subARN := topicARN + ":" + token

	s.mu.RLock()
	_, exists := s.subscriptions[subARN]
	s.mu.RUnlock()

	if !exists {
		// Return first subscription on the topic as a fallback.
		s.mu.RLock()
		if t, ok := s.topics[topicARN]; ok && len(t.subARNs) > 0 {
			subARN = t.subARNs[0]
		}
		s.mu.RUnlock()
	}

	type result struct {
		XMLName xml.Name `xml:"ConfirmSubscriptionResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			SubscriptionArn string `xml:"SubscriptionArn"`
		} `xml:"ConfirmSubscriptionResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var resp result
	resp.Xmlns = xmlNS
	resp.Result.SubscriptionArn = subARN
	resp.Metadata.RequestID = uid.New()
	xmlWrite(w, http.StatusOK, resp)
}

// --- Publish ---

func (s *Service) publish(w http.ResponseWriter, r *http.Request) {
	topicARN := r.FormValue("TopicArn")
	message := r.FormValue("Message")
	subject := r.FormValue("Subject")

	if topicARN == "" {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameter", "TopicArn is required")
		return
	}
	if message == "" {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameter", "Message is required")
		return
	}

	s.mu.RLock()
	_, ok := s.topics[topicARN]
	s.mu.RUnlock()
	if !ok {
		s.xmlError(w, http.StatusBadRequest, "NotFound", "Topic does not exist")
		return
	}

	attrs := collectMessageAttributes(r)
	msgID := uid.New()

	s.mu.Lock()
	s.messages = append(s.messages, &CapturedMessage{
		MessageID:   msgID,
		TopicARN:    topicARN,
		Subject:     subject,
		Message:     message,
		Attributes:  attrs,
		PublishedAt: time.Now().UTC(),
	})
	s.mu.Unlock()

	type result struct {
		XMLName xml.Name `xml:"PublishResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			MessageId string `xml:"MessageId"`
		} `xml:"PublishResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var resp result
	resp.Xmlns = xmlNS
	resp.Result.MessageId = msgID
	resp.Metadata.RequestID = uid.New()
	xmlWrite(w, http.StatusOK, resp)
}

func (s *Service) publishBatch(w http.ResponseWriter, r *http.Request) {
	topicARN := r.FormValue("TopicArn")
	if topicARN == "" {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameter", "TopicArn is required")
		return
	}

	s.mu.RLock()
	_, ok := s.topics[topicARN]
	s.mu.RUnlock()
	if !ok {
		s.xmlError(w, http.StatusBadRequest, "NotFound", "Topic does not exist")
		return
	}

	type successEntry struct {
		ID        string `xml:"Id"`
		MessageId string `xml:"MessageId"`
	}
	var successful []successEntry

	s.mu.Lock()
	for i := 1; ; i++ {
		prefix := fmt.Sprintf("PublishBatchRequestEntries.member.%d", i)
		id := r.FormValue(prefix + ".Id")
		if id == "" {
			break
		}
		message := r.FormValue(prefix + ".Message")
		subject := r.FormValue(prefix + ".Subject")
		msgID := uid.New()
		s.messages = append(s.messages, &CapturedMessage{
			MessageID:   msgID,
			TopicARN:    topicARN,
			Subject:     subject,
			Message:     message,
			PublishedAt: time.Now().UTC(),
		})
		successful = append(successful, successEntry{ID: id, MessageId: msgID})
	}
	s.mu.Unlock()

	type result struct {
		XMLName xml.Name `xml:"PublishBatchResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			Successful []successEntry `xml:"Successful>member"`
			Failed     struct{}       `xml:"Failed"`
		} `xml:"PublishBatchResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var resp result
	resp.Xmlns = xmlNS
	resp.Result.Successful = successful
	resp.Metadata.RequestID = uid.New()
	xmlWrite(w, http.StatusOK, resp)
}

// --- Nimbus inspection endpoints ---

// MessagesHandler serves captured messages at GET /_nimbus/sns/messages.
func (s *Service) MessagesHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if s.messages == nil {
		w.Write([]byte("[]"))
		return
	}
	json.NewEncoder(w).Encode(s.messages)
}

// ClearMessagesHandler clears all captured messages. DELETE /_nimbus/sns/messages
func (s *Service) ClearMessagesHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.messages = nil
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// MessageCount returns the number of captured published messages.
func (s *Service) MessageCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages)
}

// listTagsForResource — returns empty tags; tags are not stored in Nimbus
func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	type result struct {
		XMLName xml.Name `xml:"ListTagsForResourceResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			Tags []struct{} `xml:"Tags>member"`
		} `xml:"ListTagsForResourceResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	resp := result{Xmlns: xmlNS, Metadata: responseMetadata{RequestID: uid.New()}}
	xmlWrite(w, http.StatusOK, resp)
}

// tagResource — accepts tags but does not store them
func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	type result struct {
		XMLName  xml.Name         `xml:"TagResourceResponse"`
		Xmlns    string           `xml:"xmlns,attr"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	xmlWrite(w, http.StatusOK, result{Xmlns: xmlNS, Metadata: responseMetadata{RequestID: uid.New()}})
}

// --- Helpers ---

// collectMessageAttributes reads SNS MessageAttributes from the form.
// SNS encodes them as MessageAttributes.entry.N.Name / .Value.DataType / .Value.StringValue
func collectMessageAttributes(r *http.Request) map[string]string {
	attrs := map[string]string{}
	for i := 1; ; i++ {
		name := r.FormValue(fmt.Sprintf("MessageAttributes.entry.%d.Name", i))
		if name == "" {
			break
		}
		val := r.FormValue(fmt.Sprintf("MessageAttributes.entry.%d.Value.StringValue", i))
		if val == "" {
			val = r.FormValue(fmt.Sprintf("MessageAttributes.entry.%d.Value.BinaryValue", i))
		}
		attrs[name] = val
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

type responseMetadata struct {
	RequestID string `xml:"RequestId"`
}

func xmlWrite(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	fmt.Fprint(w, xmlHeader)
	xml.NewEncoder(w).Encode(v)
}

func (s *Service) xmlError(w http.ResponseWriter, status int, code, message string) {
	type errResp struct {
		XMLName xml.Name `xml:"ErrorResponse"`
		Error   struct {
			Type    string `xml:"Type"`
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Error"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var resp errResp
	resp.Error.Type = "Sender"
	resp.Error.Code = code
	resp.Error.Message = message
	resp.Metadata.RequestID = uid.New()
	xmlWrite(w, status, resp)
}
