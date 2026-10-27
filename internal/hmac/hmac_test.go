package hmac_test

import (
	"testing"

	"github.com/WickTech/hookrelay/internal/hmac"
)

func TestVerify(t *testing.T) {
	body := []byte(`{"event":"test"}`)
	secret := "supersecret"
	sig := hmac.Sign(secret, body)

	t.Run("valid signature passes", func(t *testing.T) {
		if err := hmac.Verify(secret, sig, body); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("wrong secret fails", func(t *testing.T) {
		if err := hmac.Verify("wrong", sig, body); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("tampered body fails", func(t *testing.T) {
		if err := hmac.Verify(secret, sig, []byte(`{"event":"tampered"}`)); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("missing prefix fails", func(t *testing.T) {
		if err := hmac.Verify(secret, "deadbeef", body); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("empty secret skips verification", func(t *testing.T) {
		if err := hmac.Verify("", "sha256=anything", body); err != nil {
			t.Fatalf("expected nil for empty secret, got %v", err)
		}
	})
}
