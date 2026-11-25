package zonestest

import (
	"context"
	"sync"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

// GroupOutputs 用真实的 zones 输出会话模拟托管设备及编组交接。
type GroupOutputs struct {
	Service *zones.Service
	Devices []zones.ManagedDevice

	mu       sync.Mutex
	sessions map[string]string
	members  map[string]string
}

func (g *GroupOutputs) VisibleDevices(context.Context, string) ([]zones.ManagedDevice, error) {
	return g.Devices, nil
}

func (g *GroupOutputs) Acquire(ctx context.Context, userID, deviceID, udn string, caps zones.Capabilities, _ bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sessions[udn] != "" {
		return nil
	}
	result, err := g.Service.ClaimOutput(ctx, userID, deviceID, zones.ClaimOutputInput{Managed: true, Protocol: 1, Capabilities: caps})
	if err != nil {
		return err
	}
	if g.sessions == nil {
		g.sessions = make(map[string]string)
	}
	g.sessions[udn] = result.SessionID
	return nil
}

func (g *GroupOutputs) ReleaseIdle(ctx context.Context, userID, udn, sessionID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sessions[udn] != sessionID {
		return nil
	}
	if err := g.Service.ReleaseOutput(ctx, userID, sessionID, zones.ReleaseOutputInput{}); err != nil {
		return err
	}
	delete(g.sessions, udn)
	return nil
}

func (g *GroupOutputs) AcquireMember(ctx context.Context, userID, leaderDeviceID, _ string, udn string, _ bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if sessionID := g.sessions[udn]; sessionID != "" {
		if err := g.Service.ReleaseOutput(ctx, userID, sessionID, zones.ReleaseOutputInput{}); err != nil {
			return err
		}
		delete(g.sessions, udn)
	}
	if g.members == nil {
		g.members = make(map[string]string)
	}
	g.members[udn] = leaderDeviceID
	return nil
}

func (g *GroupOutputs) ReleaseMember(_ context.Context, _, udn string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.members, udn)
	return nil
}

func (g *GroupOutputs) MemberLeader(udn string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.members[udn]
}
