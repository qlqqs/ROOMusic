package protocol_test

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func TestSubsonicProtocolResponseEncoding(t *testing.T) {
	// 字段结构参照 Subsonic XML 示例与 OpenSubsonic JSON 示例。
	const serverVersion = "v0.1.3+subsonic-test"
	for _, test := range []struct {
		name, xmlPayload, jsonPayload string
		response                      protocol.Response
		status                        string
		httpStatus                    int
	}{
		{
			name: "ping", status: "ok", httpStatus: http.StatusOK,
		},
		{
			name: "license", status: "ok", httpStatus: http.StatusOK,
			response:    protocol.Response{License: &protocol.License{Valid: true}},
			xmlPayload:  `<license valid="true"></license>`,
			jsonPayload: `,"license":{"valid":true}`,
		},
		{
			name: "extensions", status: "ok", httpStatus: http.StatusOK,
			response:    protocol.Response{Extensions: []protocol.Extension{{Name: "formPost", Versions: []int{1}}}},
			xmlPayload:  `<openSubsonicExtensions name="formPost"><versions>1</versions></openSubsonicExtensions>`,
			jsonPayload: `,"openSubsonicExtensions":[{"name":"formPost","versions":[1]}]`,
		},
		{
			name: "music-folders", status: "ok", httpStatus: http.StatusOK,
			response:    protocol.Response{MusicFolders: &protocol.MusicFolders{Folders: []protocol.MusicFolder{{ID: 1, Name: "ROOMusic"}}}},
			xmlPayload:  `<musicFolders><musicFolder id="1" name="ROOMusic"></musicFolder></musicFolders>`,
			jsonPayload: `,"musicFolders":{"musicFolder":[{"id":1,"name":"ROOMusic"}]}`,
		},
		{
			name: "user", status: "ok", httpStatus: http.StatusOK,
			response: protocol.Response{User: &protocol.User{
				Username: "listener", DownloadRole: true, StreamRole: true, JukeboxRole: true, Folders: []int{1},
			}},
			xmlPayload: `<user username="listener" scrobblingEnabled="false" adminRole="false" settingsRole="false" downloadRole="true" uploadRole="false" playlistRole="false" coverArtRole="false" commentRole="false" podcastRole="false" streamRole="true" jukeboxRole="true" shareRole="false" videoConversionRole="false"><folder>1</folder></user>`,
			jsonPayload: `,"user":{"username":"listener","scrobblingEnabled":false,"adminRole":false,"settingsRole":false,
				"downloadRole":true,"uploadRole":false,"playlistRole":false,"coverArtRole":false,"commentRole":false,
				"podcastRole":false,"streamRole":true,"jukeboxRole":true,"shareRole":false,"videoConversionRole":false,"folder":[1]}`,
		},
		{
			name: "jukebox-status", status: "ok", httpStatus: http.StatusOK,
			response:    protocol.Response{JukeboxStatus: &protocol.JukeboxStatus{CurrentIndex: 2, Playing: true, Gain: 0.5, Position: 30}},
			xmlPayload:  `<jukeboxStatus currentIndex="2" playing="true" gain="0.5" position="30"></jukeboxStatus>`,
			jsonPayload: `,"jukeboxStatus":{"currentIndex":2,"playing":true,"gain":0.5,"position":30}`,
		},
		{
			name: "jukebox-playlist", status: "ok", httpStatus: http.StatusOK,
			response: protocol.Response{JukeboxPlaylist: &protocol.JukeboxPlaylist{
				JukeboxStatus: protocol.JukeboxStatus{CurrentIndex: 0, Gain: 0.5},
				Entries: []protocol.Song{
					{ID: "first", Title: "潮汐 & 风", Duration: 120},
					{ID: "second", Title: "第二首", Duration: 180},
				},
			}},
			xmlPayload: `<jukeboxPlaylist currentIndex="0" playing="false" gain="0.5" position="0">
				<entry id="first" parent="" isDir="false" title="潮汐 &amp; 风" album="" artist="" duration="120" discNumber="0" albumId="" isVideo="false" type="" mediaType="" displayArtist="" displayAlbumArtist=""></entry>
				<entry id="second" parent="" isDir="false" title="第二首" album="" artist="" duration="180" discNumber="0" albumId="" isVideo="false" type="" mediaType="" displayArtist="" displayAlbumArtist=""></entry>
			</jukeboxPlaylist>`,
			jsonPayload: `,"jukeboxPlaylist":{"currentIndex":0,"playing":false,"gain":0.5,"position":0,"entry":[
				{"id":"first","parent":"","isDir":false,"title":"潮汐 & 风","album":"","artist":"","duration":120,"discNumber":0,"albumId":"","isVideo":false,"type":"","mediaType":"","displayArtist":"","displayAlbumArtist":""},
				{"id":"second","parent":"","isDir":false,"title":"第二首","album":"","artist":"","duration":180,"discNumber":0,"albumId":"","isVideo":false,"type":"","mediaType":"","displayArtist":"","displayAlbumArtist":""}
			]}`,
		},
		{
			name: "jukebox-empty-playlist", status: "ok", httpStatus: http.StatusOK,
			response: protocol.Response{JukeboxPlaylist: &protocol.JukeboxPlaylist{
				JukeboxStatus: protocol.JukeboxStatus{CurrentIndex: -1, Gain: 1},
				Entries:       []protocol.Song{},
			}},
			xmlPayload:  `<jukeboxPlaylist currentIndex="-1" playing="false" gain="1" position="0"></jukeboxPlaylist>`,
			jsonPayload: `,"jukeboxPlaylist":{"currentIndex":-1,"playing":false,"gain":1,"position":0,"entry":[]}`,
		},
		{
			name: "error", status: "failed", httpStatus: http.StatusOK,
			response:    protocol.Failure(protocol.MissingParameter, "Missing <username> & value"),
			xmlPayload:  `<error code="10" message="Missing &lt;username&gt; &amp; value"></error>`,
			jsonPayload: `,"error":{"code":10,"message":"Missing <username> & value"}`,
		},
		{
			name: "unknown-endpoint", status: "failed", httpStatus: http.StatusNotFound,
			response:    protocol.Failure(protocol.General, "Unknown endpoint"),
			xmlPayload:  `<error code="0" message="Unknown endpoint"></error>`,
			jsonPayload: `,"error":{"code":0,"message":"Unknown endpoint"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Run("XML", func(t *testing.T) {
				body := encodeSubsonicResponse(t, "xml", serverVersion, test.httpStatus, "application/xml; charset=utf-8", test.response)
				want := `<subsonic-response xmlns="http://subsonic.org/restapi" status="` + test.status +
					`" version="1.16.1" type="roomusic" serverVersion="` + serverVersion + `" openSubsonic="true">` +
					test.xmlPayload + `</subsonic-response>`
				if !reflect.DeepEqual(subsonicXMLTokens(t, body), subsonicXMLTokens(t, want)) {
					t.Errorf("XML 响应为 %s，期望 %s", body, want)
				}
			})
			t.Run("JSON", func(t *testing.T) {
				body := encodeSubsonicResponse(t, "json", serverVersion, test.httpStatus, "application/json", test.response)
				want := `{"subsonic-response":{"status":"` + test.status +
					`","version":"1.16.1","type":"roomusic","serverVersion":"` + serverVersion + `","openSubsonic":true` +
					test.jsonPayload + `}}`
				var gotValue, wantValue any
				if err := json.Unmarshal([]byte(body), &gotValue); err != nil {
					t.Fatalf("解析 JSON 响应失败：%v", err)
				}
				if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
					t.Fatalf("解析预期 JSON 失败：%v", err)
				}
				if !reflect.DeepEqual(gotValue, wantValue) {
					t.Errorf("JSON 响应为 %s，期望 %s", body, want)
				}
			})
		})
	}
}

func encodeSubsonicResponse(t *testing.T, format, version string, status int, contentType string, response protocol.Response) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	protocol.Write(recorder, format, version, status, response)
	if recorder.Code != status || recorder.Header().Get("Content-Type") != contentType || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP 状态和响应头为 %d %v，期望 %d %s 且禁止缓存", recorder.Code, recorder.Header(), status, contentType)
	}
	return recorder.Body.String()
}

func subsonicXMLTokens(t *testing.T, document string) []xml.Token {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(document))
	var tokens []xml.Token
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return tokens
		}
		if err != nil {
			t.Fatalf("解析 XML 失败：%v", err)
		}
		token = xml.CopyToken(token)
		if element, ok := token.(xml.StartElement); ok {
			slices.SortFunc(element.Attr, func(a, b xml.Attr) int {
				if result := strings.Compare(a.Name.Space, b.Name.Space); result != 0 {
					return result
				}
				return strings.Compare(a.Name.Local, b.Name.Local)
			})
		}
		if data, ok := token.(xml.CharData); ok && strings.TrimSpace(string(data)) == "" {
			continue
		}
		if pi, ok := token.(xml.ProcInst); ok && pi.Target == "xml" {
			continue
		}
		tokens = append(tokens, token)
	}
}
