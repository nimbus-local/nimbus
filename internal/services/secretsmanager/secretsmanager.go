package secretsmanager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/uid"
)

// Service implements the AWS Secrets Manager emulator.
// All secrets are stored in-memory. State survives for the lifetime
// of the container. Accepts any credentials — this is a local dev tool.
type Service struct {
	mu      sync.RWMutex
	secrets map[string]*secret // keyed by secret name
	region  string
	account string
}

type secret struct {
	name        string
	arn         string
	description string
	value       *secretValue
	createdAt   time.Time
	updatedAt   time.Time
	deletedAt   *time.Time
	versionID   string
}

type secretValue struct {
	secretString *string
	secretBinary []byte
}

const (
	defaultAccount = "000000000000"
)

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		secrets: map[string]*secret{},
		region:  region,
		account: defaultAccount,
	}
}

func (s *Service) Name() string { return "secretsmanager" }

// Detect identifies Secrets Manager requests by X-Amz-Target header.
// All Secrets Manager operations use secretsmanager.<OperationName>
func (s *Service) Detect(r *http.Request) bool {
	target := r.Header.Get("X-Amz-Target")
	return strings.HasPrefix(target, "secretsmanager.")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	operation := ""
	if idx := strings.LastIndex(target, "."); idx != -1 {
		operation = target[idx+1:]
	}

	switch operation {
	case "CreateSecret":
		s.createSecret(w, r)
	case "GetSecretValue":
		s.getSecretValue(w, r)
	case "PutSecretValue":
		s.putSecretValue(w, r)
	case "UpdateSecret":
		s.updateSecret(w, r)
	case "DeleteSecret":
		s.deleteSecret(w, r)
	case "ListSecrets":
		s.listSecrets(w, r)
	case "DescribeSecret":
		s.describeSecret(w, r)
	case "RestoreSecret":
		s.restoreSecret(w, r)
	default:
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("Operation %s is not supported.", operation))
	}
}

// --- ARN helpers ---

func (s *Service) arn(name string) string {
	return fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:%s", s.region, s.account, name)
}

// --- Operations ---

// CreateSecret — creates a new secret, optionally with an initial value
func (s *Service) createSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string  `json:"Name"`
		Description  string  `json:"Description"`
		SecretString *string `json:"SecretString"`
		SecretBinary []byte  `json:"SecretBinary"`
	}
	if !decode(w, r, &req) {
		return
	}

	if req.Name == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidParameterException",
			"You must provide a name for the secret.")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.secrets[req.Name]; ok && existing.deletedAt == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceExistsException",
			fmt.Sprintf("A secret with the name %s already exists.", req.Name))
		return
	}

	versionID := uid.New()
	sec := &secret{
		name:        req.Name,
		arn:         s.arn(req.Name),
		description: req.Description,
		createdAt:   time.Now().UTC(),
		updatedAt:   time.Now().UTC(),
		versionID:   versionID,
	}

	if req.SecretString != nil || req.SecretBinary != nil {
		sec.value = &secretValue{
			secretString: req.SecretString,
			secretBinary: req.SecretBinary,
		}
	}

	s.secrets[req.Name] = sec

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"ARN":       sec.arn,
		"Name":      sec.name,
		"VersionId": versionID,
	})
}

// GetSecretValue — retrieves the current value of a secret
func (s *Service) getSecretValue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId string `json:"SecretId"`
	}
	if !decode(w, r, &req) {
		return
	}

	sec := s.findSecret(req.SecretId)
	if sec == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Secrets Manager can't find the specified secret: %s", req.SecretId))
		return
	}

	if sec.deletedAt != nil {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidRequestException",
			fmt.Sprintf("You can't perform this operation on secret %s because it was marked for deletion.", req.SecretId))
		return
	}

	if sec.value == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			"Secrets Manager can't find the specified secret value.")
		return
	}

	resp := map[string]interface{}{
		"ARN":         sec.arn,
		"Name":        sec.name,
		"VersionId":   sec.versionID,
		"CreatedDate": sec.createdAt.Unix(),
	}

	if sec.value.secretString != nil {
		resp["SecretString"] = *sec.value.secretString
	}
	if sec.value.secretBinary != nil {
		resp["SecretBinary"] = sec.value.secretBinary
	}

	jsonhttp.Write(w, http.StatusOK, resp)
}

// PutSecretValue — stores a new value for an existing secret
func (s *Service) putSecretValue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId     string  `json:"SecretId"`
		SecretString *string `json:"SecretString"`
		SecretBinary []byte  `json:"SecretBinary"`
	}
	if !decode(w, r, &req) {
		return
	}

	sec := s.findSecret(req.SecretId)
	if sec == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Secrets Manager can't find the specified secret: %s", req.SecretId))
		return
	}

	if sec.deletedAt != nil {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidRequestException",
			"You can't perform this operation on a secret that is scheduled for deletion.")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	versionID := uid.New()
	sec.value = &secretValue{
		secretString: req.SecretString,
		secretBinary: req.SecretBinary,
	}
	sec.versionID = versionID
	sec.updatedAt = time.Now().UTC()

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"ARN":       sec.arn,
		"Name":      sec.name,
		"VersionId": versionID,
	})
}

