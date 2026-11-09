package zones

import "time"

// listenVisit 移植原 ListenTracker；重新开流只清采样基准，不丢累计或幂等键。
type listenVisit struct {
	id, entryID, trackID   string
	threshold, accumulated float64
	position               float64
	at                     time.Time
	baseline, recorded     bool
	pendingSeconds         *float64
}

func (s *Service) beginVisit(z *zoneRuntime, entryID string, force bool) {
	if !force && z.visit != nil && z.visit.entryID == entryID {
		z.visit.baseline = false
		return
	}
	for _, e := range z.persisted.Queue.Entries {
		if e.EntryID != entryID {
			continue
		}
		threshold := 240.0
		if d := z.cursorTracks[e.TrackID].DurationSeconds; d != nil && finite(*d) && *d > 0 {
			threshold = min(*d/2, threshold)
		}
		z.visit = &listenVisit{id: s.newID(), entryID: entryID, trackID: e.TrackID, threshold: threshold}
		return
	}
	z.visit = nil
}
func suspendVisit(z *zoneRuntime) {
	if z.visit != nil {
		z.visit.baseline = false
	}
}

// 达到门槛后冻结秒数：同一个幂等键的所有重试必须逐字段相同。
func (v *listenVisit) sample(position float64, at time.Time) bool {
	if v == nil || v.recorded || !finite(position) {
		return false
	}
	if v.baseline {
		delta := position - v.position
		elapsed := at.Sub(v.at).Seconds()
		if elapsed >= 0 && delta > 0 && delta <= elapsed+1 {
			v.accumulated += delta
		}
	}
	v.position, v.at, v.baseline = position, at, true
	if v.pendingSeconds == nil && v.accumulated >= v.threshold {
		seconds := min(v.accumulated, 86400)
		v.pendingSeconds = &seconds
	}
	return v.pendingSeconds != nil
}
