package app

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// playbackModified 带有纳秒残留：文件系统的修改时间有纳秒精度，扫描记录（TIMESTAMPTZ）只到微秒。
var playbackModified = time.Date(2026, 10, 1, 8, 30, 15, 123456789, time.UTC)

// playbackLibrary 是测试专用允许根目录下登记的一个真实音乐目录。
type playbackLibrary struct {
	rootID string
	root   string
}

func createPlaybackLibrary(t *testing.T, application *integrationTestApplication) playbackLibrary {
	t.Helper()
	root, err := filepath.EvalSymlinks(createLibraryDirectory(t, application, "playback-library"))
	if err != nil {
		t.Fatalf("resolve playback library: %v", err)
	}
	rootID := uuid.New()
	mustExec(t, application, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, root)
	return playbackLibrary{rootID: rootID, root: root}
}

// patternBytes 返回以魔数开头、其余是可预测图案的内容，用来核对 Range 切片取到的就是文件的对应字节。
func patternBytes(magic []byte, total int) []byte {
	content := make([]byte, total)
	for index := range content {
		content[index] = byte(index % 251)
	}
	copy(content, magic)
	return content
}

func flacBytes(total int) []byte { return patternBytes([]byte("fLaC"), total) }

// mp4Bytes 返回以 ftyp atom 开头的内容（ALAC 与 AAC 的容器）。
func mp4Bytes(total int) []byte {
	content := patternBytes(nil, total)
	binary.BigEndian.PutUint32(content[0:4], 24)
	copy(content[4:8], "ftyp")
	copy(content[8:12], "M4A ")
	return content
}

type playbackTrackSpec struct {
	relative   string
	content    []byte
	sourceKind string
	codec      string
	// recordedSize 与 recordedModified 为 nil 时取文件的真实大小与 playbackModified，即“扫描后文件没有变化”。
	recordedSize     *int64
	recordedModified *time.Time
	skipFile         bool
	// bitDepth、sampleRate 与 channels 是扫描记录的音频参数，0 表示扫描没有取得（NULL）；派生核对用它们。
	bitDepth, sampleRate, channels int
	// fingerprint 是扫描记录的内容指纹，空则用“fingerprint-<曲目 ID>”（不是真实指纹，只适合不重算指纹的测试）；
	// noFingerprint 为真时写 NULL，表示扫描没有取得指纹。
	fingerprint   string
	noFingerprint bool
}

// optionalInt 把 0 写成 NULL。
func optionalInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

