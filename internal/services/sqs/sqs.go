package sqs

import (
	"crypto/md5"
	"encoding/json"
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

// Reset clears all queues and their messages.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queues = map[string]*queue{}
	s.byName = map[string]string{}
}

// QueueCount returns the number of queues.
func (s *Service) QueueCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.queues)
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
		return isSQSAction(action)
	}
	// SQS path style: /:accountId/:queueName
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] == accountID {
		return true
	}
	// Also detect form-body Action for traditional query protocol
	r.ParseForm()
	if isSQSAction(r.Form.Get("Action")) {
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

// sqsCtx captures the request protocol (JSON vs. form/query) and the decoded body.
type sqsCtx struct {
	useJSON bool
	body    map[string]interface{}
}

func newSQSCtx(r *http.Request) sqsCtx {
	target := r.Header.Get("X-Amz-Target")
	ct := r.Header.Get("Content-Type")
	if target != "" || strings.Contains(ct, "application/x-amz-json") {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body == nil {
			body = map[string]interface{}{}
		}
		return sqsCtx{useJSON: true, body: body}
	}
	return sqsCtx{useJSON: false}
}

// str reads a string parameter from either JSON body or form values.
func (c *sqsCtx) str(key string, r *http.Request) string {
	if c.useJSON {
		v, _ := c.body[key].(string)
		return v
	}
	return r.FormValue(key)
}

// intVal reads an integer parameter.
func (c *sqsCtx) intVal(key string, r *http.Request) (int, bool) {
	if c.useJSON {
		switch v := c.body[key].(type) {
		case float64:
			return int(v), true
		case string:
			n, err := strconv.Atoi(v)
			return n, err == nil
		}
		return 0, false
	}
	s := r.FormValue(key)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// attrs reads the Attributes map (JSON: {"Attributes": {"K":"V"}} vs form Attribute.N.Name/Value).
func (c *sqsCtx) attrs(r *http.Request) map[string]string {
	if c.useJSON {
		raw, _ := c.body["Attributes"].(map[string]interface{})
		if raw == nil {
			return nil
		}
		m := make(map[string]string, len(raw))
		for k, v := range raw {
			if sv, ok := v.(string); ok {
				m[k] = sv
			}
		}
		return m
	}
	m := map[string]string{}
	for i := 1; ; i++ {
		k := r.FormValue(fmt.Sprintf("Attribute.%d.Name", i))
		v := r.FormValue(fmt.Sprintf("Attribute.%d.Value", i))
		if k == "" {
			break
		}
		m[k] = v
	}
	return m
}

// writeOK writes either a JSON map or an XML struct depending on protocol.
func (c *sqsCtx) writeOK(w http.ResponseWriter, jsonVal interface{}, xmlVal interface{}) {
	if c.useJSON {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(jsonVal)
	} else {
		xmlWrite(w, http.StatusOK, xmlVal)
	}
}

// writeError writes a protocol-appropriate error response.
func (c *sqsCtx) writeError(w http.ResponseWriter, status int, code, msg string) {
	if c.useJSON {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"__type": code, "message": msg})
	} else {
		sqsXMLError(w, status, code, msg)
	}
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		sqsXMLError(w, http.StatusBadRequest, "InvalidParameterValue", "could not parse form")
		return
	}

	ctx := newSQSCtx(r)

	action := r.FormValue("Action")
	if action == "" {
		target := r.Header.Get("X-Amz-Target")
		if idx := strings.LastIndex(target, "."); idx != -1 {
			action = target[idx+1:]
		}
	}

	switch action {
	case "CreateQueue":
		s.createQueue(w, r, ctx)
	case "DeleteQueue":
		s.deleteQueue(w, r, ctx)
	case "GetQueueUrl":
		s.getQueueURL(w, r, ctx)
	case "GetQueueAttributes":
		s.getQueueAttributes(w, r, ctx)
	case "SetQueueAttributes":
		s.setQueueAttributes(w, r, ctx)
	case "ListQueues":
		s.listQueues(w, r, ctx)
	case "SendMessage":
		s.sendMessage(w, r, ctx)
	case "ReceiveMessage":
		s.receiveMessage(w, r, ctx)
	case "DeleteMessage":
		s.deleteMessage(w, r, ctx)
	case "PurgeQueue":
		s.purgeQueue(w, r, ctx)
	case "ChangeMessageVisibility":
		s.changeMessageVisibility(w, r, ctx)
	case "ListQueueTags":
		s.listQueueTags(w, r, ctx)
	case "TagQueue":
		s.tagQueue(w, r, ctx)
	case "UntagQueue":
		ctx.writeOK(w, map[string]interface{}{}, struct {
			XMLName  xml.Name         `xml:"UntagQueueResponse"`
			Metadata responseMetadata `xml:"ResponseMetadata"`
		}{Metadata: responseMetadata{RequestId: uid.New()}})
	default:
		ctx.writeError(w, http.StatusBadRequest, "InvalidAction",
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

func (s *Service) createQueue(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	name := ctx.str("QueueName", r)
	if name == "" {
		ctx.writeError(w, http.StatusBadRequest, "InvalidParameterValue", "QueueName is required")
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
		for k, v := range ctx.attrs(r) {
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

	type xmlResult struct {
		XMLName xml.Name `xml:"CreateQueueResponse"`
		Result  struct {
			QueueUrl string `xml:"QueueUrl"`
		} `xml:"CreateQueueResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}

	var xmlRes xmlResult
	xmlRes.Result.QueueUrl = qURL
	xmlRes.Metadata.RequestId = uid.New()

	ctx.writeOK(w, map[string]string{"QueueUrl": qURL}, xmlRes)
}

func (s *Service) deleteQueue(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	qURL := ctx.str("QueueUrl", r)
	s.mu.Lock()
	q, ok := s.queues[qURL]
	if ok {
		delete(s.byName, q.name)
		delete(s.queues, qURL)
	}
	s.mu.Unlock()

	type xmlResult struct {
		XMLName  xml.Name         `xml:"DeleteQueueResponse"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	ctx.writeOK(w, map[string]interface{}{}, xmlResult{Metadata: responseMetadata{RequestId: uid.New()}})
}

func (s *Service) getQueueURL(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	name := ctx.str("QueueName", r)
	s.mu.RLock()
	qURL, ok := s.byName[name]
	s.mu.RUnlock()

	if !ok {
		// GetQueueUrl SDK waiter checks for "QueueDoesNotExist" (short JSON code).
		// Other operations (e.g. GetQueueAttributes) must keep returning the long code
		// so Terraform's waitQueueDeleted (tfawserr.ErrCodeEquals) works correctly.
		errCode := "AWS.SimpleQueueService.NonExistentQueue"
		if ctx.useJSON {
			errCode = "QueueDoesNotExist"
		}
		ctx.writeError(w, http.StatusBadRequest, errCode, "The specified queue does not exist.")
		return
	}

	type xmlResult struct {
		XMLName xml.Name `xml:"GetQueueUrlResponse"`
		Result  struct {
			QueueUrl string `xml:"QueueUrl"`
		} `xml:"GetQueueUrlResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var xmlRes xmlResult
	xmlRes.Result.QueueUrl = qURL
	xmlRes.Metadata.RequestId = uid.New()
	ctx.writeOK(w, map[string]string{"QueueUrl": qURL}, xmlRes)
}

func (s *Service) listQueues(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	prefix := ctx.str("QueueNamePrefix", r)
	s.mu.RLock()
	defer s.mu.RUnlock()

	type xmlResult struct {
		XMLName xml.Name `xml:"ListQueuesResponse"`
		Result  struct {
			QueueUrl []string `xml:"QueueUrl"`
		} `xml:"ListQueuesResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var xmlRes xmlResult
	var jsonURLs []string
	for name, qURL := range s.byName {
		if prefix == "" || strings.HasPrefix(name, prefix) {
			xmlRes.Result.QueueUrl = append(xmlRes.Result.QueueUrl, qURL)
			jsonURLs = append(jsonURLs, qURL)
		}
	}
	xmlRes.Metadata.RequestId = uid.New()
	ctx.writeOK(w, map[string]interface{}{"QueueUrls": jsonURLs}, xmlRes)
}

func (s *Service) getQueueAttributes(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	qURL := ctx.str("QueueUrl", r)
	q := s.findQueueByURL(qURL, r)
	if q == nil {
		ctx.writeError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	q.mu.Lock()
	q.attributes["ApproximateNumberOfMessages"] = strconv.Itoa(len(q.messages))
	q.attributes["ApproximateNumberOfMessagesNotVisible"] = strconv.Itoa(len(q.inflightByReceipt))
	q.mu.Unlock()

	// Determine which attributes to return
	var requestedNames []string
	if ctx.useJSON {
		if names, ok := ctx.body["AttributeNames"].([]interface{}); ok {
			for _, n := range names {
				if s, ok := n.(string); ok {
					requestedNames = append(requestedNames, s)
				}
			}
		}
	} else {
		for i := 1; ; i++ {
			n := r.FormValue(fmt.Sprintf("AttributeName.%d", i))
			if n == "" {
				break
			}
			requestedNames = append(requestedNames, n)
		}
	}

	wantAll := len(requestedNames) == 0
	for _, n := range requestedNames {
		if n == "All" {
			wantAll = true
		}
	}

	wantAttr := func(name string) bool {
		if wantAll {
			return true
		}
		for _, n := range requestedNames {
			if n == name {
				return true
			}
		}
		return false
	}

	type xmlAttr struct {
		Name  string `xml:"Name"`
		Value string `xml:"Value"`
	}
	type xmlResult struct {
		XMLName xml.Name `xml:"GetQueueAttributesResponse"`
		Result  struct {
			Attribute []xmlAttr `xml:"Attribute"`
		} `xml:"GetQueueAttributesResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}

	jsonAttrs := map[string]string{}
	var xmlRes xmlResult
	q.mu.Lock()
	for k, v := range q.attributes {
		if wantAttr(k) {
			xmlRes.Result.Attribute = append(xmlRes.Result.Attribute, xmlAttr{Name: k, Value: v})
			jsonAttrs[k] = v
		}
	}
	q.mu.Unlock()
	xmlRes.Metadata.RequestId = uid.New()
	ctx.writeOK(w, map[string]interface{}{"Attributes": jsonAttrs}, xmlRes)
}

func (s *Service) setQueueAttributes(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	qURL := ctx.str("QueueUrl", r)
	q := s.findQueueByURL(qURL, r)
	if q == nil {
		ctx.writeError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	q.mu.Lock()
	for k, v := range ctx.attrs(r) {
		q.attributes[k] = v
	}
	q.mu.Unlock()

	type xmlResult struct {
		XMLName  xml.Name         `xml:"SetQueueAttributesResponse"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	ctx.writeOK(w, map[string]interface{}{}, xmlResult{Metadata: responseMetadata{RequestId: uid.New()}})
}

// --- Message operations ---

func (s *Service) sendMessage(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	qURL := ctx.str("QueueUrl", r)
	q := s.findQueueByURL(qURL, r)
	if q == nil {
		ctx.writeError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	body := ctx.str("MessageBody", r)
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

	type xmlResult struct {
		XMLName xml.Name `xml:"SendMessageResponse"`
		Result  struct {
			MD5OfMessageBody string `xml:"MD5OfMessageBody"`
			MessageId        string `xml:"MessageId"`
		} `xml:"SendMessageResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	var xmlRes xmlResult
	xmlRes.Result.MD5OfMessageBody = msg.md5
	xmlRes.Result.MessageId = msgID
	xmlRes.Metadata.RequestId = uid.New()
	ctx.writeOK(w, map[string]string{
		"MD5OfMessageBody": msg.md5,
		"MessageId":        msgID,
	}, xmlRes)
}

func (s *Service) receiveMessage(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	qURL := ctx.str("QueueUrl", r)
	q := s.findQueueByURL(qURL, r)
	if q == nil {
		ctx.writeError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	max := defaultMaxMessages
	if n, ok := ctx.intVal("MaxNumberOfMessages", r); ok {
		max = n
	}
	if max > 10 {
		max = 10
	}

	vt := defaultVisibilityTimeout
	if n, ok := ctx.intVal("VisibilityTimeout", r); ok {
		vt = n
	}

	now := time.Now()

	q.mu.Lock()
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

	receiptMap := map[*message]string{}
	for _, msg := range received {
		receipt := uid.New()
		msg.visibleAt = now.Add(time.Duration(vt) * time.Second)
		msg.receiveCount++
		q.inflightByReceipt[receipt] = &inFlight{
			msg:           msg,
			receiptHandle: receipt,
			visibleAt:     msg.visibleAt,
		}
		receiptMap[msg] = receipt
	}
	q.mu.Unlock()

	type xmlMsg struct {
		MessageId     string `xml:"MessageId"`
		ReceiptHandle string `xml:"ReceiptHandle"`
		MD5OfBody     string `xml:"MD5OfBody"`
		Body          string `xml:"Body"`
	}
	type xmlResult struct {
		XMLName xml.Name `xml:"ReceiveMessageResponse"`
		Result  struct {
			Message []xmlMsg `xml:"Message"`
		} `xml:"ReceiveMessageResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}

	type jsonMsg struct {
		MessageId     string `json:"MessageId"`
		ReceiptHandle string `json:"ReceiptHandle"`
		MD5OfBody     string `json:"MD5OfBody"`
		Body          string `json:"Body"`
	}

	var xmlRes xmlResult
	var jsonMsgs []jsonMsg
	for _, msg := range received {
		receipt := receiptMap[msg]
		xmlRes.Result.Message = append(xmlRes.Result.Message, xmlMsg{
			MessageId:     msg.id,
			ReceiptHandle: receipt,
			MD5OfBody:     msg.md5,
			Body:          msg.body,
		})
		jsonMsgs = append(jsonMsgs, jsonMsg{
			MessageId:     msg.id,
			ReceiptHandle: receipt,
			MD5OfBody:     msg.md5,
			Body:          msg.body,
		})
	}
	xmlRes.Metadata.RequestId = uid.New()

	if jsonMsgs == nil {
		jsonMsgs = []jsonMsg{}
	}
	ctx.writeOK(w, map[string]interface{}{"Messages": jsonMsgs}, xmlRes)
}

func (s *Service) deleteMessage(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	qURL := ctx.str("QueueUrl", r)
	q := s.findQueueByURL(qURL, r)
	if q == nil {
		ctx.writeError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	receipt := ctx.str("ReceiptHandle", r)
	q.mu.Lock()
	delete(q.inflightByReceipt, receipt)
	q.mu.Unlock()

	type xmlResult struct {
		XMLName  xml.Name         `xml:"DeleteMessageResponse"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	ctx.writeOK(w, map[string]interface{}{}, xmlResult{Metadata: responseMetadata{RequestId: uid.New()}})
}

func (s *Service) purgeQueue(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	qURL := ctx.str("QueueUrl", r)
	q := s.findQueueByURL(qURL, r)
	if q == nil {
		ctx.writeError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	q.mu.Lock()
	q.messages = nil
	q.inflightByReceipt = map[string]*inFlight{}
	q.mu.Unlock()

	type xmlResult struct {
		XMLName  xml.Name         `xml:"PurgeQueueResponse"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	ctx.writeOK(w, map[string]interface{}{}, xmlResult{Metadata: responseMetadata{RequestId: uid.New()}})
}

func (s *Service) changeMessageVisibility(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	qURL := ctx.str("QueueUrl", r)
	q := s.findQueueByURL(qURL, r)
	if q == nil {
		ctx.writeError(w, http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue",
			"The specified queue does not exist.")
		return
	}

	receipt := ctx.str("ReceiptHandle", r)
	vt := 0
	if n, ok := ctx.intVal("VisibilityTimeout", r); ok {
		vt = n
	}

	q.mu.Lock()
	if inf, ok := q.inflightByReceipt[receipt]; ok {
		inf.visibleAt = time.Now().Add(time.Duration(vt) * time.Second)
	}
	q.mu.Unlock()

	type xmlResult struct {
		XMLName  xml.Name         `xml:"ChangeMessageVisibilityResponse"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	ctx.writeOK(w, map[string]interface{}{}, xmlResult{Metadata: responseMetadata{RequestId: uid.New()}})
}

func (s *Service) listQueueTags(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	type xmlResult struct {
		XMLName  xml.Name         `xml:"ListQueueTagsResponse"`
		Result   struct{}         `xml:"ListQueueTagsResult"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	ctx.writeOK(w, map[string]interface{}{"Tags": map[string]string{}},
		xmlResult{Metadata: responseMetadata{RequestId: uid.New()}})
}

func (s *Service) tagQueue(w http.ResponseWriter, r *http.Request, ctx sqsCtx) {
	type xmlResult struct {
		XMLName  xml.Name         `xml:"TagQueueResponse"`
		Metadata responseMetadata `xml:"ResponseMetadata"`
	}
	ctx.writeOK(w, map[string]interface{}{}, xmlResult{Metadata: responseMetadata{RequestId: uid.New()}})
}

// --- Helpers ---

func (s *Service) findQueueByURL(qURL string, r *http.Request) *queue {
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

func sqsXMLError(w http.ResponseWriter, status int, code, message string) {
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
