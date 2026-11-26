package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"
)

type metricRecordedValue struct {
	value, source string
	before, after json.RawMessage
}

type metricStoredLink struct {
	metricRecordedValue
	row, sourceID string
}

type metricLinkSubject struct{ kind, id string }

func metricStoredLinks(t *testing.T, app *integrationTestApplication) map[metricLinkSubject][]metricStoredLink {
	links := map[metricLinkSubject][]metricStoredLink{}
	metricReadRows(t, app, `SELECT l.id,l.object_type,l.object_id,l.source,l.external_id,
		concat_ws('/',o.origin->>'provider_kind',o.origin->>'provider',l.method),
		coalesce((SELECT x->'external_id' FROM jsonb_array_elements(o.before_state->'links') x
			WHERE x->>'object_id'=l.object_id::text AND x->>'object_type'=l.object_type AND coalesce(x->>'source',l.source)=l.source LIMIT 1),'null'::jsonb),
		coalesce((SELECT x->'external_id' FROM jsonb_array_elements(o.after_state->'links') x
			WHERE x->>'object_id'=l.object_id::text AND x->>'object_type'=l.object_type AND coalesce(x->>'source',l.source)=l.source LIMIT 1),'null'::jsonb)
		FROM external_links l LEFT JOIN operations o ON o.id=l.operation_id
		WHERE l.object_type IN ('release','release_group','release_group_match','track','artist') ORDER BY l.source,l.external_id`, func(rows *sql.Rows) {
		var key metricLinkSubject
		var link metricStoredLink
		requireCurationOK(t, rows.Scan(&link.row, &key.kind, &key.id, &link.sourceID, &link.value, &link.source, &link.before, &link.after))
		links[key] = append(links[key], link)
	})
	return links
}

func metricStoredFields(t *testing.T, app *integrationTestApplication) map[string]map[string]metricRecordedValue {
	fields := map[string]map[string]metricRecordedValue{}
	metricReadRows(t, app, `WITH vals(id,field,value,op) AS (
		SELECT release_id,field,coalesce(text_value,year_value::text,to_jsonb(list_value)::text,'null'),operation_id FROM release_auto_values
		UNION ALL SELECT track_id,field,text_value,operation_id FROM track_auto_values
	) SELECT v.id,v.field,v.value,concat_ws('/',o.origin->>'provider_kind',o.origin->>'provider'),
		o.before_state->'fields'->v.field->'effective',
		o.after_state->'fields'->v.field->'effective'
		FROM vals v JOIN operations o ON o.id=v.op`, func(rows *sql.Rows) {
		var id, field string
		var value metricRecordedValue
		requireCurationOK(t, rows.Scan(&id, &field, &value.value, &value.source, &value.before, &value.after))
		if fields[id] == nil {
			fields[id] = map[string]metricRecordedValue{}
		}
		fields[id][field] = value
	})
	return fields
}