// UpdateSecret — updates metadata and optionally the value of a secret
func (s *Service) updateSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId     string  `json:"SecretId"`
		Description  string  `json:"Description"`
		SecretString *string `json:"SecretString"`
		SecretBinary []byte  `json:"SecretBinary"`
	}
	if !decode(w, r, &req) {
		return
	}

	sec := s.findSecret(req.SecretId)
	if sec == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Secrets Manager can't find the specified secret: %s", req.SecretId))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Description != "" {
		sec.description = req.Description
	}

	versionID := sec.versionID
	if req.SecretString != nil || req.SecretBinary != nil {
		versionID = uid.New()
		sec.value = &secretValue{
			secretString: req.SecretString,
			secretBinary: req.SecretBinary,
		}
		sec.versionID = versionID
	}

	sec.updatedAt = time.Now().UTC()

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"ARN":       sec.arn,
		"Name":      sec.name,
		"VersionId": versionID,
	})
}

// DeleteSecret — marks a secret for deletion
// Real AWS has a recovery window (default 30 days). We support
// ForceDeleteWithoutRecovery to delete immediately, matching SDK behavior.
func (s *Service) deleteSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId                   string `json:"SecretId"`
		ForceDeleteWithoutRecovery bool   `json:"ForceDeleteWithoutRecovery"`
		RecoveryWindowInDays       *int   `json:"RecoveryWindowInDays"`
	}
	if !decode(w, r, &req) {
		return
	}

	sec := s.findSecret(req.SecretId)
	if sec == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Secrets Manager can't find the specified secret: %s", req.SecretId))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()

	if req.ForceDeleteWithoutRecovery {
		delete(s.secrets, sec.name)
		jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
			"ARN":          sec.arn,
			"Name":         sec.name,
			"DeletionDate": now.Unix(),
		})
		return
	}

	// Default: schedule deletion 30 days out (or RecoveryWindowInDays)
	days := 30
	if req.RecoveryWindowInDays != nil {
		days = *req.RecoveryWindowInDays
	}
	deletionDate := now.Add(time.Duration(days) * 24 * time.Hour)
	sec.deletedAt = &deletionDate

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"ARN":          sec.arn,
		"Name":         sec.name,
		"DeletionDate": deletionDate.Unix(),
	})
}

// RestoreSecret — cancels a scheduled deletion
func (s *Service) restoreSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId string `json:"SecretId"`
	}
	if !decode(w, r, &req) {
		return
	}

	sec := s.findSecret(req.SecretId)
	if sec == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Secrets Manager can't find the specified secret: %s", req.SecretId))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sec.deletedAt = nil

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"ARN":  sec.arn,
		"Name": sec.name,
	})
}

// ListSecrets — returns all non-force-deleted secrets
func (s *Service) listSecrets(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type secretEntry struct {
		ARN             string `json:"ARN"`
		Name            string `json:"Name"`
		Description     string `json:"Description,omitempty"`
		DeletedDate     *int64 `json:"DeletedDate,omitempty"`
		LastChangedDate int64  `json:"LastChangedDate"`
	}

	var entries []secretEntry
	for _, sec := range s.secrets {
		entry := secretEntry{
			ARN:             sec.arn,
			Name:            sec.name,
			Description:     sec.description,
			LastChangedDate: sec.updatedAt.Unix(),
		}
		if sec.deletedAt != nil {
			unix := sec.deletedAt.Unix()
			entry.DeletedDate = &unix
		}
		entries = append(entries, entry)
	}

	jsonhttp.Write(w, http.StatusOK, map[string]interface{}{
		"SecretList": entries,
	})
}

// DescribeSecret — returns metadata about a secret (not the value)
func (s *Service) describeSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId string `json:"SecretId"`
	}
	if !decode(w, r, &req) {
		return
	}

	sec := s.findSecret(req.SecretId)
	if sec == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Secrets Manager can't find the specified secret: %s", req.SecretId))
		return
	}

	resp := map[string]interface{}{
		"ARN":             sec.arn,
		"Name":            sec.name,
		"Description":     sec.description,
		"CreatedDate":     sec.createdAt.Unix(),
		"LastChangedDate": sec.updatedAt.Unix(),
	}

	if sec.deletedAt != nil {
		resp["DeletedDate"] = sec.deletedAt.Unix()
	}

	jsonhttp.Write(w, http.StatusOK, resp)
}

// --- Helpers ---

// findSecret looks up a secret by name or ARN
func (s *Service) findSecret(nameOrARN string) *secret {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Direct name lookup
	if sec, ok := s.secrets[nameOrARN]; ok {
		return sec
	}

	// ARN lookup — extract name from arn:aws:secretsmanager:region:account:secret:name
	for _, sec := range s.secrets {
		if sec.arn == nameOrARN {
			return sec
		}
	}

	return nil
}

func decode(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidRequestException",
			fmt.Sprintf("Could not parse request body: %s", err.Error()))
		return false
	}
	return true
}
