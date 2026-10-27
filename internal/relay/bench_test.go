package relay

import (
	"testing"
	"time"
)

func BenchmarkBackoff(b *testing.B) {
	for b.Loop() {
		_ = backoff(3)
	}
}

func BenchmarkBackoffCapped(b *testing.B) {
	for b.Loop() {
		_ = backoff(20) // should always return maxDelay ± jitter
	}
}

// BenchmarkBackoffSequence measures computing the full 1..10 sequence.
func BenchmarkBackoffSequence(b *testing.B) {
	for b.Loop() {
		var total time.Duration
		for i := 1; i <= 10; i++ {
			total += backoff(i)
		}
		_ = total
	}
}
