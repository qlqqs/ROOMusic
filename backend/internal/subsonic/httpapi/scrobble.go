package httpapi

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

// 第三方客户端各自上报播放；每个用户只保留最近的一首。
type playingTrack struct {
	username, trackID, player string
	at                        time.Time
}
type nowPlayingStore struct {
	mu    sync.Mutex
	users map[string]playingTrack
}

func (s *nowPlayingStore) set(user string, t playingTrack) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.users == nil {
		s.users = make(map[string]playingTrack)
	}
	if prev, ok := s.users[user]; !ok || !prev.at.After(t.at) {
		s.users[user] = t
	}
}
func (s *nowPlayingStore) recent(now time.Time) []playingTrack {
	s.mu.Lock()
	result := make([]playingTrack, 0, len(s.users))
	for user, t := range s.users {
		if !t.at.After(now.Add(-10 * time.Minute)) {
			delete(s.users, user)
			continue
		}
		result = append(result, t)
	}
	s.mu.Unlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].at.Equal(result[j].at) {
			return result[i].username < result[j].username
		}
		return result[i].at.After(result[j].at)
	})
	return result
}

func scrobbleKey(user, track string, at time.Time) string {
	// 播放记录的幂等键是 UUID，按 RFC 9562 的自定义 v8 布局存放摘要。
	digest := sha256.Sum256([]byte(fmt.Sprintf("subsonic:%s:%s:%d", user, track, at.UnixMilli())))
	digest[6] = digest[6]&0xf | 0x80
	digest[8] = digest[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", digest[:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
}

func (h *Handlers) scrobble(r *http.Request) protocol.Response {
	id, err := entityID(r.Form)
	if err != nil {
		return browseFailure(err)
	}
	now := time.Now().UTC()
	at := now.Truncate(time.Second)
	if r.Form.Has("time") {
		millis, err := strconv.ParseInt(r.Form.Get("time"), 10, 64)
		if err != nil || millis < 0 {
			return browseFailure(errParameter)
		}
		at = time.UnixMilli(millis).UTC()
	}
	user := r.Context().Value(userKey{}).(identity.User)
	switch r.Form.Get("submission") {
	case "", "true":
		_, err = h.browse.Listening.RecordTrackAt(r.Context(), user.ID, scrobbleKey(user.ID, id, at), id, at)
	case "false":
		_, err = h.browse.Catalog.LoadTrackDuration(r.Context(), id)
		if err == nil {
			if at.After(now) {
				at = now
			}
			h.nowPlaying.set(user.ID, playingTrack{username: user.Username, trackID: id, player: r.Form.Get("c"), at: at})
		}
	default:
		err = errParameter
	}
	if err != nil {
		return browseFailure(err)
	}
	return protocol.Response{}
}

func (h *Handlers) getNowPlaying(r *http.Request) protocol.Response {
	now := time.Now()
	recent := h.nowPlaying.recent(now)
	out := protocol.NowPlaying{Entries: []protocol.NowPlayingEntry{}}
	if len(recent) == 0 {
		return protocol.Response{NowPlaying: &out}
	}
	ids := make([]string, len(recent))
	for i, item := range recent {
		ids[i] = item.trackID
	}
	tracks, err := h.browse.Catalog.LoadBrowseTracks(r.Context(), ids)
	if err != nil {
		return browseFailure(err)
	}
	mapped, err := h.ratedEntities(r, nil, nil, tracks)
	if err != nil {
		return browseFailure(err)
	}
	songs := make(map[string]protocol.Song, len(mapped.Songs))
	for _, song := range mapped.Songs {
		songs[song.ID] = song
	}
	for _, item := range recent {
		if song, ok := songs[item.trackID]; ok {
			out.Entries = append(out.Entries, protocol.NowPlayingEntry{Song: song, Username: item.username, PlayerName: item.player, MinutesAgo: max(0, int(now.Sub(item.at).Minutes()))})
		}
	}
	return protocol.Response{NowPlaying: &out}
}
