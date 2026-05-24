package cognito

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
)

const jwkKID = "nimbus-1"

func base64urlEncode(b []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
}

func base64urlDecode(s string) ([]byte, error) {
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

func signJWT(key *rsa.PrivateKey, claims map[string]interface{}) (string, error) {
	hdr, _ := json.Marshal(map[string]string{
		"alg": "RS256",
		"kid": jwkKID,
		"typ": "JWT",
	})
	pay, _ := json.Marshal(claims)

	unsigned := base64urlEncode(hdr) + "." + base64urlEncode(pay)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64urlEncode(sig), nil
}

func parseJWTPayload(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}
	b, err := base64urlDecode(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(b, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func jwksResponse(pub *rsa.PublicKey) map[string]interface{} {
	nBytes := pub.N.Bytes()

	eBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(eBuf, uint32(pub.E))
	for len(eBuf) > 1 && eBuf[0] == 0 {
		eBuf = eBuf[1:]
	}

	return map[string]interface{}{
		"keys": []map[string]interface{}{{
			"kty": "RSA",
			"kid": jwkKID,
			"use": "sig",
			"alg": "RS256",
			"n":   base64urlEncode(nBytes),
			"e":   base64urlEncode(eBuf),
		}},
	}
}
