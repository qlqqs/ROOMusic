package stewardtools

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func (f *c5Fixture) playlist(t *testing.T, user string) (string, []string) {
	t.Helper()
	result, err := f.service.Execute(context.Background(), user, uuid.New(), personal.Command{Kind: "create", Name: "合成歌单", TrackIDs: f.tracks})
	if err != nil || result.Status != 201 {
		t.Fatalf("create fixture: %+v %v", result, err)
	}
	var body struct {
		Playlist struct {
			ID string `json:"id"`
		} `json:"playlist"`
	}
	if err := json.Unmarshal(result.Body, &body); err != nil {
		t.Fatal(err)
	}
	items, err := f.service.PlaylistItems(context.Background(), user, body.Playlist.ID, personal.Page{Number: 1, Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(items)
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	if len(ids) != 2 {
		t.Fatalf("fixture items=%s", raw)
	}
	return body.Playlist.ID, ids
}

func TestPostgreSQLStewardC5PersonalPlaylistTools(t *testing.T) {
	f := newC5Fixture(t)
	addedTrack := uuid.New()
	_, err := f.db.Exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at,duration_seconds) SELECT $1,medium_id,3,'新增曲目',artist,source_root_id,'album/extra.flac',NOW(),180 FROM tracks WHERE id=$2`, addedTrack, f.tracks[0])
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		extra map[string]any
		scope string
	}{
		{"get_playlist", nil, "read"},
		{"rename_playlist", map[string]any{"name": "新歌单"}, "personal"},
		{"delete_playlist", nil, "personal"},
		{"add_playlist_tracks", map[string]any{"track_ids": []string{addedTrack}}, "personal"},
		{"remove_playlist_track", nil, "personal"},
		{"move_playlist_track", map[string]any{"action": "last"}, "personal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, scenario := range []string{"success", "other_user", "missing", "conflict"} {
				if scenario == "conflict" && tc.scope == "read" {
					continue
				}
				t.Run(scenario, func(t *testing.T) {
					owner := f.a
					if scenario == "other_user" {
						owner = f.b
					}
					id, items := f.playlist(t, owner)
					in := map[string]any{"playlist_id": id}
					if tc.scope == "personal" {
						in["expected_revision"] = 1
					}
					for k, v := range tc.extra {
						in[k] = v
					}
					if tc.name == "remove_playlist_track" || tc.name == "move_playlist_track" {
						in["item_id"] = items[0]
					}
					want := ""
					switch scenario {
					case "other_user":
						want = "not_found"
					case "missing":
						in["playlist_id"] = uuid.New()
						want = "not_found"
					case "conflict":
						in["expected_revision"] = 2
						want = "revision_conflict"
					}
					raw := f.invoke(t, f.a, tc.name, in, want)
					if want != "" {
						detail, err := f.service.PlaylistItems(context.Background(), owner, id, personal.Page{Number: 1, Size: 20})
						if err != nil {
							t.Fatal(err)
						}
						encoded, _ := json.Marshal(detail)
						if !strings.Contains(string(encoded), `"revision":1`) {
							t.Fatalf("失败改变歌单：%s", encoded)
						}
						return
					}
					if tc.name == "delete_playlist" {
						f.invoke(t, f.a, "get_playlist", map[string]any{"playlist_id": id}, "not_found")
						return
					}
					if !strings.Contains(string(raw), id) {
						t.Fatalf("返回缺少歌单 ID：%s", raw)
					}
					if tc.name == "rename_playlist" && !strings.Contains(string(raw), "新歌单") {
						t.Fatal(string(raw))
					}
					if tc.name == "add_playlist_tracks" {
						detail, err := f.service.PlaylistItems(context.Background(), f.a, id, personal.Page{Number: 1, Size: 20})
						if err != nil || len(detail.Items) != 3 || detail.Items[2].Track.TrackID != addedTrack {
							t.Fatalf("添加曲目未生效：%+v %v", detail, err)
						}
					}
					if tc.name == "remove_playlist_track" {
						detail, err := f.service.PlaylistItems(context.Background(), f.a, id, personal.Page{Number: 1, Size: 20})
						if err != nil || len(detail.Items) != 1 {
							t.Fatalf("移除未生效：%+v %v", detail, err)
						}
					}
					if tc.name == "move_playlist_track" {
						detail, err := f.service.PlaylistItems(context.Background(), f.a, id, personal.Page{Number: 1, Size: 20})
						if err != nil {
							t.Fatal(err)
						}
						encoded, _ := json.Marshal(detail.Items)
						if strings.Index(string(encoded), items[0]) < strings.Index(string(encoded), items[1]) {
							t.Fatalf("移动未生效：%s", encoded)
						}
					}
				})
			}
		})
	}
}

func TestPostgreSQLStewardC5PersonalListsFavoritesAndCreate(t *testing.T) {
	f := newC5Fixture(t)
	ctx := context.Background()
	foreign, _ := f.playlist(t, f.b)
	artist := uuid.New()
	for _, q := range []string{`INSERT INTO artists(id,initial_name) VALUES($1,'合成艺人')`, `INSERT INTO artist_profiles(artist_id,auto_display_name,effective_display_name,sort_key,initial_group,version) VALUES($1,'合成艺人','合成艺人','synthetic','S',1)`} {
		if _, err := f.db.Exec(q, artist); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ kind, id string }{{"track", f.tracks[0]}, {"album", f.group}, {"artist", artist}} {
		t.Run(tc.kind, func(t *testing.T) {
			if _, err := f.service.SetFavorite(ctx, f.b, tc.kind, tc.id, true); err != nil {
				t.Fatal(err)
			}
			empty := f.invoke(t, f.a, "list_favorites", map[string]any{"kind": tc.kind}, "")
			if strings.Contains(string(empty), tc.id) {
				t.Fatal("读到另一用户的收藏")
			}
			f.invoke(t, f.a, "set_favorite", map[string]any{"kind": tc.kind, "target_id": tc.id, "favorited": true}, "")
			listed := f.invoke(t, f.a, "list_favorites", map[string]any{"kind": tc.kind}, "")
			if !strings.Contains(string(listed), tc.id) {
				t.Fatalf("收藏未入库：%s", listed)
			}
			f.invoke(t, f.a, "set_favorite", map[string]any{"kind": tc.kind, "target_id": tc.id, "favorited": false}, "")
			own := f.invoke(t, f.a, "list_favorites", map[string]any{"kind": tc.kind}, "")
			other := f.invoke(t, f.b, "list_favorites", map[string]any{"kind": tc.kind}, "")
			if strings.Contains(string(own), tc.id) || !strings.Contains(string(other), tc.id) {
				t.Fatal("取消收藏的用户隔离错误")
			}
			f.invoke(t, f.a, "set_favorite", map[string]any{"kind": tc.kind, "target_id": uuid.New(), "favorited": true}, "not_found")
		})
	}
	t.Run("list_playlists", func(t *testing.T) {
		raw := f.invoke(t, f.a, "list_playlists", map[string]any{}, "")
		if strings.Contains(string(raw), foreign) {
			t.Fatal("读到别人歌单")
		}
		f.invoke(t, f.a, "list_playlists", map[string]any{"user_id": f.b}, "validation_failed")
	})
	t.Run("create_playlist", func(t *testing.T) {
		raw := f.invoke(t, f.a, "create_playlist", map[string]any{"name": "工具创建", "track_ids": f.tracks}, "")
		var body struct {
			Playlist struct {
				ID string `json:"id"`
			} `json:"playlist"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		f.invoke(t, f.a, "get_playlist", map[string]any{"playlist_id": body.Playlist.ID}, "")
		f.invoke(t, f.b, "get_playlist", map[string]any{"playlist_id": body.Playlist.ID}, "not_found")
		f.invoke(t, f.a, "create_playlist", map[string]any{"name": "不存在曲目", "track_ids": []string{uuid.New()}}, "not_found")
		f.invoke(t, f.a, "create_playlist", map[string]any{"name": "越权", "user_id": f.b}, "validation_failed")
	})
}

