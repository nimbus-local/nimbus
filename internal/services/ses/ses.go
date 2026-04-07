package ses

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>`

// Service implements the AWS SES emulator.
// Emails are never actually sent — they are captured in memory and
// exposed via a Nimbus-specific endpoint for inspection during testing.
//
// Captured emails are available at:
//
//	GET /_nimbus/ses/messages
type Service struct {
	mu         sync.RWMutex
	messages   []*CapturedEmail
	identities map[string]bool // verified email addresses / domains
	region     string
	account    string
}

// CapturedEmail represents an email that was "sent" via SES.
// Exposed via the /_nimbus/ses/messages endpoint.
type CapturedEmail struct {
	MessageID string    `json:"MessageId"`
	From      string    `json:"From"`
	To        []string  `json:"To"`
	CC        []string  `json:"CC,omitempty"`
	BCC       []string  `json:"BCC,omitempty"`
	ReplyTo   []string  `json:"ReplyTo,omitempty"`
	Subject   string    `json:"Subject"`
	Body      emailBody `json:"Body"`
	SentAt    time.Time `json:"SentAt"`
	Raw       string    `json:"Raw,omitempty"` // populated for SendRawEmail
}

type emailBody struct {
	Text string `json:"Text,omitempty"`
	HTML string `json:"HTML,omitempty"`
}

const defaultAccount = "000000000000"

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		identities: map[string]bool{},
		region:     region,
		account:    defaultAccount,
	}
}

func (s *Service) Name() string { return "ses" }

// Detect identifies SES requests by X-Amz-Target header or Action param.
// SES v1 uses query-param style (Action=SendEmail).
// SES v2 uses X-Amz-Target: AmazonSimpleEmailService.* or path /v2/email/...
func (s *Service) Detect(r *http.Request) bool {
	target := r.Header.Get("X-Amz-Target")
	if strings.HasPrefix(target, "AmazonSimpleEmailService") {
		return true
	}
	// SES v2 path style
	if strings.HasPrefix(r.URL.Path, "/v2/email/") {
		return true
	}
	// SES v1 — AWS CLI sends Action in the POST body, not the URL query string.
	// ParseForm is safe to call multiple times (idempotent — caches on first call).
	// r.Form combines both URL query params and body form values.
	r.ParseForm()
	if isSESAction(r.Form.Get("Action")) {
		return true
	}
	return false
}

func isSESAction(action string) bool {
	switch action {
	case "SendEmail", "SendRawEmail", "VerifyEmailIdentity",
		"VerifyEmailAddress", "ListIdentities", "DeleteIdentity",
		"GetSendQuota", "GetSendStatistics":
		return true
	}
	return false
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// SES v2 path-based routing
	if strings.HasPrefix(r.URL.Path, "/v2/email/outbound-emails") {
		s.sendEmailV2(w, r)
		return
	}

	// SES v1 — action from X-Amz-Target or query param
	target := r.Header.Get("X-Amz-Target")
	action := ""
	if target != "" {
		if idx := strings.LastIndex(target, "."); idx != -1 {
			action = target[idx+1:]
		}
	}
	if action == "" {
		if err := r.ParseForm(); err == nil {
			action = r.FormValue("Action")
		}
	}

	switch action {
	case "SendEmail":
		s.sendEmail(w, r)
	case "SendRawEmail":
		s.sendRawEmail(w, r)
	case "VerifyEmailIdentity", "VerifyEmailAddress":
		s.verifyEmailIdentity(w, r)
	case "ListIdentities":
		s.listIdentities(w, r)
	case "DeleteIdentity":
		s.deleteIdentity(w, r)
	case "GetSendQuota":
		s.getSendQuota(w, r)
	case "GetSendStatistics":
		s.getSendStatistics(w, r)
	default:
		s.xmlError(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("Operation %s is not supported.", action))
	}
}

// --- SES v1 Operations ---

// SendEmail — captures a structured email
func (s *Service) sendEmail(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameterValue", "Could not parse form")
		return
	}

	from := r.FormValue("Source")
	if from == "" {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameterValue", "Source (From) is required.")
		return
	}

	// Collect To/CC/BCC addresses (1-indexed in SES form encoding)
	to := collectAddresses(r, "Destination.ToAddresses.member")
	cc := collectAddresses(r, "Destination.CcAddresses.member")
	bcc := collectAddresses(r, "Destination.BccAddresses.member")
	replyTo := collectAddresses(r, "ReplyToAddresses.member")

	if len(to) == 0 {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameterValue",
			"At least one recipient is required.")
		return
	}

	subject := r.FormValue("Message.Subject.Data")
	textBody := r.FormValue("Message.Body.Text.Data")
	htmlBody := r.FormValue("Message.Body.Html.Data")

	msgID := uid.New() + "@nimbus.local"

	email := &CapturedEmail{
		MessageID: msgID,
		From:      from,
		To:        to,
		CC:        cc,
		BCC:       bcc,
		ReplyTo:   replyTo,
		Subject:   subject,
		Body:      emailBody{Text: textBody, HTML: htmlBody},
		SentAt:    time.Now().UTC(),
	}

	s.mu.Lock()
	s.messages = append(s.messages, email)
	s.mu.Unlock()

	type result struct {
		XMLName xml.Name `xml:"SendEmailResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			MessageID string `xml:"MessageId"`
		} `xml:"SendEmailResult"`
		Metadata struct {
			RequestID string `xml:"RequestId"`
		} `xml:"ResponseMetadata"`
	}

	var resp result
	resp.Xmlns = "http://ses.amazonaws.com/doc/2010-12-01/"
	resp.Result.MessageID = msgID
	resp.Metadata.RequestID = uid.New()
	xmlWrite(w, http.StatusOK, resp)
}