// addTrack 把文件写进目录并登记与之一致的 present 曲目（可用 spec 制造与扫描记录不符的情形），返回曲目 ID。
func (library playbackLibrary) addTrack(t *testing.T, application *integrationTestApplication, spec playbackTrackSpec) string {
	t.Helper()
	if spec.sourceKind == "" {
		spec.sourceKind, spec.codec = "flac_vorbis_comment", "flac"
	}
	if !spec.skipFile {
		path := filepath.Join(library.root, filepath.FromSlash(spec.relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create track directory: %v", err)
		}
		if err := os.WriteFile(path, spec.content, 0o644); err != nil {
			t.Fatalf("write track file: %v", err)
		}
		if err := os.Chtimes(path, playbackModified, playbackModified); err != nil {
			t.Fatalf("set track modification time: %v", err)
		}
	}
	size := int64(len(spec.content))
	if spec.recordedSize != nil {
		size = *spec.recordedSize
	}
	modified := playbackModified
	if spec.recordedModified != nil {
		modified = *spec.recordedModified
	}
	groupID, releaseID, mediumID, trackID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var fingerprint any = "fingerprint-" + trackID
	switch {
	case spec.noFingerprint:
		fingerprint = nil
	case spec.fingerprint != "":
		fingerprint = spec.fingerprint
	}
	mustExec(t, application, `INSERT INTO release_groups(id) VALUES($1::uuid)`, groupID)
	mustExec(t, application, `INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind)
		VALUES($1::uuid,$2::uuid,'专辑','艺术家',$3::uuid,$4,$5,'ordinary_directory')`, releaseID, groupID, library.rootID, "dir-"+trackID, library.rootID+":v2:"+trackID)
	mustExec(t, application, `INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,1,'Medium')`, mediumID, releaseID)
	mustExec(t, application, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,
			source_kind,codec,source_size,source_modified_at,content_fingerprint,bit_depth,sample_rate,channels)
		VALUES($1::uuid,$2::uuid,1,'曲目','艺术家',1,$3::uuid,$4,'present',NOW(),$5,$6,$7,$8,$9,$10,$11,$12)`,
		trackID, mediumID, library.rootID, spec.relative, spec.sourceKind, spec.codec, size, modified.UTC().Truncate(time.Microsecond), fingerprint,
		optionalInt(spec.bitDepth), optionalInt(spec.sampleRate), optionalInt(spec.channels))
	return trackID
}

func audioPath(trackID string) string { return "/api/v1/tracks/" + trackID + "/audio" }

func requireNoPathLeak(t *testing.T, application *integrationTestApplication, response *httptest.ResponseRecorder) {
	t.Helper()
	body := response.Body.String()
	for _, secret := range []string{application.allowedRoot, os.TempDir(), "playback-library"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaks %q: %s", secret, body)
		}
	}
}

func TestPostgreSQLPlaybackAudioRequiresLoginAndRevealsNothingWithoutIt(t *testing.T) {
	application := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, application)
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "a.flac", content: flacBytes(4096)})
	user := application.createUser(t, "user")

	for _, path := range []string{audioPath(trackID), "/api/v1/tracks/" + trackID + "/playback", "/api/v1/playback/formats", audioPath("not-a-uuid"), audioPath(uuid.New())} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, headers := range []map[string]string{nil, {"Range": "bytes=0-9"}, {"Range": "bytes=999999-"}, {"If-None-Match": "*"}} {
				response := application.request(t, method, path, nil, nil, headers)
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("anonymous %s %s %v = HTTP %d, want 401", method, path, headers, response.Code)
				}
				for _, header := range []string{"Content-Range", "ETag", "Accept-Ranges", "Last-Modified"} {
					if response.Header().Get(header) != "" {
						t.Fatalf("anonymous %s %s leaks the %s header: %q", method, path, header, response.Header().Get(header))
					}
				}
				if method == http.MethodGet {
					requireAPIError(t, response, http.StatusUnauthorized, "unauthorized")
					if strings.Contains(response.Body.String(), "4096") {
						t.Fatalf("anonymous response mentions the file size: %s", response.Body.String())
					}
				}
			}
		}
	}

	// 会话撤销后同样是 401。
	mustExec(t, application, `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid`, user.id)
	requireAPIError(t, application.request(t, http.MethodGet, audioPath(trackID), nil, &user, nil), http.StatusUnauthorized, "unauthorized")
}

func TestPostgreSQLPlaybackAudioStreamsTheOriginalFileWithProtectedHeaders(t *testing.T) {
	application := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, application)
	content := flacBytes(100_000)
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "专辑/01 曲目.flac", content: content})
	user := application.createUser(t, "user")
	get := func(method string, headers map[string]string) *httptest.ResponseRecorder {
		return application.request(t, method, audioPath(trackID), nil, &user, headers)
	}

	full := get(http.MethodGet, nil)
	if full.Code != http.StatusOK || !bytes.Equal(full.Body.Bytes(), content) {
		t.Fatalf("full read: HTTP %d, %d bytes (want %d)", full.Code, full.Body.Len(), len(content))
	}
	etag := full.Header().Get("ETag")
	if len(etag) != 34 || !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) || strings.HasPrefix(etag, `W/`) {
		t.Fatalf("ETag = %q, want a strong tag of 32 hexadecimal characters", etag)
	}
	for name, want := range map[string]string{
		"Content-Type":           "audio/flac",
		"Cache-Control":          "private, no-store",
		"Accept-Ranges":          "bytes",
		"X-Content-Type-Options": "nosniff",
		"Content-Length":         strconv.Itoa(len(content)),
	} {
		if got := full.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if full.Header().Get("Last-Modified") != "" {
		t.Errorf("Last-Modified = %q, want none", full.Header().Get("Last-Modified"))
	}
	if again := get(http.MethodGet, nil); again.Header().Get("ETag") != etag {
		t.Fatalf("ETag changed between two reads of an unchanged file: %q, %q", etag, again.Header().Get("ETag"))
	}

	head := get(http.MethodHead, nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(len(content)) || head.Header().Get("ETag") != etag {
		t.Fatalf("HEAD: HTTP %d, %d body bytes, Content-Length %q, ETag %q", head.Code, head.Body.Len(), head.Header().Get("Content-Length"), head.Header().Get("ETag"))
	}
	if head.Header().Get("Cache-Control") != "private, no-store" || head.Header().Get("Content-Type") != "audio/flac" {
		t.Fatalf("HEAD headers: Cache-Control %q, Content-Type %q", head.Header().Get("Cache-Control"), head.Header().Get("Content-Type"))
	}

	for _, testCase := range []struct {
		name         string
		rangeHeader  string
		start, end   int // 闭区间，对应 Content-Range
		wantContents []byte
	}{
		{"closed range", "bytes=10-19", 10, 19, content[10:20]},
		{"first byte", "bytes=0-0", 0, 0, content[:1]},
		{"open ended", "bytes=99000-", 99000, 99999, content[99000:]},
		{"suffix", "bytes=-50", 99950, 99999, content[99950:]},
		{"end clamped to the size", "bytes=99990-999999", 99990, 99999, content[99990:]},
		{"whole file", "bytes=0-", 0, 99999, content},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := get(http.MethodGet, map[string]string{"Range": testCase.rangeHeader})
			if response.Code != http.StatusPartialContent || !bytes.Equal(response.Body.Bytes(), testCase.wantContents) {
				t.Fatalf("%s: HTTP %d, %d bytes (want 206 and %d bytes)", testCase.rangeHeader, response.Code, response.Body.Len(), len(testCase.wantContents))
			}
			if want := fmt.Sprintf("bytes %d-%d/%d", testCase.start, testCase.end, len(content)); response.Header().Get("Content-Range") != want {
				t.Fatalf("Content-Range = %q, want %q", response.Header().Get("Content-Range"), want)
			}
			if response.Header().Get("ETag") != etag || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Content-Type") != "audio/flac" {
				t.Fatalf("206 headers: ETag %q, Cache-Control %q, Content-Type %q", response.Header().Get("ETag"), response.Header().Get("Cache-Control"), response.Header().Get("Content-Type"))
			}
		})
	}

	// If-Range：命中当前强校验值时按范围返回；不命中时 Range 被整体忽略，返回完整内容（即使范围本身无法满足也不是 416）。
	if response := get(http.MethodGet, map[string]string{"Range": "bytes=10-19", "If-Range": etag}); response.Code != http.StatusPartialContent || !bytes.Equal(response.Body.Bytes(), content[10:20]) {
		t.Fatalf("If-Range with the current tag: HTTP %d, %d bytes", response.Code, response.Body.Len())
	}
	for _, ifRange := range []string{`"stale-tag"`, `W/` + etag, "Wed, 21 Oct 2015 07:28:00 GMT", "garbage"} {
		for _, rangeHeader := range []string{"bytes=10-19", "bytes=9999999-"} {
			response := get(http.MethodGet, map[string]string{"Range": rangeHeader, "If-Range": ifRange})
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), content) {
				t.Fatalf("If-Range %q with %s: HTTP %d, %d bytes; want the complete 200", ifRange, rangeHeader, response.Code, response.Body.Len())
			}
		}
	}

	// 条件请求：命中强校验值得到 304，不带正文，保留校验值与缓存头。
	notModified := get(http.MethodGet, map[string]string{"If-None-Match": etag})
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 || notModified.Header().Get("ETag") != etag || notModified.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("If-None-Match: HTTP %d, %d body bytes, ETag %q, Cache-Control %q", notModified.Code, notModified.Body.Len(), notModified.Header().Get("ETag"), notModified.Header().Get("Cache-Control"))
	}
	if other := get(http.MethodGet, map[string]string{"If-None-Match": `"another-tag"`}); other.Code != http.StatusOK || other.Body.Len() != len(content) {
		t.Fatalf("a different If-None-Match must get the full response: HTTP %d", other.Code)
	}
	if conditional := get(http.MethodGet, map[string]string{"If-Modified-Since": time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)}); conditional.Code != http.StatusOK {
		t.Fatalf("If-Modified-Since must be ignored without a Last-Modified: HTTP %d", conditional.Code)
	}

	// 源文件始终只读：播放不改变修改时间与内容。
	info, err := os.Stat(filepath.Join(library.root, "专辑", "01 曲目.flac"))
	if err != nil || !info.ModTime().Equal(playbackModified) || info.Size() != int64(len(content)) {
		t.Fatalf("source file changed during playback: %v, %v", info, err)
	}
}

func TestPostgreSQLPlaybackAudioRejectsUnusableRangesWithAJSON416(t *testing.T) {
	application := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, application)
	content := flacBytes(5000)
	trackID := library.addTrack(t, application, playbackTrackSpec{relative: "a.flac", content: content})
	user := application.createUser(t, "user")

	for _, rangeHeader := range []string{
		"bytes=5000-",        // 起点等于大小
		"bytes=99999-100000", // 起点超出
		"bytes=0-1,5-6",      // 多范围
		"bytes=0-10,5-20",    // 重叠的多范围
		"bytes=abc",          // 语法错误
		"bytes=10-5",         // 终点早于起点
		"bytes=-0",           // 零长度后缀
		"items=0-5",          // 不是 bytes
		"bytes=",             // 空
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			response := application.request(t, method, audioPath(trackID), nil, &user, map[string]string{"Range": rangeHeader})
			if response.Code != http.StatusRequestedRangeNotSatisfiable {
				t.Fatalf("%s Range %q = HTTP %d, want 416", method, rangeHeader, response.Code)
			}
			if got := response.Header().Get("Content-Range"); got != "bytes */5000" {
				t.Fatalf("%s Range %q: Content-Range = %q, want bytes */5000", method, rangeHeader, got)
			}
			if got := response.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("%s Range %q: Content-Type = %q, want the JSON error envelope", method, rangeHeader, got)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("%s Range %q: Cache-Control = %q", method, rangeHeader, got)
			}
			if method == http.MethodGet {
				requireAPIError(t, response, http.StatusRequestedRangeNotSatisfiable, "range_not_satisfiable")
			}
		}
	}
}

func TestPostgreSQLPlaybackChecksRunInTheDocumentedOrder(t *testing.T) {
	application := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, application)
	user := application.createUser(t, "user")
	content := flacBytes(4096)
	changedSize := int64(len(content) - 1)
	changedTime := playbackModified.Add(time.Second)

	playable := library.addTrack(t, application, playbackTrackSpec{relative: "ok.flac", content: content})
	alac := library.addTrack(t, application, playbackTrackSpec{relative: "alac.m4a", content: mp4Bytes(4096), sourceKind: "m4a", codec: "alac"})
	cue := library.addTrack(t, application, playbackTrackSpec{relative: "album.cue#track-1@image.flac#index-0", content: content, skipFile: true, sourceKind: "cue_virtual", codec: "flac"})
	floatWAV := library.addTrack(t, application, playbackTrackSpec{relative: "float.wav", content: content, sourceKind: "wav_info", codec: "ieee_float"})
	gone := library.addTrack(t, application, playbackTrackSpec{relative: "gone.flac", content: content, skipFile: true})
	sizeChanged := library.addTrack(t, application, playbackTrackSpec{relative: "size.flac", content: content, recordedSize: &changedSize})
	timeChanged := library.addTrack(t, application, playbackTrackSpec{relative: "time.flac", content: content, recordedModified: &changedTime})
	notFLAC := library.addTrack(t, application, playbackTrackSpec{relative: "not-flac.flac", content: mp4Bytes(4096)})
	missing := library.addTrack(t, application, playbackTrackSpec{relative: "missing.flac", content: content})
	mustExec(t, application, `UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, missing)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.flac"), content, 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Chtimes(filepath.Join(outside, "secret.flac"), playbackModified, playbackModified); err != nil {
		t.Fatalf("set outside modification time: %v", err)
	}
	escaped := library.addTrack(t, application, playbackTrackSpec{relative: "escape.flac", content: content, skipFile: true})
	if err := os.Symlink(filepath.Join(outside, "secret.flac"), filepath.Join(library.root, "escape.flac")); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}

	type expectation struct {
		name    string
		trackID string
		status  int
		code    string
	}
	cases := []expectation{
		{"unknown track", uuid.New(), http.StatusNotFound, "not_found"},
		{"missing track", missing, http.StatusNotFound, "not_found"},
		{"cue virtual track", cue, http.StatusConflict, "not_directly_playable"},
		{"format outside the table", floatWAV, http.StatusConflict, "not_directly_playable"},
		{"ALAC while derivation is unavailable", alac, http.StatusConflict, "not_directly_playable"},
		{"file is gone", gone, http.StatusConflict, "source_unavailable"},
		{"link leaves the root", escaped, http.StatusConflict, "source_unavailable"},
		{"size differs from the scan", sizeChanged, http.StatusConflict, "source_changed"},
		{"modification time differs from the scan", timeChanged, http.StatusConflict, "source_changed"},
		{"container differs from the format", notFLAC, http.StatusConflict, "source_changed"},
	}
	for _, testCase := range cases {
		for _, target := range []string{audioPath(testCase.trackID), "/api/v1/tracks/" + testCase.trackID + "/playback"} {
			t.Run(testCase.name+" "+target[strings.LastIndex(target, "/")+1:], func(t *testing.T) {
				response := application.request(t, http.MethodGet, target, nil, &user, map[string]string{"Range": "bytes=0-9"})
				requireAPIError(t, response, testCase.status, testCase.code)
				requireNoPathLeak(t, application, response)
				if response.Header().Get("ETag") != "" || response.Header().Get("Accept-Ranges") != "" || response.Header().Get("Content-Range") != "" {
					t.Fatalf("an error response carries representation headers: %v", response.Header())
				}
			})
		}
	}
	requireAPIError(t, application.request(t, http.MethodGet, audioPath("not-a-uuid"), nil, &user, nil), http.StatusBadRequest, "invalid_id")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/tracks/not-a-uuid/playback", nil, &user, nil), http.StatusBadRequest, "invalid_id")

	// 目录停用先于格式判断：停用目录里的任何曲目（包括 CUE 虚拟轨）都是 root_disabled，专辑详情仍可浏览。
	mustExec(t, application, `UPDATE library_roots SET status='disabled' WHERE id=$1::uuid`, library.rootID)
	for _, trackID := range []string{playable, alac, cue, floatWAV, gone} {
		for _, target := range []string{audioPath(trackID), "/api/v1/tracks/" + trackID + "/playback"} {
			requireAPIError(t, application.request(t, http.MethodGet, target, nil, &user, nil), http.StatusConflict, "root_disabled")
		}
	}
	requireAPIError(t, application.request(t, http.MethodGet, audioPath(missing), nil, &user, nil), http.StatusNotFound, "not_found")
	if response := application.request(t, http.MethodGet, "/api/v1/releases", nil, &user, nil); response.Code != http.StatusOK {
		t.Fatalf("a disabled root must stay browsable: HTTP %d", response.Code)
	}
	mustExec(t, application, `UPDATE library_roots SET status='active' WHERE id=$1::uuid`, library.rootID)
	if response := application.request(t, http.MethodGet, audioPath(playable), nil, &user, nil); response.Code != http.StatusOK {
		t.Fatalf("a restored root must play again: HTTP %d", response.Code)
	}
}

