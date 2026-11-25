package upnp

import (
	"context"
	"errors"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/devices/slimproto"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type groupOccupancyRegistry struct {
	deviceRegistry
	device Device
}

func (r groupOccupancyRegistry) Lookup(udn string) (Entry, bool) {
	return Entry{Device: r.device, Online: true}, udn == r.device.UDN
}

func TestPostgreSQLUPnPSlimProtoMemberIsNotReusableOutput(t *testing.T) {
	ctx := t.Context()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "roomusic_group_occupancy_"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	actor := identity.User{ID: uuid.New(), Username: "synthetic-listener", Role: "user"}
	if _, err := db.ExecContext(ctx, "INSERT INTO users(id,username,password_hash,role) VALUES($1,$2,'test-only',$3)", actor.ID, actor.Username, actor.Role); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')", identity.HashSessionToken(uuid.New()), actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE upnp_settings SET enabled=true, slimproto_enabled=true WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	member := Device{Kind: "slimproto", UDN: "slim:02:00:00:00:00:02", slimPlayer: &slimproto.Player{}}
	leader := &deviceOutput{
		actor: actor, ctx: ctx, deviceID: uuid.New(), udn: "slim:02:00:00:00:00:01",
		device: Device{Kind: "slimproto"},
	}
	slot := &deviceOutput{actor: actor, ctx: ctx, deviceID: uuid.New(), udn: member.UDN, device: member, leader: leader}
	s := &Service{db: db, protocols: map[string]*protocol{
		"slimproto": {ctx: ctx, registry: groupOccupancyRegistry{device: member}},
	}}
	s.AttachOutput(nil, "http://192.0.2.1:8080", "", nil)
	s.output.owners[leader.udn] = leader
	s.output.owners[slot.udn] = slot

	// 同一组的重复成员认领可复用。相同账号和设备的独立输出认领必须检查当前占用。
	if err := s.AcquireMember(ctx, actor.ID, leader.deviceID, slot.deviceID, slot.udn, false); err != nil {
		t.Fatalf("重复认领组成员：%v", err)
	}
	requireBusy := func(err error) {
		t.Helper()
		var busy *zones.DeviceBusyError
		if !errors.As(err, &busy) || busy.Occupant.UserID != actor.ID {
			t.Fatalf("期望设备由当前账号占用，实际 %v", err)
		}
	}
	requireBusy(s.Acquire(ctx, actor.ID, slot.deviceID, slot.udn, zones.Capabilities{}, false))

	// 同一账号已单独使用的设备也不能在自动重新入组时被接管。
	slot.leader = nil
	_, err = s.slimMember(context.Background(), leader, zones.GroupMember{DeviceID: slot.deviceID, UDN: slot.udn}, true)
	requireBusy(err)
	if err := s.Acquire(ctx, actor.ID, slot.deviceID, slot.udn, zones.Capabilities{}, false); err != nil {
		t.Fatalf("复用现有独立输出：%v", err)
	}
}