func metricScoreValues(t *testing.T, app *integrationTestApplication, entries []metricEntry, scores []metricScore, artists []map[string]string, groupIDs, canonical map[string]string) {
	t.Helper()
	links := metricStoredLinks(t, app)
	fields := metricStoredFields(t, app)
	family := map[string][]metricStoredLink{}
	for obj, rows := range links {
		if obj.kind == "artist" {
			family[canonical[obj.id]] = append(family[canonical[obj.id]], rows...)
		}
	}

	// 在原始对象上核对错误写入；别名通过合法合并继承对应时，读取整个家族来判断完成。
	countedLinks := map[string]string{}
	checkLinks := func(i int, obj metricLinkSubject, target, want string) {
		direct := links[obj]
		for _, link := range direct {
			if link.sourceID == "musicbrainz" && link.value == want {
				continue
			}
			owner, seen := countedLinks[link.row]
			if !seen {
				owner = entries[i].sample.ID
				countedLinks[link.row] = owner
				scores[i].WrongLinks++
			}
			expected := want
			if expected == "" {
				expected = "不建立外部对应"
			}
			scores[i].issue(metricIssue{Kind: "wrong_link", Target: target, Field: obj.kind, Expected: expected, Actual: link.value, Source: link.source, CountedFor: owner, Before: link.before, After: link.after})
		}
		available := direct
		if obj.kind == "artist" {
			available = family[canonical[obj.id]]
		}
		if want != "" && len(direct) == 0 && !slices.ContainsFunc(available, func(l metricStoredLink) bool { return l.sourceID == "musicbrainz" && l.value == want }) {
			scores[i].issue(metricIssue{Kind: "missing_link", Target: target, Field: obj.kind, Expected: want, Actual: "未建立"})
		}
	}
	checkFields := func(i int, id, target string, want map[string]string, track bool) {
		actual := fields[id]
		for _, field := range slices.Sorted(maps.Keys(actual)) {
			value := actual[field]
			expected, required := want[field]
			if required && expected == value.value {
				continue
			}
			if !required {
				expected = "保留写入前的值"
			}
			scores[i].WrongFields++
			scores[i].issue(metricIssue{Kind: "wrong_field", Target: target, Field: field, Expected: expected, Actual: value.value, Before: value.before, After: value.after, Source: value.source, CountedFor: entries[i].sample.ID})
		}
		for _, field := range slices.Sorted(maps.Keys(want)) {
			if _, written := actual[field]; !written {
				q := `SELECT to_jsonb(e)->>$2::text FROM release_effective e WHERE e.id=$1`
				if track {
					q = `SELECT to_jsonb(e)->>$2::text FROM track_effective e WHERE e.id=$1`
				}
				var effective sql.NullString
				requireCurationOK(t, app.database.QueryRowContext(t.Context(), q, id, field).Scan(&effective))
				scores[i].issue(metricIssue{Kind: "missing_field", Target: target, Field: field, Expected: want[field], Actual: effective.String})
			}
		}
	}
	for i, e := range entries {
		sample, want := e.sample.ID, e.sample.Expect
		checkLinks(i, metricLinkSubject{"release", e.release}, sample, want.Release)
		checkLinks(i, metricLinkSubject{"release_group", groupIDs[e.release]}, sample, want.Group)
		groupMatch := want.GroupMatch
		// 同时允许版本或组级时，用同一组标注核对实际降级；完整指标仍按标注的首选版本评分。
		if groupMatch == "" && slices.Contains(want.Accept, "release_group") && len(links[metricLinkSubject{"release_group_match", e.release}]) > 0 {
			groupMatch = want.Group
		}
		checkLinks(i, metricLinkSubject{"release_group_match", e.release}, sample, groupMatch)
		checkFields(i, e.release, sample, want.Fields, false)
		for j, id := range e.tracks {
			var recording string
			if j < len(want.Recordings) {
				recording = want.Recordings[j]
			}
			track := e.sample.Local.Tracks[j]
			title := track.Title
			if title == "" {
				title = track.File
			}
			target := fmt.Sprintf("%s/track-%02d %s", sample, j+1, title)
			checkLinks(i, metricLinkSubject{"track", id}, target, recording)
			var expected map[string]string
			if j < len(want.TrackFields) {
				expected = want.TrackFields[j]
			}
			checkFields(i, id, target, expected, true)
		}
		for _, name := range slices.Sorted(maps.Keys(artists[i])) {
			id := artists[i][name]
			// 最终署名经过艺人重定向，同一拼写仍以扫描时记录的本地对象追溯。
			if original := e.artists[name]; original != "" {
				id = original
			}
			checkLinks(i, metricLinkSubject{"artist", id}, sample+"/artist "+name, want.Artists[name])
		}
		for _, name := range slices.Sorted(maps.Keys(want.Artists)) {
			if artists[i][name] == "" {
				scores[i].issue(metricIssue{Kind: "missing_artist", Target: sample + "/artist " + name, Field: "artist", Expected: want.Artists[name]})
			}
		}
	}
}
