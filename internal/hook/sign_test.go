package hook

import (
	"testing"
)

// TestVerifySignature: the encoding a sender uses varies, and guessing wrong
// looks exactly like a wrong secret. swarm accepts the usual forms so that a
// rejection means what it says.
func TestVerifySignature(t *testing.T) {
	const secret = "s3cret"
	body := []byte(payload)
	hexSig := Signature(secret, body)
	b64Sig := SignatureBase64(secret, body)

	good := []struct {
		name string
		sig  string
	}{
		{"hex", hexSig},
		{"hex with a label", "sha256=" + hexSig},
		{"an uppercase label", "SHA256=" + hexSig},
		{"base64", b64Sig},
		{"base64 with a label", "sha256=" + b64Sig},
		{"surrounding space", "  " + hexSig + "  "},
	}
	for _, c := range good {
		t.Run(c.name, func(t *testing.T) {
			if !VerifySignature(secret, body, c.sig) {
				t.Errorf("VerifySignature(%q) = false, want true", c.sig)
			}
		})
	}

	bad := []struct {
		name   string
		secret string
		body   []byte
		sig    string
	}{
		{"the wrong secret", "wrong", body, hexSig},
		{"a tampered body", secret, []byte(`{"action":"closed"}`), hexSig},
		{"no signature at all", secret, body, ""},
		{"no secret configured", "", body, hexSig},
		{"junk", secret, body, "not-a-digest"},
		{"a truncated digest", secret, body, hexSig[:20]},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if VerifySignature(c.secret, c.body, c.sig) {
				t.Errorf("VerifySignature() accepted %s", c.name)
			}
		})
	}
}
