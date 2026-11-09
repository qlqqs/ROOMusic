package zones

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

// Service 的注册表只管理对象生命周期，播放状态由每区锁保护；仅支持单进程。
type Service struct {
	db          *sql.DB
	options     Options
	mu          sync.Mutex
	users       map[string]*userRuntime
	hub         *Hub
	ctx         context.Context
	cancel      context.CancelFunc
	closed      atomic.Bool
	cleanup     Timer
	entropyMu   sync.Mutex
	outputMu    sync.Mutex
	outputs     map[string]*outputSession
	offlineScan Timer
	workers     sync.WaitGroup
}
type userRuntime struct {
	mu      sync.RWMutex
	zones   map[string]*zoneRuntime
	devices map[string]*zoneRuntime
	order   []*zoneRuntime
}
type zoneRuntime struct {
	// dispatch 让解锁后的广播仍与同区命令顺序一致；双区始终按 ID 取锁。
	dispatch                  sync.Mutex
	mu                        sync.Mutex
	persisted                 PersistedZone
	device                    PersistedDevice
	online                    bool
	outputSessionID           *string
	deleted                   bool
	intent, status            string
	fault                     *Fault
	failures                  []Failure
	consecutiveFailures       int // 按渲染事件递增，起播命令先清零。
	pausedAt                  *Timestamp
	event                     *ZoneEvent
	signal                    json.RawMessage
	session                   RenderSession
	stream, nextStream        *Stream
	nextSession               RenderSession
	offlinePaused             bool
	prepared                  map[string]string
	cursorState               queue.State
	cursorTracks              map[string]catalog.TrackSummary
	generation                uint64
	rebuild                   Timer
	pause                     Timer
	rebuildVersion            uint64
	pauseVersion              uint64
	output                    *outputSession
	releasedAt                time.Time
	positionWrittenAt         time.Time
	currentHandle, nextHandle *renderHandle
	lostStream                *Stream
	resumeFailures            int
	resumeAwaitingSeq         int64
	visit                     *listenVisit
	resumeRetry               Timer
	resumeVersion             uint64
}

func NewService(db *sql.DB, options Options) *Service {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.AfterFunc == nil {
		options.AfterFunc = func(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
	}
	if options.Random == nil {
		options.Random = rand.Float64
	}
	if options.NewID == nil {
		options.NewID = uuid.New
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{db: db, options: options, users: map[string]*userRuntime{}, outputs: map[string]*outputSession{}, hub: NewHub(), ctx: ctx, cancel: cancel}
	if db != nil {
		s.scheduleCleanup()
		s.scheduleOfflineScan()
	}
	return s
}

func (s *Service) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.cancel()
	s.mu.Lock()
	if s.offlineScan != nil {
		s.offlineScan.Stop()
	}
	if s.cleanup != nil {
		s.cleanup.Stop()
	}
	users := make([]*userRuntime, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, u)
	}
	s.mu.Unlock()
	for _, u := range users {
		u.mu.Lock()
		for _, z := range u.order {
			z.mu.Lock()
			s.closeSession(z)
			s.cancelTimers(z)
			z.mu.Unlock()
		}
		u.mu.Unlock()
	}
	s.hub.Close()
	s.workers.Wait()
}

