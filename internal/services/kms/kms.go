package kms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const accountID = "000000000000"

// Service implements the AWS KMS emulator.
// Key material is generated locally using crypto/rand. Encrypt/Decrypt use
// real AES-256-GCM so ciphertexts produced by the emulator are genuinely
// decryptable — they are not forwarded to AWS.
type Service struct {
	mu      sync.RWMutex
	keys    map[string]*cmk    // keyID -> key
	aliases map[string]string  // aliasName -> keyID
	region  string
}

type cmk struct {
	id           string
	arn          string
	description  string
	state        string // Enabled, Disabled, PendingDeletion
	keyMaterial  []byte // 32 bytes — AES-256
	createdAt    time.Time
	deletionDate *time.Time
	tags         map[string]string
}

// ciphertextEnvelope is the structure stored inside a KMS CiphertextBlob.
// The whole envelope is JSON-marshalled then base64url-encoded.
type ciphertextEnvelope struct {
	KeyID string `json:"k"` // key that encrypted this blob
	Nonce []byte `json:"n"` // AES-GCM nonce (12 bytes)
	Data  []byte `json:"d"` // AES-GCM ciphertext
}

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region:  region,
		keys:    map[string]*cmk{},
		aliases: map[string]string{},
	}
}

func (s *Service) Name() string { return "kms" }

// Detect identifies KMS requests by X-Amz-Target header (TrentService.*).
func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), "TrentService.")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	action := ""
	if idx := strings.LastIndex(target, "."); idx != -1 {
		action = target[idx+1:]
	}

	switch action {
	case "CreateKey":
		s.createKey(w, r)
	case "DescribeKey":
		s.describeKey(w, r)
	case "ListKeys":
		s.listKeys(w, r)
	case "EnableKey":
		s.enableKey(w, r)
	case "DisableKey":
		s.disableKey(w, r)
	case "ScheduleKeyDeletion":
		s.scheduleKeyDeletion(w, r)
	case "CancelKeyDeletion":
		s.cancelKeyDeletion(w, r)
	case "CreateAlias":
		s.createAlias(w, r)
	case "DeleteAlias":
		s.deleteAlias(w, r)
	case "ListAliases":
		s.listAliases(w, r)
	case "UpdateAlias":
		s.updateAlias(w, r)
	case "TagResource":
		s.tagResource(w, r)
	case "UntagResource":
		s.untagResource(w, r)
	case "ListResourceTags":
		s.listResourceTags(w, r)
	case "Encrypt":
		s.encrypt(w, r)
	case "Decrypt":
		s.decrypt(w, r)
	case "GenerateDataKey":
		s.generateDataKey(w, r)
	case "GenerateDataKeyWithoutPlaintext":
		s.generateDataKeyWithoutPlaintext(w, r)
	case "ReEncrypt":
		s.reEncrypt(w, r)
	case "GenerateRandom":
		s.generateRandom(w, r)
	case "GetKeyPolicy":
		s.getKeyPolicy(w, r)
	case "PutKeyPolicy":
		s.putKeyPolicy(w, r)
	default:
		jsonError(w, http.StatusBadRequest, "UnsupportedOperationException",
			fmt.Sprintf("Operation %s is not supported.", action))
	}
}

// --- Key management ---

func (s *Service) keyARN(id string) string {
	return fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", s.region, accountID, id)
}

func (s *Service) aliasARN(name string) string {
	return fmt.Sprintf("arn:aws:kms:%s:%s:%s", s.region, accountID, name)
}

func (s *Service) createKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Description string            `json:"Description"`
		Tags        []map[string]string `json:"Tags"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	keyMaterial := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, keyMaterial); err != nil {
		jsonError(w, http.StatusInternalServerError, "KMSInternalException", "failed to generate key material")
		return
	}

	id := uid.New()
	arn := s.keyARN(id)
	tags := map[string]string{}
	for _, t := range req.Tags {
		if k, v := t["TagKey"], t["TagValue"]; k != "" {
			tags[k] = v
		}
	}

	key := &cmk{
		id:          id,
		arn:         arn,
		description: req.Description,
		state:       "Enabled",
		keyMaterial: keyMaterial,
		createdAt:   time.Now().UTC(),
		tags:        tags,
	}

	s.mu.Lock()
	s.keys[id] = key
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"KeyMetadata": keyMetadata(key)})
}

func (s *Service) describeKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID string `json:"KeyId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	key, err := s.resolveKey(req.KeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}

	s.mu.RLock()
	meta := keyMetadata(key)
	s.mu.RUnlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"KeyMetadata": meta})
}

func (s *Service) listKeys(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type entry struct {
		KeyID  string `json:"KeyId"`
		KeyArn string `json:"KeyArn"`
	}
	keys := []entry{}
	for _, k := range s.keys {
		keys = append(keys, entry{KeyID: k.id, KeyArn: k.arn})
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"Keys": keys, "Truncated": false})
}