func TestPostgreSQLStewardC5PersonalNestedMissingTargets(t *testing.T) {
	f := newC5Fixture(t)
	_, foreignItems := f.playlist(t, f.b)
	for _, name := range []string{"add_playlist_tracks", "remove_playlist_track", "move_playlist_track"} {
		t.Run(name, func(t *testing.T) {
			for _, scenario := range []string{"missing", "other_user"} {
				if name == "add_playlist_tracks" && scenario == "other_user" {
					continue
				}
				t.Run(scenario, func(t *testing.T) {
					id, _ := f.playlist(t, f.a)
					in := map[string]any{"playlist_id": id, "expected_revision": 1}
					if name == "add_playlist_tracks" {
						in["track_ids"] = []string{uuid.New()}
					} else {
						in["item_id"] = uuid.New()
						if scenario == "other_user" {
							in["item_id"] = foreignItems[0]
						}
					}
					if name == "move_playlist_track" {
						in["action"] = "last"
					}
					f.invoke(t, f.a, name, in, "not_found")
				})
			}
		})
	}
}

func TestPostgreSQLRatingToolContracts(t *testing.T) {
	f := newC5Fixture(t)
	caller := tool.Caller{User: identity.User{ID: f.a, Role: "user"}}
	defs := f.registry.List(caller)
	for name, scope := range map[string]tool.Scope{"list_ratings": tool.ScopeRead, "set_rating": tool.ScopePersonal} {
		t.Run(name, func(t *testing.T) {
			idx := slices.IndexFunc(defs, func(d tool.Definition) bool { return d.Name == name })
			if idx < 0 {
				t.Fatalf("成员工具列表缺少 %s", name)
			}
			if def := defs[idx]; def.Scope != scope || def.MinRole != tool.RoleMember || def.Revertible || !def.Untrusted {
				t.Fatalf("工具合同错误：%+v", def)
			}
			caller := tool.Caller{User: identity.User{ID: f.a}}
			_, err := f.registry.Invoke(t.Context(), caller, name, json.RawMessage(`{}`))
			if failure, ok := err.(*tool.Error); !ok || failure.Code != tool.CodePermissionDenied {
				t.Fatalf("未经成员鉴权的工具返回 %v", err)
			}
		})
	}

	for _, input := range []map[string]any{
		{},
		{"kind": "albums"},
		{"kind": "album", "page_size": 51},
		{"kind": "album", "page": 0},
		{"kind": "album", "min_rating": 0},
		{"kind": "album", "min_rating": 3.5},
		{"kind": "album", "sort": "highest"},
		{"kind": "album", "user_id": f.b},
	} {
		f.invoke(t, f.a, "list_ratings", input, "validation_failed")
	}
	for _, input := range []map[string]any{
		{"kind": "track", "target_id": f.tracks[0]},
		{"kind": "track", "target_id": f.tracks[0], "rating": -1},
		{"kind": "track", "target_id": f.tracks[0], "rating": 6},
		{"kind": "track", "target_id": f.tracks[0], "rating": 4.5},
		{"kind": "track", "target_id": f.tracks[0], "rating": 5, "user_id": f.b},
	} {
		f.invoke(t, f.a, "set_rating", input, "validation_failed")
	}
	for _, kind := range []string{"track", "album", "artist"} {
		for _, rating := range []int{0, 5} {
			f.invoke(t, f.a, "set_rating", map[string]any{"kind": kind, "target_id": uuid.New(), "rating": rating}, "not_found")
		}
	}
}