func (s *Service) user(ctx context.Context, userID string) (*userRuntime, error) {
	if !uuid.Valid(userID) {
		return nil, ErrInvalidInput
	}
	userID = strings.ToLower(userID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return nil, ErrClosed
	}
	if u := s.users[userID]; u != nil {
		return u, nil
	}
	if s.db == nil {
		return nil, ErrDatabaseUnavailable
	}
	state, err := loadUser(ctx, s.db, userID, s.options.Logger)
	if err != nil {
		return nil, databaseError(err)
	}
	u := &userRuntime{zones: map[string]*zoneRuntime{}, devices: map[string]*zoneRuntime{}, order: []*zoneRuntime{}}
	devices := map[string]PersistedDevice{}
	for _, d := range state.Devices {
		devices[d.DeviceID] = d
	}
	for _, p := range state.Zones {
		paused := timestamp(s.options.Now())
		z := &zoneRuntime{persisted: p, device: devices[p.DeviceID], intent: "pause", status: "idle", failures: []Failure{}, pausedAt: &paused, offlinePaused: true, prepared: map[string]string{}}
		if p.Queue.CurrentEntryID != "" {
			z.status = "paused"
		}
		u.zones[p.ZoneID] = z
		u.devices[p.DeviceID] = z
		u.order = append(u.order, z)
	}
	s.users[userID] = u
	return u, nil
}
func databaseError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrDatabaseUnavailable, err)
}
func (s *Service) env() queue.Env {
	return queue.Env{NewEntryID: s.newID, Random: func() float64 { s.entropyMu.Lock(); defer s.entropyMu.Unlock(); return s.options.Random() }}
}
func (s *Service) newID() string {
	s.entropyMu.Lock()
	defer s.entropyMu.Unlock()
	return strings.ToLower(s.options.NewID())
}
func (s *Service) transact(ctx context.Context, f func(*Store) error) error {
	if s.closed.Load() {
		return ErrClosed
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return databaseError(err)
	}
	defer tx.Rollback()
	if err = f(NewStore(tx)); err != nil {
		return databaseError(err)
	}
	return databaseError(tx.Commit())
}
func validID(id string) bool { return uuid.Valid(id) }
func defaultCapabilities() Capabilities {
	return Capabilities{StreamFormats: []StreamFormatCapability{}, MaxChannels: 2, DSD: "none", Volume: "number"}
}
func (s *Service) loadSummaries(ctx context.Context, entries []queue.Entry) (map[string]catalog.TrackSummary, error) {
	ids := make([]string, 0, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		if !seen[e.TrackID] {
			seen[e.TrackID] = true
			ids = append(ids, e.TrackID)
		}
	}
	out := make(map[string]catalog.TrackSummary, len(ids))
	for start := 0; start < len(ids); start += 1000 {
		batch, err := catalog.NewStore(s.db).LoadTrackSummaries(ctx, ids[start:min(start+1000, len(ids))])
		if err != nil {
			return nil, databaseError(err)
		}
		for id, t := range batch {
			out[id] = t
		}
	}
	return out, nil
}
func queueProjection(entries []queue.Entry, tracks map[string]catalog.TrackSummary) ([]QueueEntry, error) {
	out := make([]QueueEntry, 0, len(entries))
	for _, e := range entries {
		track, ok := tracks[e.TrackID]
		if !ok {
			return nil, fmt.Errorf("%w: missing queue summary", ErrDatabaseUnavailable)
		}
		out = append(out, QueueEntry{e.EntryID, cloneTrack(track)})
	}
	return out, nil
}
func (s *Service) Snapshot(ctx context.Context, userID string) (Snapshot, error) {
	u, err := s.user(ctx, userID)
	if err != nil {
		return Snapshot{}, err
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	// 与转移使用相同的 ID 锁序，避免读到源转移前、目标转移后的混合快照。
	ids := make([]string, 0, len(u.zones))
	for id := range u.zones {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	locked := make([]*zoneRuntime, 0, len(ids))
	for _, id := range ids {
		z := u.zones[id]
		z.mu.Lock()
		locked = append(locked, z)
	}
	defer func() {
		for i := len(locked) - 1; i >= 0; i-- {
			locked[i].mu.Unlock()
		}
	}()
	result := Snapshot{ServerTime: timestamp(s.options.Now()), Protocol: 1, Devices: []Device{}, Zones: []Zone{}}
	for _, z := range u.order {
		tracks, e := s.loadSummaries(ctx, z.persisted.Queue.Entries)
		if e != nil {
			return Snapshot{}, e
		}
		result.Devices = append(result.Devices, deviceOf(z))
		result.Zones = append(result.Zones, s.zoneOf(z, tracks))
	}
	return result, nil
}
func (s *Service) ReadQueue(ctx context.Context, userID, zoneID string) (QueueResult, error) {
	if !validID(zoneID) {
		return QueueResult{}, ErrInvalidInput
	}
	u, err := s.user(ctx, userID)
	if err != nil {
		return QueueResult{}, err
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	z := u.zones[strings.ToLower(zoneID)]
	if z == nil {
		return QueueResult{}, ErrNotFound
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	tracks, err := s.loadSummaries(ctx, z.persisted.Queue.Entries)
	if err != nil {
		return QueueResult{}, err
	}
	entries, err := queueProjection(z.persisted.Queue.Entries, tracks)
	return QueueResult{z.persisted.ZoneID, z.persisted.QueueRevision, entries}, err
}
func deviceOf(z *zoneRuntime) Device {
	d := z.device
	caps := cloneCapabilities(d.Capabilities)
	return Device{DeviceID: d.DeviceID, ZoneID: z.persisted.ZoneID, Name: d.Name, Kind: d.Kind, Online: z.online, LastSeenAt: timestamp(d.LastSeenAt), Volume: d.Volume, Muted: d.Muted, Capabilities: caps, OutputSessionID: cloneValue(z.outputSessionID)}
}
func (s *Service) zoneOf(z *zoneRuntime, tracks map[string]catalog.TrackSummary) Zone {
	p := z.persisted
	out := Zone{ZoneID: p.ZoneID, DeviceID: p.DeviceID, Revision: p.Revision, QueueRevision: p.QueueRevision, QueueLength: len(p.Queue.Entries), Mode: p.Queue.Mode, Settings: settingsOf(p.Settings), Intent: z.intent, Status: z.status, Fault: cloneValue(z.fault), Failures: append([]Failure{}, z.failures...), PausedAt: cloneValue(z.pausedAt), Stream: cloneStream(z.stream), NextStream: cloneStream(z.nextStream), Event: cloneZoneEvent(z.event), Position: Position{Seconds: p.PositionSeconds, At: timestamp(p.PositionSavedAt)}}
	if z.signal != nil {
		out.Signal = append(json.RawMessage(nil), z.signal...)
	}
	for _, e := range p.Queue.Entries {
		if e.EntryID == p.Queue.CurrentEntryID {
			if track, ok := tracks[e.TrackID]; ok {
				out.Current = &QueueEntry{e.EntryID, cloneTrack(track)}
				out.Position.DurationSeconds = cloneValue(track.DurationSeconds)
				out.Position.Seekable = true
			}
			break
		}
	}
	// has_next 只查询可用性，不消耗注入随机源。
	out.HasNext = queue.Advance(p.Queue, queue.Env{Random: func() float64 { return 0 }}, queue.AdvanceOptions{Reason: queue.ReasonManual, IsSkipped: func(id string) bool { return failed(z, id) }}).EntryID != ""
	return out
}
func failed(z *zoneRuntime, id string) bool {
	for _, f := range z.failures {
		if f.EntryID == id {
			return true
		}
	}
	return false
}
func sortedZones(a, b *zoneRuntime) (*zoneRuntime, *zoneRuntime) {
	// ID 不变，但命令会替换整个 persisted；读取仍需遵守其互斥锁。
	a.mu.Lock()
	aID := a.persisted.ZoneID
	a.mu.Unlock()
	b.mu.Lock()
	bID := b.persisted.ZoneID
	b.mu.Unlock()
	if aID < bID {
		return a, b
	}
	return b, a
}
func sortUser(u *userRuntime) {
	sort.Slice(u.order, func(i, j int) bool {
		u.order[i].mu.Lock()
		a := u.order[i].device
		u.order[i].mu.Unlock()
		u.order[j].mu.Lock()
		b := u.order[j].device
		u.order[j].mu.Unlock()
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.DeviceID < b.DeviceID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
}

// 异步边界保留操作与原因，不向客户端泄漏底层数据库信息。
func (s *Service) reportAsync(operation, userID, zoneID string, err error) {
	if s.closed.Load() {
		return
	}
	s.options.Logger.Error("zones.async.failed", "operation", operation, "user_id", userID, "zone_id", zoneID, "error", err)
}
