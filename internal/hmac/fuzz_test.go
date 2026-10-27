package hmac_test

import (
	"testing"

	"github.com/WickTech/hookrelay/internal/hmac"
)

// FuzzVerify ensures Verify never panics on arbitrary input.
func FuzzVerify(f *testing.F) {
	f.Add("supersecret", "sha256=abc123", []byte(`{"event":"test"}`))
	f.Add("", "", []byte(`{}`))
	f.Add("s", "sha256=", []byte(nil))

	f.Fuzz(func(t *testing.T, secret, header string, body []byte) {
		_ = hmac.Verify(secret, header, body)
	})
}

// FuzzSign ensures Sign never panics and always produces a valid signature.
func FuzzSign(f *testing.F) {
	f.Add("secret", []byte(`{"x":1}`))
	f.Add("", []byte(nil))
	f.Add("x", []byte("hello"))

	f.Fuzz(func(t *testing.T, secret string, body []byte) {
		sig := hmac.Sign(secret, body)
		if secret != "" {
			if err := hmac.Verify(secret, sig, body); err != nil {
				t.Errorf("Sign produced invalid sig: %v", err)
			}
		}
	})
}