func TestPostgreSQLRatingToolsSetListAndClear(t *testing.T) {
	f := newC5Fixture(t)
	const privateLyrics = "合成但不可公开的歌词"
	if _, err := f.db.Exec(`UPDATE tracks SET lyrics=$2, lyrics_source='vorbis_lyrics' WHERE id=$1`, f.tracks[0], privateLyrics); err != nil {
		t.Fatal(err)
	}
	artist := uuid.New()
	if _, err := f.db.Exec(`INSERT INTO artists(id,initial_name) VALUES($1,'工具合成艺人')`, artist); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO artist_profiles(artist_id,auto_display_name,effective_display_name,sort_key,initial_group,version)
		VALUES($1,'工具合成艺人','工具合成艺人','工具合成艺人','#',1)`, artist); err != nil {
		t.Fatal(err)
	}

	f.invoke(t, f.b, "set_rating", map[string]any{"kind": "track", "target_id": f.tracks[1], "rating": 5}, "")
	for _, tc := range []struct {
		kind, target, idKey string
	}{
		{"track", f.tracks[0], "track_id"},
		{"album", f.group, "group_id"},
		{"artist", artist, "id"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			set := func(user string, rating int) {
				t.Helper()
				input := map[string]any{"kind": tc.kind, "target_id": tc.target, "rating": rating}
				output := f.invoke(t, user, "set_rating", input, "")
				var got personal.RatingResult
				if err := json.Unmarshal(output, &got); err != nil {
					t.Fatal(err)
				}
				if got.TargetKind != tc.kind || got.TargetID != tc.target {
					t.Fatalf("改分目标错误：%s", output)
				}
				if rating == 0 {
					if got.Rating != nil || got.RatedAt != nil {
						t.Fatalf("清除评分的结果错误：%s", output)
					}
				} else if got.Rating == nil || *got.Rating != rating || got.RatedAt == nil {
					t.Fatalf("设置评分的结果错误：%s", output)
				}
			}
			list := func(user string, input map[string]any, size int, ratings ...int) []map[string]json.RawMessage {
				t.Helper()
				input["kind"] = tc.kind
				raw := f.invoke(t, user, "list_ratings", input, "")
				var page struct {
					Items      []map[string]json.RawMessage `json:"items"`
					Pagination personal.Pagination          `json:"pagination"`
				}
				if err := json.Unmarshal(raw, &page); err != nil {
					t.Fatal(err)
				}
				if page.Items == nil || len(page.Items) != len(ratings) || page.Pagination != (personal.Pagination{Page: 1, PageSize: size, Total: int64(len(ratings))}) {
					t.Fatalf("列表数量或分页错误：%s", raw)
				}
				for i, rating := range ratings {
					if string(page.Items[i]["rating"]) != strconv.Itoa(rating) {
						t.Fatalf("第 %d 项的评分错误：%s", i, raw)
					}
				}
				for _, secret := range []string{privateLyrics, `"lyrics"`, `"path"`, `"relative_path"`, "/synthetic/c5", "album/0.flac", f.b} {
					if strings.Contains(string(raw), secret) {
						t.Fatalf("列表输出泄露 %q：%s", secret, raw)
					}
				}
				return page.Items
			}
			set(f.b, 2)
			set(f.a, 5)
			items := list(f.a, map[string]any{"min_rating": 5, "sort": "recent", "page_size": 50}, 50, 5)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(items[0][tc.kind], &fields); err != nil {
				t.Fatal(err)
			}
			var id string
			if err := json.Unmarshal(fields[tc.idKey], &id); err != nil || id != tc.target {
				t.Fatalf("列表没有预期目标 %s，摘要 %s，错误 %v", tc.target, items[0][tc.kind], err)
			}
			if tc.kind == "track" {
				list(f.b, map[string]any{"min_rating": 5}, 20, 5)
			}
			set(f.a, 0)
			list(f.a, map[string]any{}, 20)
			wantOther := []int{2}
			if tc.kind == "track" {
				wantOther = []int{5, 2}
			}
			list(f.b, map[string]any{"min_rating": 2, "sort": "rating"}, 20, wantOther...)
		})
	}
}
