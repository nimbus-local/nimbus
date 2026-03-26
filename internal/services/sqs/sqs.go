package sqs

import (
	"crypto/md5"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

// Service implements the SQS emulator.
// All state is in-memory. Queues and messages survive for the lifetime
// of the container. For persistence across restarts, mount a volume and
// set NIMBUS_DATA_DIR — a future version will serialize state to disk.
type Service struct {
	mu     sync.RWMutex
	queues map[string]*queue // keyed by queue URL
	byName map[string]string // name -> queue URL
	region string
	host   string
}

type queue struct {
	name              string
	url               string
	arn               string
	attributes        map[string]string
	messages          []*message
	inflightByReceipt map[string]*inFlight
	mu                sync.Mutex
}

type message struct {
	id           string
	body         string
	md5          string
	attributes   map[string]string
	receiveCount int
	sentAt       time.Time
	visibleAt    time.Time
}

type inFlight struct {
	msg           *message
	receiptHandle string
	visibleAt     time.Time
}

const (
	defaultVisibilityTimeout = 30
	defaultWaitSeconds       = 0
	defaultMaxMessages       = 1
	accountID                = "000000000000"
)

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region: region,
		queues: map[string]*queue{},
		byName: map[string]string{},
	}
}

func (s *Service) Name() string { return "sqs" }

// Detect identifies SQS requests by the Action query parameter or
// X-Amz-Target header containing "AmazonSQS"
func (s *Service) Detect(r *http.Request) bool {
	target := r.Header.Get("X-Amz-Target")
	if strings.HasPrefix(target, "AmazonSQS") {
		return true
	}
	action := r.URL.Query().Get("Action")
	if action != "" {
		// Only claim it if it's a known SQS action
		return isSQSAction(action)
	}
	// SQS path style: /:accountId/:queueName
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] == accountID {
		return true
	}
	return false
}

func isSQSAction(action string) bool {
	switch action {
	case "CreateQueue", "DeleteQueue", "GetQueueUrl", "GetQueueAttributes",
		"SetQueueAttributes", "ListQueues", "SendMessage", "SendMessageBatch",
		"ReceiveMessage", "DeleteMessage", "DeleteMessageBatch", "PurgeQueue",
		"ChangeMessageVisibility":
		return true
	}
	return false
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameterValue", "could not parse form")
		return
	}

	action := r.FormValue("Action")
	if action == "" {
		// JSON/AWS SDK v2 style uses X-Amz-Target
		target := r.Header.Get("X-Amz-Target")
		if idx := strings.LastIndex(target, "."); idx != -1 {
			action = target[idx+1:]
		}
	}

	switch action {
	case "CreateQueue":
		s.createQueue(w, r)
	case "DeleteQueue":
		s.deleteQueue(w, r)
	case "GetQueueUrl":
		s.getQueueURL(w, r)
	case "GetQueueAttributes":
		s.getQueueAttributes(w, r)
	case "SetQueueAttributes":
		s.setQueueAttributes(w, r)
	case "ListQueues":
		s.listQueues(w, r)
	case "SendMessage":
		s.sendMessage(w, r)
	case "ReceiveMessage":
		s.receiveMessage(w, r)
	case "DeleteMessage":
		s.deleteMessage(w, r)
	case "PurgeQueue":
		s.purgeQueue(w, r)
	case "ChangeMessageVisibility":
		s.changeMessageVisibility(w, r)
	default:
		s.xmlError(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("The action %s is not valid for this endpoint.", action))
	}
}

// --- Queue management ---

func (s *Service) queueURL(name string) string {
	return fmt.Sprintf("http://sqs.%s.localhost:4566/%s/%s", s.region, accountID, name)
}

func (s *Service) queueARN(name string) string {
	return fmt.Sprintf("arn:aws:sqs:%s:%s:%s", s.region, accountID, name)
}

