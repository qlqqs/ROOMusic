package cast

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/cast/castv2"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestCastMediaStatusTransitions(t *testing.T) {
	for _, tc := range []struct {
		state, reason, wantState string
		wantID                   int
		wantErr                  error
	}{
		{"PLAYING", "", "PLAYING", 42, zones.ErrOutputUnknown},
		{"PAUSED", "", "PAUSED", 42, zones.ErrOutputUnknown},
		{"BUFFERING", "", "BUFFERING", 42, zones.ErrOutputUnknown},
		{"IDLE", "FINISHED", "FINISHED", 0, zones.ErrOutputUnknown},
		{"IDLE", "CANCELLED", "IDLE", 0, errTaken},
		{"IDLE", "INTERRUPTED", "IDLE", 0, errTaken},
		{"IDLE", "ERROR", "IDLE", 0, zones.ErrOutputUnknown},
	} {
		t.Run(tc.state+tc.reason, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := session{
					Output: &Output{Zones: &zones.Service{}, UserID: "00000000-0000-4000-8000-000000000001",
						SessionID: "00000000-0000-4000-8000-000000000002"},
					ctx: t.Context(), transport: "transport-test", mediaID: 42, seconds: 9, state: "PLAYING",
				}
				position := 12.0
				if tc.state == "IDLE" {
					position = 0
				}
				e := castv2.Event{
					Message: castv2.Message{Namespace: castv2.Media, Source: s.transport},
					Envelope: castv2.Envelope{Type: "MEDIA_STATUS", Status: json.RawMessage(fmt.Sprintf(
						`[{"mediaSessionId":42,"playerState":%q,"idleReason":%q,"currentTime":%g}]`, tc.state, tc.reason, position))},
					Order: 2,
				}
				if err := s.event(e); !errors.Is(err, tc.wantErr) {
					t.Fatalf("状态错误 = %v，期望 %v", err, tc.wantErr)
				}
				if s.mediaID != tc.wantID || s.state != tc.wantState {
					t.Fatalf("媒体会话 = %d，状态 %s，期望 %d / %s", s.mediaID, s.state, tc.wantID, tc.wantState)
				}
				if tc.state == "IDLE" {
					position = 9
				}
				time.Sleep(time.Second)
				if tc.state == "PLAYING" {
					position++
				}
				if got := s.position(); got != position {
					t.Fatalf("播放头 = %g，期望 %g", got, position)
				}
				e.Order = 1
				if err := s.event(e); err != nil || s.mediaOrder != 2 {
					t.Fatalf("较早的状态覆盖了当前状态：顺序 %d，错误 %v", s.mediaOrder, err)
				}
			})
		})
	}
}

func TestCastReceiverStatusReplacedApplication(t *testing.T) {
	s := session{transport: "transport-test"}
	for _, tc := range []struct {
		status string
		want   error
	}{
		{`{"applications":[{"appId":"CC1AD845","transportId":"transport-test"}],"volume":{"controlType":"fixed"}}`, nil},
		{`{"applications":[{"appId":"CC1AD845","transportId":"transport-other"}]}`, errTaken},
		{`{"applications":[{"appId":"FAKEOTHER","transportId":"transport-test"}]}`, errTaken},
		{`{"applications":[]}`, errTaken},
	} {
		e := castv2.Event{
			Message:  castv2.Message{Namespace: castv2.Receiver, Source: castv2.ReceiverID},
			Envelope: castv2.Envelope{Type: "RECEIVER_STATUS", Status: json.RawMessage(tc.status)},
			Order:    s.receiverOrder + 1,
		}
		if err := s.event(e); !errors.Is(err, tc.want) {
			t.Errorf("接收端状态 %s 的错误 = %v，期望 %v", tc.status, err, tc.want)
		}
	}
}