func (s *Service) enableKey(w http.ResponseWriter, r *http.Request) {
	s.setKeyState(w, r, "Enabled")
}

func (s *Service) disableKey(w http.ResponseWriter, r *http.Request) {
	s.setKeyState(w, r, "Disabled")
}

func (s *Service) setKeyState(w http.ResponseWriter, r *http.Request, state string) {
	var req struct {
		KeyID string `json:"KeyId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	key, err := s.resolveKey(req.KeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}

	s.mu.Lock()
	key.state = state
	key.deletionDate = nil
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) scheduleKeyDeletion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID               string `json:"KeyId"`
		PendingWindowInDays int    `json:"PendingWindowInDays"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	key, err := s.resolveKey(req.KeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}

	days := req.PendingWindowInDays
	if days < 7 {
		days = 30
	}
	deletionDate := time.Now().UTC().AddDate(0, 0, days)

	s.mu.Lock()
	key.state = "PendingDeletion"
	key.deletionDate = &deletionDate
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"KeyId":        key.id,
		"DeletionDate": deletionDate.Unix(),
	})
}

func (s *Service) cancelKeyDeletion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID string `json:"KeyId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	key, err := s.resolveKey(req.KeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}

	s.mu.Lock()
	key.state = "Disabled"
	key.deletionDate = nil
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"KeyId": key.id})
}

// --- Aliases ---

func (s *Service) createAlias(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AliasName   string `json:"AliasName"`
		TargetKeyID string `json:"TargetKeyId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if !strings.HasPrefix(req.AliasName, "alias/") {
		jsonError(w, http.StatusBadRequest, "InvalidAliasNameException",
			"Alias name must start with alias/")
		return
	}

	key, err := s.resolveKey(req.TargetKeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}

	s.mu.Lock()
	s.aliases[req.AliasName] = key.id
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) deleteAlias(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AliasName string `json:"AliasName"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	delete(s.aliases, req.AliasName)
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) listAliases(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID string `json:"KeyId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	type aliasEntry struct {
		AliasName   string `json:"AliasName"`
		AliasArn    string `json:"AliasArn"`
		TargetKeyId string `json:"TargetKeyId"`
	}
	aliases := []aliasEntry{}
	for name, keyID := range s.aliases {
		if req.KeyID != "" && keyID != req.KeyID {
			continue
		}
		aliases = append(aliases, aliasEntry{
			AliasName:   name,
			AliasArn:    s.aliasARN(name),
			TargetKeyId: keyID,
		})
	}
	jsonWrite(w, http.StatusOK, map[string]interface{}{"Aliases": aliases, "Truncated": false})
}

func (s *Service) updateAlias(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AliasName   string `json:"AliasName"`
		TargetKeyID string `json:"TargetKeyId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	key, err := s.resolveKey(req.TargetKeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}

	s.mu.Lock()
	s.aliases[req.AliasName] = key.id
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

// --- Tags ---

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID string              `json:"KeyId"`
		Tags  []map[string]string `json:"Tags"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	key, err := s.resolveKey(req.KeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}

	s.mu.Lock()
	for _, t := range req.Tags {
		key.tags[t["TagKey"]] = t["TagValue"]
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID   string   `json:"KeyId"`
		TagKeys []string `json:"TagKeys"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	key, err := s.resolveKey(req.KeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}

	s.mu.Lock()
	for _, k := range req.TagKeys {
		delete(key.tags, k)
	}
	s.mu.Unlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) listResourceTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID string `json:"KeyId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	key, err := s.resolveKey(req.KeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}

	s.mu.RLock()
	type tag struct {
		TagKey   string `json:"TagKey"`
		TagValue string `json:"TagValue"`
	}
	tags := []tag{}
	for k, v := range key.tags {
		tags = append(tags, tag{TagKey: k, TagValue: v})
	}
	s.mu.RUnlock()

	jsonWrite(w, http.StatusOK, map[string]interface{}{"Tags": tags, "Truncated": false})
}

// --- Cryptographic operations ---

func (s *Service) encrypt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID     string `json:"KeyId"`
		Plaintext []byte `json:"Plaintext"` // SDK sends as base64; json.Unmarshal handles []byte
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Plaintext) == 0 {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "Plaintext is required")
		return
	}

	key, err := s.resolveKey(req.KeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}
	if err := s.checkKeyUsable(key); err != nil {
		jsonError(w, http.StatusBadRequest, "DisabledException", err.Error())
		return
	}

	ciphertext, err := aesGCMEncrypt(key.keyMaterial, key.id, req.Plaintext)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "KMSInternalException", "encryption failed")
		return
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"CiphertextBlob":      ciphertext,
		"KeyId":               key.arn,
		"EncryptionAlgorithm": "SYMMETRIC_DEFAULT",
	})
}

