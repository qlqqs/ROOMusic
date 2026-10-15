package app

import (
	"net/http"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func comparisonPath(groupID string) string {
	return "/api/v1/release-groups/" + groupID + "/comparison"
}

type comparisonResponse struct {
	GroupID          string  `json:"group_id"`
	DefaultReleaseID *string `json:"default_release_id"`
	ReleaseCount     int     `json:"release_count"`
	Releases         []struct {
		ID              string   `json:"id"`
		Title           string   `json:"title"`
		Year            *int     `json:"year"`
		MediumCount     int      `json:"medium_count"`
		TrackCount      int      `json:"track_count"`
		DurationSeconds *float64 `json:"duration_seconds"`
		Formats         []struct {
			TrackCount int `json:"track_count"`
		} `json:"formats"`
		CorrectedFields []string `json:"corrected_fields"`
	} `json:"releases"`
}

// 对比读取的权限与错误：未登录 401、标识无效 400、专辑不存在或没有可见版本 404；普通成员可以读，
// 读取不改变任何数据；它给出的成员顺序与默认版本和组详情一致，并随管理员设定的默认版本变化。
func TestPostgreSQLComparisonAPIPermissionsErrorsAndReads(t *testing.T) {
	environment := newDefaultEnvironment(t)
	application := environment.application

	requireAPIError(t, application.request(t, http.MethodGet, comparisonPath(environment.groupA), nil, nil, nil), 401, "unauthorized")
	requireAPIError(t, application.request(t, http.MethodGet, comparisonPath("not-a-uuid"), nil, &environment.ordinary, nil), 400, "invalid_id")
	requireAPIError(t, application.request(t, http.MethodGet, comparisonPath(uuid.New()), nil, &environment.ordinary, nil), 404, "not_found")
	requireAPIError(t, application.request(t, http.MethodGet, comparisonPath(environment.groupHiddenOnly), nil, &environment.ordinary, nil), 404, "not_found")
	// 对比只有读：其他方法不被接受。
	requireAPIError(t, application.request(t, http.MethodPost, comparisonPath(environment.groupA), map[string]any{}, &environment.admin, nil), 405, "method_not_allowed")

	operationsBefore := countRows(t, application, `SELECT COUNT(*) FROM operations`)
	var automatic comparisonResponse
	getJSON(t, application, &environment.ordinary, comparisonPath(environment.groupA), &automatic)
	if automatic.GroupID != environment.groupA || automatic.DefaultReleaseID != nil || automatic.ReleaseCount != 2 || len(automatic.Releases) != 2 ||
		automatic.Releases[0].ID != environment.a || automatic.Releases[1].ID != environment.b {
		t.Fatalf("automatic comparison = %+v; want album A's two versions with A first and no default", automatic)
	}
	first := automatic.Releases[0]
	if first.Title != "Album A" || first.Year == nil || *first.Year != 1998 || first.MediumCount != 1 || first.TrackCount != 1 || first.DurationSeconds != nil ||
		len(first.Formats) != 1 || first.Formats[0].TrackCount != 1 || first.CorrectedFields == nil || len(first.CorrectedFields) != 0 {
		t.Fatalf("version A in the comparison = %+v", first)
	}

	// 设定默认版本之后：默认版本在前，对比与组详情给出同样的顺序和默认版本。
	if response := environment.postDefault(t, &environment.admin, environment.groupA, uuid.New(), defaultBody(1, environment.b)); response.Code != http.StatusOK {
		t.Fatalf("set the default: HTTP %d: %s", response.Code, response.Body.String())
	}
	var withDefault comparisonResponse
	getJSON(t, application, &environment.ordinary, comparisonPath(environment.groupA), &withDefault)
	detail := environment.detail(t, environment.groupA)
	if withDefault.DefaultReleaseID == nil || *withDefault.DefaultReleaseID != environment.b || detail.DefaultReleaseID == nil || *detail.DefaultReleaseID != environment.b ||
		len(withDefault.Releases) != len(detail.Releases) || withDefault.Releases[0].ID != detail.Releases[0].ID || withDefault.Releases[1].ID != detail.Releases[1].ID {
		t.Fatalf("comparison %+v and group detail %+v disagree", withDefault, detail)
	}

	// 单版本的专辑（C）也可以读；B 已经被归入 A，原来的组没有任何成员，是 404。
	var single comparisonResponse
	getJSON(t, application, &environment.ordinary, comparisonPath(environment.groupC), &single)
	if single.GroupID != environment.groupC || single.ReleaseCount != 1 || len(single.Releases) != 1 || single.Releases[0].ID != environment.c || single.DefaultReleaseID != nil {
		t.Fatalf("single-version comparison = %+v", single)
	}
	requireAPIError(t, application.request(t, http.MethodGet, comparisonPath(environment.groupB), nil, &environment.ordinary, nil), 404, "not_found")

	// 读取没有产生任何操作记录：只有设定默认版本的那一条。
	if operations := countRows(t, application, `SELECT COUNT(*) FROM operations`); operations != operationsBefore+1 {
		t.Fatalf("operation records = %d, want %d (only the default-version change)", operations, operationsBefore+1)
	}
}
