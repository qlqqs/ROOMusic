package app

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestPostgreSQLMigrationVersionDevelopmentSample(t *testing.T) {
	app, _, _ := newSystemApplication(t, t.TempDir())
	admin := app.createUser(t, "admin")
	response := app.request(t, "GET", "/api/v1/system/version", nil, &admin, nil)
	raw, err := os.ReadFile("testdata/api-samples/system/version-dev.json")
	if err != nil {
		t.Fatal(err)
	}
	var sample struct {
		Status int            `json:"status"`
		Body   map[string]any `json:"body"`
	}
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != sample.Status || !reflect.DeepEqual(body, sample.Body) || !reflect.DeepEqual(body, map[string]any{"version": nil, "commit": nil}) {
		t.Fatalf("开发版API样本漂移：HTTP %d %s", response.Code, response.Body.String())
	}
}