func TestPostgreSQLPlaybackPreflightDescribesTheOriginalRepresentation(t *testing.T) {
	application := newIntegrationTestApplication(t)
	library := createPlaybackLibrary(t, application)
	user := application.createUser(t, "user")
	flac := library.addTrack(t, application, playbackTrackSpec{relative: "a.flac", content: flacBytes(8192)})
	aac := library.addTrack(t, application, playbackTrackSpec{relative: "b.m4a", content: mp4Bytes(6000), sourceKind: "m4a", codec: "aac"})

	type preflight struct {
		TrackID        string `json:"track_id"`
		Representation struct {
			Kind      string  `json:"kind"`
			URL       string  `json:"url"`
			MIMEType  string  `json:"mime_type"`
			Codecs    *string `json:"codecs"`
			SizeBytes int64   `json:"size_bytes"`
		} `json:"representation"`
	}
	read := func(trackID string) preflight {
		t.Helper()
		response := application.request(t, http.MethodGet, "/api/v1/tracks/"+trackID+"/playback", nil, &user, nil)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("preflight: HTTP %d, Cache-Control %q, Content-Type %q: %s", response.Code, response.Header().Get("Cache-Control"), response.Header().Get("Content-Type"), response.Body.String())
		}
		var decoded preflight
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode preflight: %v", err)
		}
		requireNoPathLeak(t, application, response)
		return decoded
	}

	flacResult := read(flac)
	if flacResult.TrackID != flac || flacResult.Representation.Kind != "original" || flacResult.Representation.URL != audioPath(flac) ||
		flacResult.Representation.MIMEType != "audio/flac" || flacResult.Representation.Codecs != nil || flacResult.Representation.SizeBytes != 8192 {
		t.Fatalf("FLAC preflight = %+v", flacResult)
	}
	aacResult := read(aac)
	if aacResult.Representation.MIMEType != "audio/mp4" || aacResult.Representation.Codecs == nil || *aacResult.Representation.Codecs != "mp4a.40.2" || aacResult.Representation.SizeBytes != 6000 {
		t.Fatalf("AAC preflight = %+v", aacResult)
	}
	// 预检返回的地址就是音频接口，两者的核对完全一致。
	if response := application.request(t, http.MethodGet, flacResult.Representation.URL, nil, &user, nil); response.Code != http.StatusOK || response.Body.Len() != 8192 {
		t.Fatalf("the preflight URL did not serve the file: HTTP %d", response.Code)
	}
	// 预检每次都重新核对文件：删除之后马上得到准确的错误，不依赖上一次的结果。
	if err := os.Remove(filepath.Join(library.root, "a.flac")); err != nil {
		t.Fatalf("remove source: %v", err)
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/tracks/"+flac+"/playback", nil, &user, nil), http.StatusConflict, "source_unavailable")
}

