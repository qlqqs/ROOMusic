package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestPostgreSQLDSPPresetsOwnership(t *testing.T) {
	application := newIntegrationTestApplication(t)
	owner := application.createUser(t, "user")
	other := application.createUser(t, "user")
	const path = "/api/v1/me/dsp/peq-presets"
	original := json.RawMessage(`{"enabled":true,"preamp_db":-3,"bands":[{"type":"peaking","freq_hz":1000,"gain_db":6,"q":1,"channels":"all","enabled":true}],"source":{"kind":"import"}}`)
	replacement := json.RawMessage(`{"enabled":true,"preamp_db":-6,"bands":[{"type":"high_pass","freq_hz":80,"slope_db":24,"channels":[0,1],"enabled":true}]}`)
	type preset struct {
		ID        string            `json:"preset_id"`
		Name      string            `json:"name"`
		PEQ       zones.PEQSettings `json:"peq"`
		CreatedAt time.Time         `json:"created_at"`
		UpdatedAt time.Time         `json:"updated_at"`
	}
	readPreset := func(response *httptest.ResponseRecorder, status int, id, name string, raw json.RawMessage) preset {
		t.Helper()
		if response.Code != status {
			t.Fatalf("预设响应状态=%d，期望=%d：%s", response.Code, status, response.Body.String())
		}
		var got preset
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		var want zones.PEQSettings
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if !uuid.Valid(got.ID) || id != "" && got.ID != id || got.Name != name || !reflect.DeepEqual(got.PEQ, want) {
			t.Fatalf("预设内容不符：%s", response.Body.String())
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() || got.UpdatedAt.Before(got.CreatedAt) {
			t.Fatalf("预设时间无效：%+v", got)
		}
		return got
	}
	assertList := func(actor *integrationTestUser, want ...preset) {
		t.Helper()
		response := application.request(t, http.MethodGet, path, nil, actor, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("读取预设列表：%d %s", response.Code, response.Body.String())
		}
		var got struct {
			Items []preset `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Items == nil || len(got.Items) != len(want) {
			t.Fatalf("预设列表不符，期望 %d 项：%s", len(want), response.Body.String())
		}
		for i := range want {
			if !reflect.DeepEqual(got.Items[i], want[i]) {
				t.Fatalf("持久化预设不符：实际=%+v，期望=%+v", got.Items[i], want[i])
			}
		}
	}

	assertList(&owner)
	created := readPreset(application.request(t, http.MethodPost, path,
		map[string]any{"name": "试听均衡", "peq": original}, &owner, nil), http.StatusCreated, "", "试听均衡", original)
	assertList(&owner, created)
	assertList(&other)
	itemPath := path + "/" + created.ID
	for _, attempt := range []struct {
		method string
		body   any
	}{
		{http.MethodPatch, map[string]any{"name": "他人改名", "peq": replacement}},
		{http.MethodDelete, nil},
	} {
		response := application.request(t, attempt.method, itemPath, attempt.body, &other, nil)
		requireAPIError(t, response, http.StatusNotFound, "not_found")
		assertList(&owner, created)
		assertList(&other)
	}

	renamed := readPreset(application.request(t, http.MethodPatch, itemPath,
		map[string]any{"name": "客厅均衡"}, &owner, nil), http.StatusOK, created.ID, "客厅均衡", original)
	if !renamed.CreatedAt.Equal(created.CreatedAt) {
		t.Fatal("改名改变了创建时间")
	}
	assertList(&owner, renamed)
	overwritten := readPreset(application.request(t, http.MethodPatch, itemPath,
		map[string]any{"peq": replacement}, &owner, nil), http.StatusOK, created.ID, "客厅均衡", replacement)
	if !overwritten.CreatedAt.Equal(created.CreatedAt) {
		t.Fatal("覆盖内容改变了创建时间")
	}
	assertList(&owner, overwritten)
	assertList(&other)

	response := application.request(t, http.MethodDelete, itemPath, nil, &owner, nil)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("删除预设应返回空 204：%d %s", response.Code, response.Body.String())
	}
	assertList(&owner)
	assertList(&other)
}
