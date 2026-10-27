package hmac

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Verify checks that the X-Hub-Signature-256 header matches the body.
// header format: "sha256=<hex>"
func Verify(secret, header string, body []byte) error {
	if secret == "" {
		return nil // target has no secret configured — skip verification
	}
	prefix := "sha256="
	if !strings.HasPrefix(header, prefix) {
		return fmt.Errorf("signature missing sha256= prefix")
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return fmt.Errorf("signature hex decode: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// Sign produces the X-Hub-Signature-256 value for a payload.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
