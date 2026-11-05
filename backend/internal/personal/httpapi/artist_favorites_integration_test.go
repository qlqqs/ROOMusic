package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLArtistLookupEmptyInputWhilePreparing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, postgrestest.NewSchema(t, "roomusic_test_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	userID, unknownID := uuid.New(), uuid.New()
	if _, err := database.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,'artist-lookup-test','test-only','user')`, userID); err != nil {
		t.Fatal(err)
	}
	token := identity.GenerateSessionToken()
	if _, err := database.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2::uuid,NOW()+INTERVAL '1 hour')`, identity.HashSessionToken(token), userID); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(identity.NewGuard(database, false, ""), personal.NewService(database)).Register(mux)
	for _, tc := range []struct {
		name, body     string
		status         int
		code           string
		tracks, albums int
	}{
		{name: "省略艺人字段", body: `{}`, status: 200},
		{name: "显式空数组", body: `{"artist_ids":[]}`, status: 200},
		{name: "全部为空", body: `{"artist_ids":[],"track_ids":[],"group_ids":[]}`, status: 200},
		{name: "空艺人不阻断旧状态", body: `{"artist_ids":[],"track_ids":["` + unknownID + `"],"group_ids":["` + unknownID + `"]}`, status: 200, tracks: 1, albums: 1},
		{name: "显式null拒绝", body: `{"artist_ids":null}`, status: 400, code: "invalid_input"},
		{name: "非空艺人准备态", body: `{"artist_ids":["` + unknownID + `"]}`, status: 503, code: "artist_catalog_preparing"},
		{name: "混合请求不返回部分状态", body: `{"artist_ids":["` + unknownID + `"],"track_ids":["` + unknownID + `"]}`, status: 503, code: "artist_catalog_preparing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://roomusic.test/api/v1/me/favorites/lookup", strings.NewReader(tc.body)).WithContext(ctx)
			r.Header.Set("Origin", "http://roomusic.test")
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body)
			}
			if tc.status == 200 {
				var result personal.FavoriteLookup
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Revision != 0 || result.Tracks == nil || result.Albums == nil || result.Artists == nil || len(result.Artists) != 0 || len(result.Tracks) != tc.tracks || len(result.Albums) != tc.albums {
					t.Fatalf("empty artist lookup contract: %+v", result)
				}
				for _, track := range result.Tracks {
					if track.Favorited || track.TrackID != unknownID {
						t.Fatalf("unknown track: %+v", track)
					}
				}
				for _, album := range result.Albums {
					if album.Favorited || album.GroupID != unknownID {
						t.Fatalf("unknown album: %+v", album)
					}
				}
				if w.Header().Get("Retry-After") != "" {
					t.Fatal("empty artist input inherited preparing retry")
				}
			} else {
				if !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(w.Body.String(), `"tracks":`) {
					t.Fatalf("error/partial response: %s", w.Body)
				}
				if tc.status == 503 && w.Header().Get("Retry-After") != "3" {
					t.Fatal("preparing response missing retry")
				}
			}
		})
	}
	var states int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_favorite_state WHERE user_id=$1::uuid`, userID).Scan(&states); err != nil || states != 0 {
		t.Fatalf("lookup created personal state: %d %v", states, err)
	}
}
