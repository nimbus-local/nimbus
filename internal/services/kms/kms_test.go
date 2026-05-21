package kms

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestService() *Service {
	return New("us-east-1")
}

func kmsRequest(t *testing.T, svc *Service, action string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("X-Amz-Target", "TrentService."+action)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("failed to decode JSON: %v\nbody: %s", err, w.Body.String())
	}
	return out
}

func mustCreateKey(t *testing.T, svc *Service, desc string) string {
	t.Helper()
	w := kmsRequest(t, svc, "CreateKey", map[string]string{"Description": desc})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateKey: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	resp := decodeJSON(t, w)
	meta := resp["KeyMetadata"].(map[string]interface{})
	return meta["KeyId"].(string)
}

// --- Detect ---

func TestDetect(t *testing.T) {
	svc := newTestService()
	cases := []struct {
		target   string
		expected bool
	}{
		{"TrentService.CreateKey", true},
		{"TrentService.Encrypt", true},
		{"AmazonSQS.SendMessage", false},
		{"AmazonEventBridge.PutEvents", false},
		{"", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("X-Amz-Target", tc.target)
		if got := svc.Detect(req); got != tc.expected {
			t.Errorf("Detect(%q): expected %v, got %v", tc.target, tc.expected, got)
		}
	}
}

// --- CreateKey ---

func TestCreateKey(t *testing.T) {
	svc := newTestService()

	w := kmsRequest(t, svc, "CreateKey", map[string]string{"Description": "my key"})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateKey: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	resp := decodeJSON(t, w)
	meta := resp["KeyMetadata"].(map[string]interface{})
	if meta["KeyId"] == "" {
		t.Error("expected non-empty KeyId")
	}
	if meta["KeyState"] != "Enabled" {
		t.Errorf("expected KeyState=Enabled, got %v", meta["KeyState"])
	}
	if meta["Description"] != "my key" {
		t.Errorf("expected Description='my key', got %v", meta["Description"])
	}
	if !strings.Contains(meta["Arn"].(string), "arn:aws:kms") {
		t.Errorf("expected ARN, got %v", meta["Arn"])
	}
}

func TestCreateKey_WithTags(t *testing.T) {
	svc := newTestService()

	keyID := mustCreateKey(t, svc, "tagged key")
	kmsRequest(t, svc, "TagResource", map[string]interface{}{
		"KeyId": keyID,
		"Tags":  []map[string]string{{"TagKey": "env", "TagValue": "test"}},
	})

	w := kmsRequest(t, svc, "ListResourceTags", map[string]string{"KeyId": keyID})
	resp := decodeJSON(t, w)
	tags := resp["Tags"].([]interface{})
	if len(tags) != 1 {
		t.Errorf("expected 1 tag, got %d", len(tags))
	}
}

// --- DescribeKey ---

func TestDescribeKey(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "test key")

	w := kmsRequest(t, svc, "DescribeKey", map[string]string{"KeyId": keyID})
	if w.Code != http.StatusOK {
		t.Fatalf("DescribeKey: expected 200, got %d", w.Code)
	}
	resp := decodeJSON(t, w)
	meta := resp["KeyMetadata"].(map[string]interface{})
	if meta["KeyId"] != keyID {
		t.Errorf("expected KeyId=%s, got %v", keyID, meta["KeyId"])
	}
}