// SendRawEmail — captures a raw MIME email
func (s *Service) sendRawEmail(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameterValue", "Could not parse form")
		return
	}

	from := r.FormValue("Source")
	rawData := r.FormValue("RawMessage.Data")
	to := collectAddresses(r, "Destinations.member")

	msgID := uid.New() + "@nimbus.local"

	email := &CapturedEmail{
		MessageID: msgID,
		From:      from,
		To:        to,
		SentAt:    time.Now().UTC(),
		Raw:       rawData,
	}

	s.mu.Lock()
	s.messages = append(s.messages, email)
	s.mu.Unlock()

	type result struct {
		XMLName xml.Name `xml:"SendRawEmailResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			MessageID string `xml:"MessageId"`
		} `xml:"SendRawEmailResult"`
		Metadata struct {
			RequestID string `xml:"RequestId"`
		} `xml:"ResponseMetadata"`
	}

	var resp result
	resp.Xmlns = "http://ses.amazonaws.com/doc/2010-12-01/"
	resp.Result.MessageID = msgID
	resp.Metadata.RequestID = uid.New()
	xmlWrite(w, http.StatusOK, resp)
}

// VerifyEmailIdentity — marks an email/domain as verified
func (s *Service) verifyEmailIdentity(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameterValue", "Could not parse form")
		return
	}

	identity := r.FormValue("EmailAddress")
	if identity == "" {
		identity = r.FormValue("Identity")
	}

	s.mu.Lock()
	s.identities[identity] = true
	s.mu.Unlock()

	type result struct {
		XMLName  xml.Name `xml:"VerifyEmailIdentityResponse"`
		Xmlns    string   `xml:"xmlns,attr"`
		Result   struct{} `xml:"VerifyEmailIdentityResult"`
		Metadata struct {
			RequestID string `xml:"RequestId"`
		} `xml:"ResponseMetadata"`
	}

	var resp result
	resp.Xmlns = "http://ses.amazonaws.com/doc/2010-12-01/"
	resp.Metadata.RequestID = uid.New()
	xmlWrite(w, http.StatusOK, resp)
}

// ListIdentities — returns all verified identities
func (s *Service) listIdentities(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type member struct {
		Value string `xml:",chardata"`
	}
	type result struct {
		XMLName xml.Name `xml:"ListIdentitiesResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			Identities struct {
				Members []member `xml:"member"`
			} `xml:"Identities"`
		} `xml:"ListIdentitiesResult"`
		Metadata struct {
			RequestID string `xml:"RequestId"`
		} `xml:"ResponseMetadata"`
	}

	var resp result
	resp.Xmlns = "http://ses.amazonaws.com/doc/2010-12-01/"
	resp.Metadata.RequestID = uid.New()
	for identity := range s.identities {
		resp.Result.Identities.Members = append(
			resp.Result.Identities.Members, member{Value: identity})
	}

	xmlWrite(w, http.StatusOK, resp)
}

// DeleteIdentity — removes a verified identity
func (s *Service) deleteIdentity(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.xmlError(w, http.StatusBadRequest, "InvalidParameterValue", "Could not parse form")
		return
	}

	identity := r.FormValue("Identity")
	s.mu.Lock()
	delete(s.identities, identity)
	s.mu.Unlock()

	type result struct {
		XMLName  xml.Name `xml:"DeleteIdentityResponse"`
		Xmlns    string   `xml:"xmlns,attr"`
		Result   struct{} `xml:"DeleteIdentityResult"`
		Metadata struct {
			RequestID string `xml:"RequestId"`
		} `xml:"ResponseMetadata"`
	}

	var resp result
	resp.Xmlns = "http://ses.amazonaws.com/doc/2010-12-01/"
	resp.Metadata.RequestID = uid.New()
	xmlWrite(w, http.StatusOK, resp)
}