func TestPostgreSQLPlaybackFormatsListTheDirectlyPlayableCombinationsWithoutDerivation(t *testing.T) {
	application := newIntegrationTestApplication(t)
	user := application.createUser(t, "user")
	response := application.request(t, http.MethodGet, "/api/v1/playback/formats", nil, &user, nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("formats: HTTP %d, Cache-Control %q", response.Code, response.Header().Get("Cache-Control"))
	}
	var decoded struct {
		Items []struct {
			SourceKind string  `json:"source_kind"`
			Codec      string  `json:"codec"`
			Delivery   string  `json:"delivery"`
			MIMEType   string  `json:"mime_type"`
			Codecs     *string `json:"codecs"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode formats: %v", err)
	}
	var got []string
	for _, item := range decoded.Items {
		codecs := "null"
		if item.Codecs != nil {
			codecs = *item.Codecs
		}
		got = append(got, item.SourceKind+"/"+item.Codec+" "+item.Delivery+" "+item.MIMEType+" "+codecs)
	}
	// 没有派生时只有直出格式；ALAC 不在其中（派生可用时才出现，见 playback_derivation_integration_test.go）。
	want := []string{
		"flac_vorbis_comment/flac original audio/flac null",
		"mp3_id3v2/mp3 original audio/mpeg null",
		"wav_info/pcm original audio/wav 1",
		"m4a/aac original audio/mp4 mp4a.40.2",
		"ogg_vorbis_comment/vorbis original audio/ogg vorbis",
		"opus_tags/opus original audio/ogg opus",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("formats =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPostgreSQLPlaybackRoutesRejectOtherMethods(t *testing.T) {
	application := newIntegrationTestApplication(t)
	user := application.createUser(t, "user")
	for _, path := range []string{"/api/v1/playback/formats", "/api/v1/tracks/" + uuid.New() + "/playback", audioPath(uuid.New())} {
		response := application.request(t, http.MethodPost, path, map[string]any{}, &user, nil)
		requireAPIError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		if allow := response.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Fatalf("POST %s: Allow = %q, want GET, HEAD", path, allow)
		}
	}
}
