package upnp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAirPlayPINChallengeExpired(t *testing.T) {
	lifetime, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	const key = "airplay:02:00:00:00:00:01"
	s := &Service{pendingPIN: map[string]*pinChallenge{key: {ctx: lifetime}}}
	if err := s.FinishAirPlayPIN(t.Context(), key, "0123"); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("挑战已超过截止时间时提交 PIN 得到 %v", err)
	}
}