// GetSendQuota — returns dummy quota values. Many SDKs call this on startup.
func (s *Service) getSendQuota(w http.ResponseWriter, r *http.Request) {
	type result struct {
		XMLName xml.Name `xml:"GetSendQuotaResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			Max24HourSend   float64 `xml:"Max24HourSend"`
			MaxSendRate     float64 `xml:"MaxSendRate"`
			SentLast24Hours float64 `xml:"SentLast24Hours"`
		} `xml:"GetSendQuotaResult"`
		Metadata struct {
			RequestID string `xml:"RequestId"`
		} `xml:"ResponseMetadata"`
	}

	s.mu.RLock()
	sent := float64(len(s.messages))
	s.mu.RUnlock()

	var resp result
	resp.Xmlns = "http://ses.amazonaws.com/doc/2010-12-01/"
	resp.Result.Max24HourSend = 50000
	resp.Result.MaxSendRate = 14
	resp.Result.SentLast24Hours = sent
	resp.Metadata.RequestID = uid.New()
	xmlWrite(w, http.StatusOK, resp)
}

// GetSendStatistics — returns basic send stats
func (s *Service) getSendStatistics(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	total := len(s.messages)
	s.mu.RUnlock()

	type dataPoint struct {
		DeliveryAttempts int    `xml:"DeliveryAttempts"`
		Bounces          int    `xml:"Bounces"`
		Complaints       int    `xml:"Complaints"`
		Rejects          int    `xml:"Rejects"`
		Timestamp        string `xml:"Timestamp"`
	}
	type result struct {
		XMLName xml.Name `xml:"GetSendStatisticsResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Result  struct {
			SendDataPoints []dataPoint `xml:"SendDataPoints>member"`
		} `xml:"GetSendStatisticsResult"`
		Metadata struct {
			RequestID string `xml:"RequestId"`
		} `xml:"ResponseMetadata"`
	}

	var resp result
	resp.Xmlns = "http://ses.amazonaws.com/doc/2010-12-01/"
	resp.Metadata.RequestID = uid.New()
	if total > 0 {
		resp.Result.SendDataPoints = []dataPoint{{
			DeliveryAttempts: total,
			Timestamp:        time.Now().UTC().Format(time.RFC3339),
		}}
	}
	xmlWrite(w, http.StatusOK, resp)
}

// --- SES v2 ---

// sendEmailV2 handles POST /v2/email/outbound-emails (AWS SDK v2 style)
func (s *Service) sendEmailV2(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FromEmailAddress string `json:"FromEmailAddress"`
		Destination      struct {
			ToAddresses  []string `json:"ToAddresses"`
			CcAddresses  []string `json:"CcAddresses"`
			BccAddresses []string `json:"BccAddresses"`
		} `json:"Destination"`
		ReplyToAddresses []string `json:"ReplyToAddresses"`
		Content          struct {
			Simple *struct {
				Subject struct {
					Data string `json:"Data"`
				} `json:"Subject"`
				Body struct {
					Text *struct {
						Data string `json:"Data"`
					} `json:"Text"`
					Html *struct {
						Data string `json:"Data"`
					} `json:"Html"`
				} `json:"Body"`
			} `json:"Simple"`
			Raw *struct {
				Data []byte `json:"Data"`
			} `json:"Raw"`
		} `json:"Content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"__type":  "InvalidParameterValue",
			"message": "Could not parse request body.",
		})
		return
	}

	msgID := uid.New() + "@nimbus.local"

	email := &CapturedEmail{
		MessageID: msgID,
		From:      req.FromEmailAddress,
		To:        req.Destination.ToAddresses,
		CC:        req.Destination.CcAddresses,
		BCC:       req.Destination.BccAddresses,
		ReplyTo:   req.ReplyToAddresses,
		SentAt:    time.Now().UTC(),
	}

	if req.Content.Simple != nil {
		email.Subject = req.Content.Simple.Subject.Data
		if req.Content.Simple.Body.Text != nil {
			email.Body.Text = req.Content.Simple.Body.Text.Data
		}
		if req.Content.Simple.Body.Html != nil {
			email.Body.HTML = req.Content.Simple.Body.Html.Data
		}
	}

	s.mu.Lock()
	s.messages = append(s.messages, email)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"MessageId": msgID,
	})
}

// --- Nimbus inspection endpoint ---

// MessagesHandler serves captured emails at GET /_nimbus/ses/messages.
// This is not an AWS API — it's a Nimbus-specific endpoint for test inspection.
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

// ClearMessagesHandler clears all captured emails. Useful between tests.
// DELETE /_nimbus/ses/messages
func (s *Service) ClearMessagesHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.messages = nil
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// MessageCount returns how many emails have been captured.
func (s *Service) MessageCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages)
}

// --- Helpers ---

// collectAddresses extracts 1-indexed address lists from SES form encoding.
// e.g. Destination.ToAddresses.member.1, Destination.ToAddresses.member.2
func collectAddresses(r *http.Request, prefix string) []string {
	var addresses []string
	for i := 1; ; i++ {
		val := r.FormValue(fmt.Sprintf("%s.%d", prefix, i))
		if val == "" {
			break
		}
		addresses = append(addresses, val)
	}
	return addresses
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
		RequestID string `xml:"RequestId"`
	}
	var resp errResp
	resp.Error.Type = "Sender"
	resp.Error.Code = code
	resp.Error.Message = message
	resp.RequestID = uid.New()
	xmlWrite(w, status, resp)
}