func TestDescribeKey_ByARN(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	wDesc := kmsRequest(t, svc, "DescribeKey", map[string]string{"KeyId": keyID})
	arn := decodeJSON(t, wDesc)["KeyMetadata"].(map[string]interface{})["Arn"].(string)

	w := kmsRequest(t, svc, "DescribeKey", map[string]string{"KeyId": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("DescribeKey by ARN: expected 200, got %d", w.Code)
	}
}

func TestDescribeKey_ByAlias(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")
	kmsRequest(t, svc, "CreateAlias", map[string]string{
		"AliasName":   "alias/my-key",
		"TargetKeyId": keyID,
	})

	w := kmsRequest(t, svc, "DescribeKey", map[string]string{"KeyId": "alias/my-key"})
	if w.Code != http.StatusOK {
		t.Fatalf("DescribeKey by alias: expected 200, got %d", w.Code)
	}
}

func TestDescribeKey_NotFound(t *testing.T) {
	svc := newTestService()

	w := kmsRequest(t, svc, "DescribeKey", map[string]string{"KeyId": "no-such-key"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing key, got %d", w.Code)
	}
}

// --- ListKeys ---

func TestListKeys(t *testing.T) {
	svc := newTestService()

	mustCreateKey(t, svc, "key-a")
	mustCreateKey(t, svc, "key-b")
	mustCreateKey(t, svc, "key-c")

	w := kmsRequest(t, svc, "ListKeys", map[string]string{})
	if w.Code != http.StatusOK {
		t.Fatalf("ListKeys: expected 200, got %d", w.Code)
	}
	resp := decodeJSON(t, w)
	keys := resp["Keys"].([]interface{})
	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}
}

// --- Enable / Disable ---

func TestEnableDisableKey(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	kmsRequest(t, svc, "DisableKey", map[string]string{"KeyId": keyID})
	w := kmsRequest(t, svc, "DescribeKey", map[string]string{"KeyId": keyID})
	meta := decodeJSON(t, w)["KeyMetadata"].(map[string]interface{})
	if meta["KeyState"] != "Disabled" {
		t.Errorf("expected Disabled after DisableKey, got %v", meta["KeyState"])
	}

	kmsRequest(t, svc, "EnableKey", map[string]string{"KeyId": keyID})
	w = kmsRequest(t, svc, "DescribeKey", map[string]string{"KeyId": keyID})
	meta = decodeJSON(t, w)["KeyMetadata"].(map[string]interface{})
	if meta["KeyState"] != "Enabled" {
		t.Errorf("expected Enabled after EnableKey, got %v", meta["KeyState"])
	}
}

// --- ScheduleKeyDeletion / CancelKeyDeletion ---

func TestScheduleKeyDeletion(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	w := kmsRequest(t, svc, "ScheduleKeyDeletion", map[string]interface{}{
		"KeyId":               keyID,
		"PendingWindowInDays": 7,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("ScheduleKeyDeletion: expected 200, got %d", w.Code)
	}

	resp := decodeJSON(t, w)
	if resp["DeletionDate"] == nil {
		t.Error("expected DeletionDate in response")
	}

	dw := kmsRequest(t, svc, "DescribeKey", map[string]string{"KeyId": keyID})
	meta := decodeJSON(t, dw)["KeyMetadata"].(map[string]interface{})
	if meta["KeyState"] != "PendingDeletion" {
		t.Errorf("expected PendingDeletion state, got %v", meta["KeyState"])
	}
}

func TestCancelKeyDeletion(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	kmsRequest(t, svc, "ScheduleKeyDeletion", map[string]interface{}{"KeyId": keyID, "PendingWindowInDays": 7})
	kmsRequest(t, svc, "CancelKeyDeletion", map[string]string{"KeyId": keyID})

	w := kmsRequest(t, svc, "DescribeKey", map[string]string{"KeyId": keyID})
	meta := decodeJSON(t, w)["KeyMetadata"].(map[string]interface{})
	if meta["KeyState"] != "Disabled" {
		t.Errorf("expected Disabled after CancelKeyDeletion, got %v", meta["KeyState"])
	}
}

// --- Aliases ---

func TestCreateAlias(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	w := kmsRequest(t, svc, "CreateAlias", map[string]string{
		"AliasName":   "alias/my-key",
		"TargetKeyId": keyID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateAlias: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestCreateAlias_InvalidName(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	w := kmsRequest(t, svc, "CreateAlias", map[string]string{
		"AliasName":   "my-key", // missing alias/ prefix
		"TargetKeyId": keyID,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad alias name, got %d", w.Code)
	}
}

func TestListAliases(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	kmsRequest(t, svc, "CreateAlias", map[string]string{"AliasName": "alias/alpha", "TargetKeyId": keyID})
	kmsRequest(t, svc, "CreateAlias", map[string]string{"AliasName": "alias/beta", "TargetKeyId": keyID})

	w := kmsRequest(t, svc, "ListAliases", map[string]string{})
	resp := decodeJSON(t, w)
	aliases := resp["Aliases"].([]interface{})
	if len(aliases) != 2 {
		t.Errorf("expected 2 aliases, got %d", len(aliases))
	}
}

func TestDeleteAlias(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")
	kmsRequest(t, svc, "CreateAlias", map[string]string{"AliasName": "alias/temp", "TargetKeyId": keyID})

	kmsRequest(t, svc, "DeleteAlias", map[string]string{"AliasName": "alias/temp"})

	w := kmsRequest(t, svc, "ListAliases", map[string]string{})
	resp := decodeJSON(t, w)
	aliases := resp["Aliases"].([]interface{})
	if len(aliases) != 0 {
		t.Errorf("expected 0 aliases after delete, got %d", len(aliases))
	}
}

func TestUpdateAlias(t *testing.T) {
	svc := newTestService()
	keyID1 := mustCreateKey(t, svc, "key1")
	keyID2 := mustCreateKey(t, svc, "key2")

	kmsRequest(t, svc, "CreateAlias", map[string]string{"AliasName": "alias/shared", "TargetKeyId": keyID1})
	kmsRequest(t, svc, "UpdateAlias", map[string]string{"AliasName": "alias/shared", "TargetKeyId": keyID2})

	w := kmsRequest(t, svc, "ListAliases", map[string]string{"KeyId": keyID2})
	resp := decodeJSON(t, w)
	aliases := resp["Aliases"].([]interface{})
	if len(aliases) != 1 {
		t.Errorf("expected alias to point to key2, got %d aliases", len(aliases))
	}
}

// --- Encrypt / Decrypt ---

func TestEncryptDecrypt(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	plaintext := []byte("hello from nimbus")

	ew := kmsRequest(t, svc, "Encrypt", map[string]interface{}{
		"KeyId":     keyID,
		"Plaintext": plaintext,
	})
	if ew.Code != http.StatusOK {
		t.Fatalf("Encrypt: expected 200, got %d\n%s", ew.Code, ew.Body.String())
	}

	eResp := decodeJSON(t, ew)
	ciphertext := eResp["CiphertextBlob"]
	if ciphertext == nil {
		t.Fatal("expected CiphertextBlob in Encrypt response")
	}

	dw := kmsRequest(t, svc, "Decrypt", map[string]interface{}{
		"CiphertextBlob": ciphertext,
	})
	if dw.Code != http.StatusOK {
		t.Fatalf("Decrypt: expected 200, got %d\n%s", dw.Code, dw.Body.String())
	}

	dResp := decodeJSON(t, dw)
	// Plaintext comes back as base64 in JSON []byte fields
	gotB64, ok := dResp["Plaintext"].(string)
	if !ok {
		t.Fatalf("expected Plaintext string in Decrypt response, got %T", dResp["Plaintext"])
	}
	if gotB64 == "" {
		t.Error("expected non-empty Plaintext")
	}
	decoded, err := base64.StdEncoding.DecodeString(gotB64)
	if err != nil {
		t.Fatalf("failed to base64-decode plaintext: %v", err)
	}
	if string(decoded) != string(plaintext) {
		t.Errorf("decrypt round-trip failed: got %q, want %q", decoded, plaintext)
	}
}

func TestEncrypt_ByAlias(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")
	kmsRequest(t, svc, "CreateAlias", map[string]string{"AliasName": "alias/my-key", "TargetKeyId": keyID})

	w := kmsRequest(t, svc, "Encrypt", map[string]interface{}{
		"KeyId":     "alias/my-key",
		"Plaintext": []byte("test"),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("Encrypt by alias: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestEncrypt_DisabledKey(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")
	kmsRequest(t, svc, "DisableKey", map[string]string{"KeyId": keyID})

	w := kmsRequest(t, svc, "Encrypt", map[string]interface{}{
		"KeyId":     keyID,
		"Plaintext": []byte("test"),
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for disabled key, got %d", w.Code)
	}
}

func TestDecrypt_WrongCiphertext(t *testing.T) {
	svc := newTestService()

	w := kmsRequest(t, svc, "Decrypt", map[string]interface{}{
		"CiphertextBlob": []byte("not-valid-ciphertext"),
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid ciphertext, got %d", w.Code)
	}
}

func TestEncryptDecrypt_UniqueEachTime(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")
	plaintext := []byte("same plaintext")

	var blobs []interface{}
	for i := 0; i < 3; i++ {
		ew := kmsRequest(t, svc, "Encrypt", map[string]interface{}{
			"KeyId":     keyID,
			"Plaintext": plaintext,
		})
		resp := decodeJSON(t, ew)
		blobs = append(blobs, resp["CiphertextBlob"])
	}
	// Each ciphertext should be unique (different nonces)
	if blobs[0] == blobs[1] || blobs[1] == blobs[2] {
		t.Error("expected unique ciphertexts per encryption (different nonces)")
	}
}

// --- GenerateDataKey ---

func TestGenerateDataKey(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	w := kmsRequest(t, svc, "GenerateDataKey", map[string]interface{}{
		"KeyId":   keyID,
		"KeySpec": "AES_256",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("GenerateDataKey: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	resp := decodeJSON(t, w)
	if resp["Plaintext"] == nil {
		t.Error("expected Plaintext in GenerateDataKey response")
	}
	if resp["CiphertextBlob"] == nil {
		t.Error("expected CiphertextBlob in GenerateDataKey response")
	}
	if resp["KeyId"] == nil {
		t.Error("expected KeyId in GenerateDataKey response")
	}
}

func TestGenerateDataKey_RoundTrip(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	gw := kmsRequest(t, svc, "GenerateDataKey", map[string]interface{}{
		"KeyId":   keyID,
		"KeySpec": "AES_256",
	})
	gResp := decodeJSON(t, gw)

	// Decrypt the encrypted data key and verify it matches the plaintext
	dw := kmsRequest(t, svc, "Decrypt", map[string]interface{}{
		"CiphertextBlob": gResp["CiphertextBlob"],
	})
	if dw.Code != http.StatusOK {
		t.Fatalf("Decrypt of data key: expected 200, got %d\n%s", dw.Code, dw.Body.String())
	}
	dResp := decodeJSON(t, dw)
	if dResp["Plaintext"] != gResp["Plaintext"] {
		t.Error("decrypted data key does not match original plaintext")
	}
}

func TestGenerateDataKeyWithoutPlaintext(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	w := kmsRequest(t, svc, "GenerateDataKeyWithoutPlaintext", map[string]interface{}{
		"KeyId":   keyID,
		"KeySpec": "AES_256",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("GenerateDataKeyWithoutPlaintext: expected 200, got %d", w.Code)
	}

	resp := decodeJSON(t, w)
	if resp["Plaintext"] != nil {
		t.Error("expected no Plaintext in GenerateDataKeyWithoutPlaintext response")
	}
	if resp["CiphertextBlob"] == nil {
		t.Error("expected CiphertextBlob")
	}
}

// --- ReEncrypt ---

func TestReEncrypt(t *testing.T) {
	svc := newTestService()
	srcKeyID := mustCreateKey(t, svc, "source")
	dstKeyID := mustCreateKey(t, svc, "destination")

	// Encrypt with source key
	ew := kmsRequest(t, svc, "Encrypt", map[string]interface{}{
		"KeyId":     srcKeyID,
		"Plaintext": []byte("secret data"),
	})
	ciphertext := decodeJSON(t, ew)["CiphertextBlob"]

	// ReEncrypt to destination key
	rw := kmsRequest(t, svc, "ReEncrypt", map[string]interface{}{
		"CiphertextBlob":   ciphertext,
		"DestinationKeyId": dstKeyID,
	})
	if rw.Code != http.StatusOK {
		t.Fatalf("ReEncrypt: expected 200, got %d\n%s", rw.Code, rw.Body.String())
	}

	rResp := decodeJSON(t, rw)
	if rResp["CiphertextBlob"] == nil {
		t.Error("expected CiphertextBlob in ReEncrypt response")
	}

	// Decrypt with destination key
	dw := kmsRequest(t, svc, "Decrypt", map[string]interface{}{
		"CiphertextBlob": rResp["CiphertextBlob"],
	})
	if dw.Code != http.StatusOK {
		t.Fatalf("Decrypt after ReEncrypt: expected 200, got %d\n%s", dw.Code, dw.Body.String())
	}
}

// --- GenerateRandom ---

func TestGenerateRandom(t *testing.T) {
	svc := newTestService()

	w := kmsRequest(t, svc, "GenerateRandom", map[string]int{"NumberOfBytes": 64})
	if w.Code != http.StatusOK {
		t.Fatalf("GenerateRandom: expected 200, got %d", w.Code)
	}
	resp := decodeJSON(t, w)
	if resp["Plaintext"] == nil {
		t.Error("expected Plaintext in GenerateRandom response")
	}
}

// --- Tags ---

func TestTagUntagListTags(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	kmsRequest(t, svc, "TagResource", map[string]interface{}{
		"KeyId": keyID,
		"Tags": []map[string]string{
			{"TagKey": "env", "TagValue": "prod"},
			{"TagKey": "team", "TagValue": "platform"},
		},
	})

	w := kmsRequest(t, svc, "ListResourceTags", map[string]string{"KeyId": keyID})
	resp := decodeJSON(t, w)
	tags := resp["Tags"].([]interface{})
	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(tags))
	}

	kmsRequest(t, svc, "UntagResource", map[string]interface{}{
		"KeyId":   keyID,
		"TagKeys": []string{"env"},
	})

	w2 := kmsRequest(t, svc, "ListResourceTags", map[string]string{"KeyId": keyID})
	resp2 := decodeJSON(t, w2)
	tags2 := resp2["Tags"].([]interface{})
	if len(tags2) != 1 {
		t.Errorf("expected 1 tag after untag, got %d", len(tags2))
	}
}

// --- GetKeyPolicy / PutKeyPolicy ---

func TestGetPutKeyPolicy(t *testing.T) {
	svc := newTestService()
	keyID := mustCreateKey(t, svc, "")

	w := kmsRequest(t, svc, "GetKeyPolicy", map[string]string{
		"KeyId":      keyID,
		"PolicyName": "default",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("GetKeyPolicy: expected 200, got %d", w.Code)
	}
	resp := decodeJSON(t, w)
	if resp["Policy"] == nil {
		t.Error("expected Policy in response")
	}

	pw := kmsRequest(t, svc, "PutKeyPolicy", map[string]string{
		"KeyId":      keyID,
		"PolicyName": "default",
		"Policy":     `{"Version":"2012-10-17","Statement":[]}`,
	})
	if pw.Code != http.StatusOK {
		t.Fatalf("PutKeyPolicy: expected 200, got %d", pw.Code)
	}
}

// --- Unknown action ---

func TestUnknownAction(t *testing.T) {
	svc := newTestService()

	w := kmsRequest(t, svc, "CreateGrant", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown action, got %d", w.Code)
	}
}
