package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 独立合成库避免边界样本的数据影响其他接口样本，仍经真实 app handler 生成。
func recordHistorySamples(t *testing.T, samples *apiSampleRecorder) {
	t.Helper()
	app := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, app)
	user := app.createUser(t, "user")
	id := library.addTrack(t, app, playbackTrackSpec{relative: "history.flac", content: flacBytes(4096), bitDepth: 24, sampleRate: 96000, channels: 2})
	mustExec(t, app, `UPDATE tracks SET duration_seconds=240,bitrate=500000 WHERE id=$1::uuid`, id)
	mustExec(t, app, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
 SELECT release_id,$2,'image/webp',640,480,'sample-history-cover.webp','embedded' FROM media WHERE id=(SELECT medium_id FROM tracks WHERE id=$1::uuid)`, id, strings.Repeat("e", 64))
	call := func(method, path string) *httptest.ResponseRecorder {
		return app.request(t, method, path, nil, &user, map[string]string{"X-Request-ID": sampleRequestID})
	}
	samples.record("listening/history-tracks-empty", call(http.MethodGet, historyTracksURL), 200)
	samples.record("listening/history-queue-empty", call(http.MethodGet, historyQueueURL), 200)
	for i := 0; i < 2; i++ {
		requirePlayRecorded(t, postPlay(t, app, &user, uuid.New(), id, 120))
	}
	samples.record("listening/history-tracks", call(http.MethodGet, historyTracksURL), 200)
	samples.record("listening/history-queue", call(http.MethodGet, historyQueueURL), 200)
	samples.record("listening/history-tracks-out-of-range", call(http.MethodGet, historyTracksURL+"?page=2"), 200)
	mustExec(t, app, `UPDATE tracks SET source_status='missing',duration_seconds=NULL,codec=NULL,bit_depth=NULL,sample_rate=NULL,channels=NULL,bitrate=NULL WHERE id=$1::uuid`, id)
	samples.record("listening/history-tracks-missing", call(http.MethodGet, historyTracksURL), 200)
	samples.record("listening/track-plays-deleted", call(http.MethodDelete, historyTracksURL+"/"+id), 200)
	samples.record("listening/track-plays-deleted-empty", call(http.MethodDelete, historyTracksURL+"/"+id), 200)
	// 1,001 个候选中包括 missing；固定时间与编号保证同序样本每次生成一致。
	mustExec(t, app, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,codec)
 SELECT ('20000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,t.medium_id,i+1,'历史曲目 '||i,'合成艺人',1,t.source_root_id,'history-'||i||'.flac',
 CASE WHEN i=1000 THEN 'missing' ELSE 'present' END,NOW(),'flac_vorbis_comment','flac'
 FROM tracks t CROSS JOIN generate_series(1,1001) i WHERE t.id=$1::uuid`, id)
	mustExec(t, app, `INSERT INTO play_history(user_id,track_id,idempotency_key,played_seconds,played_at)
 SELECT $1::uuid,id,gen_random_uuid(),120,'2026-11-01T00:00:00Z'::timestamptz+position*interval '1 second' FROM tracks WHERE id<>$2::uuid`, user.id, id)
	samples.record("listening/history-queue-truncated", call(http.MethodGet, historyQueueURL), 200)
}