func (s *Service) decrypt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CiphertextBlob []byte `json:"CiphertextBlob"`
		KeyID          string `json:"KeyId"` // optional hint
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.CiphertextBlob) == 0 {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "CiphertextBlob is required")
		return
	}

	env, err := decodeCiphertext(req.CiphertextBlob)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "InvalidCiphertextException", "invalid ciphertext blob")
		return
	}

	s.mu.RLock()
	key, ok := s.keys[env.KeyID]
	s.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusBadRequest, "NotFoundException",
			fmt.Sprintf("key %s not found", env.KeyID))
		return
	}
	if err := s.checkKeyUsable(key); err != nil {
		jsonError(w, http.StatusBadRequest, "DisabledException", err.Error())
		return
	}

	plaintext, err := aesGCMDecrypt(key.keyMaterial, key.id, env)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "InvalidCiphertextException", "decryption failed")
		return
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"Plaintext":           plaintext,
		"KeyId":               key.arn,
		"EncryptionAlgorithm": "SYMMETRIC_DEFAULT",
	})
}

func (s *Service) generateDataKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID         string `json:"KeyId"`
		KeySpec       string `json:"KeySpec"`       // AES_256 or AES_128
		NumberOfBytes int    `json:"NumberOfBytes"` // alternative to KeySpec
	}
	json.NewDecoder(r.Body).Decode(&req)

	key, err := s.resolveKey(req.KeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}
	if err := s.checkKeyUsable(key); err != nil {
		jsonError(w, http.StatusBadRequest, "DisabledException", err.Error())
		return
	}

	dataKeyLen := dataKeySize(req.KeySpec, req.NumberOfBytes)
	dataKey := make([]byte, dataKeyLen)
	if _, err := io.ReadFull(rand.Reader, dataKey); err != nil {
		jsonError(w, http.StatusInternalServerError, "KMSInternalException", "failed to generate data key")
		return
	}

	encrypted, err := aesGCMEncrypt(key.keyMaterial, key.id, dataKey)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "KMSInternalException", "encryption failed")
		return
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"Plaintext":      dataKey,
		"CiphertextBlob": encrypted,
		"KeyId":          key.arn,
	})
}

func (s *Service) generateDataKeyWithoutPlaintext(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID         string `json:"KeyId"`
		KeySpec       string `json:"KeySpec"`
		NumberOfBytes int    `json:"NumberOfBytes"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	key, err := s.resolveKey(req.KeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}
	if err := s.checkKeyUsable(key); err != nil {
		jsonError(w, http.StatusBadRequest, "DisabledException", err.Error())
		return
	}

	dataKeyLen := dataKeySize(req.KeySpec, req.NumberOfBytes)
	dataKey := make([]byte, dataKeyLen)
	if _, err := io.ReadFull(rand.Reader, dataKey); err != nil {
		jsonError(w, http.StatusInternalServerError, "KMSInternalException", "failed to generate data key")
		return
	}

	encrypted, err := aesGCMEncrypt(key.keyMaterial, key.id, dataKey)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "KMSInternalException", "encryption failed")
		return
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"CiphertextBlob": encrypted,
		"KeyId":          key.arn,
	})
}

func (s *Service) reEncrypt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CiphertextBlob   []byte `json:"CiphertextBlob"`
		DestinationKeyID string `json:"DestinationKeyId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.CiphertextBlob) == 0 {
		jsonError(w, http.StatusBadRequest, "InvalidParameterException", "CiphertextBlob is required")
		return
	}

	env, err := decodeCiphertext(req.CiphertextBlob)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "InvalidCiphertextException", "invalid ciphertext blob")
		return
	}

	s.mu.RLock()
	srcKey, ok := s.keys[env.KeyID]
	s.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusBadRequest, "NotFoundException", "source key not found")
		return
	}

	plaintext, err := aesGCMDecrypt(srcKey.keyMaterial, srcKey.id, env)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "InvalidCiphertextException", "decryption failed")
		return
	}

	dstKey, err := s.resolveKey(req.DestinationKeyID)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}
	if err := s.checkKeyUsable(dstKey); err != nil {
		jsonError(w, http.StatusBadRequest, "DisabledException", err.Error())
		return
	}

	newCiphertext, err := aesGCMEncrypt(dstKey.keyMaterial, dstKey.id, plaintext)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "KMSInternalException", "re-encryption failed")
		return
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{
		"CiphertextBlob":             newCiphertext,
		"SourceKeyId":                srcKey.arn,
		"KeyId":                      dstKey.arn,
		"SourceEncryptionAlgorithm":  "SYMMETRIC_DEFAULT",
		"DestinationEncryptionAlgorithm": "SYMMETRIC_DEFAULT",
	})
}

