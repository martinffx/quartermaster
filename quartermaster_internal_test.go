package quartermaster

import (
	"context"
	"testing"
	"time"
)

func TestRollbackTimeout(t *testing.T) {
	future, cancelFuture := context.WithTimeout(context.Background(), time.Minute)
	defer cancelFuture()
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()

	tests := []struct {
		name       string
		ctx        context.Context
		configured time.Duration
		check      func(time.Duration) bool
		want       string
	}{
		{"option wins over a ctx deadline", future, 2 * time.Second, func(d time.Duration) bool { return d == 2*time.Second }, "2s"},
		{"option wins with no deadline", context.Background(), 2 * time.Second, func(d time.Duration) bool { return d == 2*time.Second }, "2s"},
		{"live ctx deadline is used", future, 0, func(d time.Duration) bool { return d > 0 && d <= time.Minute }, "(0, 1m]"},
		{"expired ctx deadline falls back", expired, 0, func(d time.Duration) bool { return d == defaultRollbackTimeout }, "default"},
		{"no deadline falls back", context.Background(), 0, func(d time.Duration) bool { return d == defaultRollbackTimeout }, "default"},
		{"negative option is ignored", context.Background(), -time.Second, func(d time.Duration) bool { return d == defaultRollbackTimeout }, "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rollbackTimeout(tt.ctx, tt.configured); !tt.check(got) {
				t.Fatalf("rollbackTimeout = %v, want %s", got, tt.want)
			}
		})
	}
}
