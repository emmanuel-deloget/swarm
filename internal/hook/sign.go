package hook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// Signature is the HMAC-SHA256 of the raw body under the secret, hex encoded —
// the form most senders use.
func Signature(secret string, body []byte) string {
	return hex.EncodeToString(sum(secret, body))
}

// SignatureBase64 is the same digest, base64 encoded. `swarm hook sign` prints
// both: comparing them against a real delivery settles which encoding a sender
// uses without having to read its documentation.
func SignatureBase64(secret string, body []byte) string {
	return base64.StdEncoding.EncodeToString(sum(secret, body))
}

// algoPrefixes are the labels senders put in front of the digest. They are
// stripped by name rather than by cutting at the first "=", because base64
// padding is an "=" too.
var algoPrefixes = []string{"sha256=", "sha-256=", "hmac-sha256=", "sha1="}

// VerifySignature reports whether presented is a valid HMAC-SHA256 of body
// under secret.
//
// It accepts hex or base64, with or without an algorithm label, because the
// convention varies from sender to sender and getting it wrong looks exactly
// like a wrong secret. Being liberal about the encoding costs nothing: the
// digest still has to match, and the comparison is constant time.
func VerifySignature(secret string, body []byte, presented string) bool {
	if secret == "" || presented == "" {
		return false
	}
	want := sum(secret, body)

	s := strings.TrimSpace(presented)
	for _, p := range algoPrefixes {
		if len(s) >= len(p) && strings.EqualFold(s[:len(p)], p) {
			s = s[len(p):]
			break
		}
	}

	for _, decode := range []func(string) ([]byte, error){
		hex.DecodeString,
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	} {
		if got, err := decode(s); err == nil && hmac.Equal(got, want) {
			return true
		}
	}
	return false
}

func sum(secret string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return mac.Sum(nil)
}