func (s *Service) generateRandom(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NumberOfBytes int `json:"NumberOfBytes"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	n := req.NumberOfBytes
	if n <= 0 || n > 1024 {
		n = 32
	}

	b := make([]byte, n)
	io.ReadFull(rand.Reader, b)

	jsonWrite(w, http.StatusOK, map[string]interface{}{"Plaintext": b})
}

// --- Key policy (stub — no real IAM enforcement) ---

func (s *Service) getKeyPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID      string `json:"KeyId"`
		PolicyName string `json:"PolicyName"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if _, err := s.resolveKey(req.KeyID); err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}

	defaultPolicy := `{"Version":"2012-10-17","Statement":[{"Sid":"Enable IAM User Permissions","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"kms:*","Resource":"*"}]}`
	jsonWrite(w, http.StatusOK, map[string]interface{}{"Policy": defaultPolicy})
}

func (s *Service) putKeyPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID string `json:"KeyId"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if _, err := s.resolveKey(req.KeyID); err != nil {
		jsonError(w, http.StatusBadRequest, "NotFoundException", err.Error())
		return
	}

	jsonWrite(w, http.StatusOK, map[string]interface{}{})
}

// --- Helpers ---

// resolveKey finds a key by ID, ARN, or alias name.
func (s *Service) resolveKey(keyID string) (*cmk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Direct ID
	if k, ok := s.keys[keyID]; ok {
		return k, nil
	}

	// ARN: arn:aws:kms:region:account:key/id
	if strings.HasPrefix(keyID, "arn:") {
		parts := strings.Split(keyID, "/")
		if len(parts) >= 2 {
			if k, ok := s.keys[parts[len(parts)-1]]; ok {
				return k, nil
			}
		}
		return nil, fmt.Errorf("key %s not found", keyID)
	}

	// Alias
	if strings.HasPrefix(keyID, "alias/") {
		if id, ok := s.aliases[keyID]; ok {
			if k, ok := s.keys[id]; ok {
				return k, nil
			}
		}
		return nil, fmt.Errorf("alias %s not found", keyID)
	}

	return nil, fmt.Errorf("key %s not found", keyID)
}

func (s *Service) checkKeyUsable(k *cmk) error {
	if k.state == "Disabled" {
		return fmt.Errorf("key %s is disabled", k.id)
	}
	if k.state == "PendingDeletion" {
		return fmt.Errorf("key %s is pending deletion", k.id)
	}
	return nil
}

func keyMetadata(k *cmk) map[string]interface{} {
	m := map[string]interface{}{
		"KeyId":                k.id,
		"Arn":                  k.arn,
		"Description":          k.description,
		"KeyState":             k.state,
		"Enabled":              k.state == "Enabled",
		"CreationDate":         k.createdAt.Unix(),
		"KeyUsage":             "ENCRYPT_DECRYPT",
		"KeySpec":              "SYMMETRIC_DEFAULT",
		"EncryptionAlgorithms": []string{"SYMMETRIC_DEFAULT"},
		"MultiRegion":          false,
	}
	if k.deletionDate != nil {
		m["DeletionDate"] = k.deletionDate.Unix()
	}
	return m
}

func dataKeySize(keySpec string, numberOfBytes int) int {
	if keySpec == "AES_128" {
		return 16
	}
	if numberOfBytes > 0 {
		return numberOfBytes
	}
	return 32 // AES_256 default
}

// aesGCMEncrypt encrypts plaintext with the key material using AES-256-GCM.
// The key ID is used as additional authenticated data.
func aesGCMEncrypt(keyMaterial []byte, keyID string, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(keyMaterial)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(keyID))

	env := ciphertextEnvelope{KeyID: keyID, Nonce: nonce, Data: ciphertext}
	envJSON, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}

	blob := make([]byte, base64.StdEncoding.EncodedLen(len(envJSON)))
	base64.StdEncoding.Encode(blob, envJSON)
	return blob, nil
}

// decodeCiphertext extracts the envelope from a CiphertextBlob.
// The blob is base64-decoded JSON (as produced by aesGCMEncrypt).
func decodeCiphertext(blob []byte) (*ciphertextEnvelope, error) {
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(blob)))
	n, err := base64.StdEncoding.Decode(decoded, blob)
	if err != nil {
		return nil, err
	}
	var env ciphertextEnvelope
	if err := json.Unmarshal(decoded[:n], &env); err != nil {
		return nil, err
	}
	return &env, nil
}

func aesGCMDecrypt(keyMaterial []byte, keyID string, env *ciphertextEnvelope) ([]byte, error) {
	block, err := aes.NewCipher(keyMaterial)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, env.Nonce, env.Data, []byte(keyID))
}

func jsonWrite(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"__type":  code,
		"message": message,
	})
}