func (s *Service) createQueue(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("QueueName")
	if name == "" {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameterValue", "QueueName is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	qURL := s.queueURL(name)

	if _, exists := s.byName[name]; !exists {
		attrs := map[string]string{
			"VisibilityTimeout":             strconv.Itoa(defaultVisibilityTimeout),
			"MaximumMessageSize":            "262144",
			"MessageRetentionPeriod":        "345600",
			"ReceiveMessageWaitTimeSeconds": "0",
			"ApproximateNumberOfMessages":   "0",
			"CreatedTimestamp":              strconv.FormatInt(time.Now().Unix(), 10),
			"LastModifiedTimestamp":         strconv.FormatInt(time.Now().Unix(), 10),
			"QueueArn":                      s.queueARN(name),
		}

		// Override with provided attributes
		for i := 1; ; i++ {
			k := r.FormValue(fmt.Sprintf("Attribute.%d.Name", i))
			v := r.FormValue(fmt.Sprintf("Attribute.%d.Value", i))
			if k == "" {
				break
			}
			attrs[k] = v
		}

		s.queues[qURL] = &queue{
			name:              name,
			url:               qURL,
			arn:               s.queueARN(name),
			attributes:        attrs,
			inflightByReceipt: map[string]*inFlight{},
		}
		s.byName[name] = qURL
	}

	type result struct {
		XMLName xml.Name `xml:"CreateQueueResponse"`
		Result  struct {
			QueueUrl string `xml:"QueueUrl"`
		} `xml:"CreateQueueResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}

	var res result
	res.Result.QueueUrl = qURL
	res.Metadata.RequestId = uid.New()
	xmlWrite(w, http.StatusOK, res)
}

func (s *Service) deleteQueue(w http.ResponseWriter, r *http.Request) {
	qURL := r.FormValue("QueueUrl")
	s.mu.Lock()
	q, ok := s.queues[qURL]
	if ok {
		delete(s.byName, q.name)
		delete(s.queues, qURL)
	}
	s.mu.Unlock()

	type result struct {
		XMLName  xml.Name         `xml:"DeleteQueueResponse"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	xmlWrite(w, http.StatusOK, result{Metadata: responseMetadata{RequestId: uid.New()}})
}

func (s *Service) getQueueURL(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("QueueName")
	s.mu.RLock()
	qURL, ok := s.byName[name]
	s.mu.RUnlock()

	if !ok {
		s.xmlError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	type result struct {
		XMLName xml.Name `xml:"GetQueueUrlResponse"`
		Result  struct {
			QueueUrl string `xml:"QueueUrl"`
		} `xml:"GetQueueUrlResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var res result
	res.Result.QueueUrl = qURL
	res.Metadata.RequestId = uid.New()
	xmlWrite(w, http.StatusOK, res)
}

func (s *Service) listQueues(w http.ResponseWriter, r *http.Request) {
	prefix := r.FormValue("QueueNamePrefix")
	s.mu.RLock()
	defer s.mu.RUnlock()

	type result struct {
		XMLName xml.Name `xml:"ListQueuesResponse"`
		Result  struct {
			QueueUrl []string `xml:"QueueUrl"`
		} `xml:"ListQueuesResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var res result
	for name, qURL := range s.byName {
		if prefix == "" || strings.HasPrefix(name, prefix) {
			res.Result.QueueUrl = append(res.Result.QueueUrl, qURL)
		}
	}
	res.Metadata.RequestId = uid.New()
	xmlWrite(w, http.StatusOK, res)
}

func (s *Service) getQueueAttributes(w http.ResponseWriter, r *http.Request) {
	q := s.findQueue(r)
	if q == nil {
		s.xmlError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	q.mu.Lock()
	// Update approximate message count
	q.attributes["ApproximateNumberOfMessages"] = strconv.Itoa(len(q.messages))
	q.attributes["ApproximateNumberOfMessagesNotVisible"] = strconv.Itoa(len(q.inflightByReceipt))
	q.mu.Unlock()

	type attr struct {
		Name  string `xml:"Name"`
		Value string `xml:"Value"`
	}
	type result struct {
		XMLName xml.Name `xml:"GetQueueAttributesResponse"`
		Result  struct {
			Attribute []attr `xml:"Attribute"`
		} `xml:"GetQueueAttributesResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}

	var res result
	for k, v := range q.attributes {
		res.Result.Attribute = append(res.Result.Attribute, attr{Name: k, Value: v})
	}
	res.Metadata.RequestId = uid.New()
	xmlWrite(w, http.StatusOK, res)
}

func (s *Service) setQueueAttributes(w http.ResponseWriter, r *http.Request) {
	q := s.findQueue(r)
	if q == nil {
		s.xmlError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	q.mu.Lock()
	for i := 1; ; i++ {
		k := r.FormValue(fmt.Sprintf("Attribute.%d.Name", i))
		v := r.FormValue(fmt.Sprintf("Attribute.%d.Value", i))
		if k == "" {
			break
		}
		q.attributes[k] = v
	}
	q.mu.Unlock()

	type result struct {
		XMLName  xml.Name         `xml:"SetQueueAttributesResponse"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	xmlWrite(w, http.StatusOK, result{Metadata: responseMetadata{RequestId: uid.New()}})
}

// --- Message operations ---

func (s *Service) sendMessage(w http.ResponseWriter, r *http.Request) {
	q := s.findQueue(r)
	if q == nil {
		s.xmlError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	body := r.FormValue("MessageBody")
	sum := md5.Sum([]byte(body))
	msgID := uid.New()

	msg := &message{
		id:        msgID,
		body:      body,
		md5:       fmt.Sprintf("%x", sum),
		sentAt:    time.Now(),
		visibleAt: time.Now(),
	}

	q.mu.Lock()
	q.messages = append(q.messages, msg)
	q.mu.Unlock()

	type result struct {
		XMLName xml.Name `xml:"SendMessageResponse"`
		Result  struct {
			MD5OfMessageBody string `xml:"MD5OfMessageBody"`
			MessageId        string `xml:"MessageId"`
		} `xml:"SendMessageResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var res result
	res.Result.MD5OfMessageBody = msg.md5
	res.Result.MessageId = msgID
	res.Metadata.RequestId = uid.New()
	xmlWrite(w, http.StatusOK, res)
}

func (s *Service) receiveMessage(w http.ResponseWriter, r *http.Request) {
	q := s.findQueue(r)
	if q == nil {
		s.xmlError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	maxStr := r.FormValue("MaxNumberOfMessages")
	max := defaultMaxMessages
	if maxStr != "" {
		fmt.Sscanf(maxStr, "%d", &max)
	}
	if max > 10 {
		max = 10
	}

	vtStr := r.FormValue("VisibilityTimeout")
	vt := defaultVisibilityTimeout
	if vtStr != "" {
		fmt.Sscanf(vtStr, "%d", &vt)
	}

	now := time.Now()

	q.mu.Lock()
	// Recover any expired in-flight messages
	for receipt, inf := range q.inflightByReceipt {
		if now.After(inf.visibleAt) {
			inf.msg.visibleAt = now
			q.messages = append([]*message{inf.msg}, q.messages...)
			delete(q.inflightByReceipt, receipt)
		}
	}

	var received []*message
	var remaining []*message
	for _, msg := range q.messages {
		if len(received) < max && !now.Before(msg.visibleAt) {
			received = append(received, msg)
		} else {
			remaining = append(remaining, msg)
		}
	}
	q.messages = remaining

	// Move received messages to in-flight
	for _, msg := range received {
		receipt := uid.New()
		msg.visibleAt = now.Add(time.Duration(vt) * time.Second)
		msg.receiveCount++
		q.inflightByReceipt[receipt] = &inFlight{
			msg:           msg,
			receiptHandle: receipt,
			visibleAt:     msg.visibleAt,
		}
	}
	q.mu.Unlock()

	type msgXML struct {
		MessageId     string `xml:"MessageId"`
		ReceiptHandle string `xml:"ReceiptHandle"`
		MD5OfBody     string `xml:"MD5OfBody"`
		Body          string `xml:"Body"`
	}
	type result struct {
		XMLName xml.Name `xml:"ReceiveMessageResponse"`
		Result  struct {
			Message []msgXML `xml:"Message"`
		} `xml:"ReceiveMessageResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}

	var res result
	for receipt, inf := range q.inflightByReceipt {
		for _, recv := range received {
			if recv == inf.msg {
				res.Result.Message = append(res.Result.Message, msgXML{
					MessageId:     recv.id,
					ReceiptHandle: receipt,
					MD5OfBody:     recv.md5,
					Body:          recv.body,
				})
			}
		}
	}
	res.Metadata.RequestId = uid.New()
	xmlWrite(w, http.StatusOK, res)
}

func (s *Service) deleteMessage(w http.ResponseWriter, r *http.Request) {
	q := s.findQueue(r)
	if q == nil {
		s.xmlError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	receipt := r.FormValue("ReceiptHandle")
	q.mu.Lock()
	delete(q.inflightByReceipt, receipt)
	q.mu.Unlock()

	type result struct {
		XMLName  xml.Name         `xml:"DeleteMessageResponse"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	xmlWrite(w, http.StatusOK, result{Metadata: responseMetadata{RequestId: uid.New()}})
}

func (s *Service) purgeQueue(w http.ResponseWriter, r *http.Request) {
	q := s.findQueue(r)
	if q == nil {
		s.xmlError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	q.mu.Lock()
	q.messages = nil
	q.inflightByReceipt = map[string]*inFlight{}
	q.mu.Unlock()

	type result struct {
		XMLName  xml.Name         `xml:"PurgeQueueResponse"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	xmlWrite(w, http.StatusOK, result{Metadata: responseMetadata{RequestId: uid.New()}})
}

func (s *Service) changeMessageVisibility(w http.ResponseWriter, r *http.Request) {
	q := s.findQueue(r)
	if q == nil {
		s.xmlError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	receipt := r.FormValue("ReceiptHandle")
	vtStr := r.FormValue("VisibilityTimeout")
	vt := 0
	fmt.Sscanf(vtStr, "%d", &vt)

	q.mu.Lock()
	if inf, ok := q.inflightByReceipt[receipt]; ok {
		inf.visibleAt = time.Now().Add(time.Duration(vt) * time.Second)
	}
	q.mu.Unlock()

	type result struct {
		XMLName  xml.Name         `xml:"ChangeMessageVisibilityResponse"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	xmlWrite(w, http.StatusOK, result{Metadata: responseMetadata{RequestId: uid.New()}})
}

// --- Helpers ---

func (s *Service) findQueue(r *http.Request) *queue {
	qURL := r.FormValue("QueueUrl")
	if qURL == "" {
		// Try to find from path: /:accountId/:queueName
		path := strings.Trim(r.URL.Path, "/")
		parts := strings.Split(path, "/")
		if len(parts) == 2 {
			s.mu.RLock()
			qURL = s.byName[parts[1]]
			s.mu.RUnlock()
		}
	}

	// Normalize URL - strip trailing slash, query params
	if u, err := url.Parse(qURL); err == nil {
		qURL = fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, u.Path)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queues[qURL]
}

type responseMetadata struct {
	RequestId string `xml:"RequestId"`
}

func xmlWrite(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`)
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
	var res errResp
	res.Error.Type = "Sender"
	res.Error.Code = code
	res.Error.Message = message
	res.Metadata.RequestId = uid.New()
	xmlWrite(w, status, res)
}
