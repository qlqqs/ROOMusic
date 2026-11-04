package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library"
)

func TestScanFeedbackPresenterRedactsRootIdentity(t *testing.T) {
	index, id, name := 2, "root-id", "测试目录"
	for _, admin := range []bool{false, true} {
		for _, root := range []*library.RootProgress{nil, {Total: 3}, {Index: &index, Total: 3, ID: &id, Name: &name}} {
			raw, err := json.Marshal(presentScan(library.Scan{RootProgress: root, ReportStatus: "pending"}, admin))
			if err != nil {
				t.Fatal(err)
			}
			var dto map[string]any
			if err := json.Unmarshal(raw, &dto); err != nil {
				t.Fatal(err)
			}
			if root == nil {
				if dto["root_progress"] != nil {
					t.Fatalf("历史根应为空: %s", raw)
				}
				continue
			}
			projected := dto["root_progress"].(map[string]any)
			if projected["total"] != float64(3) {
				t.Fatalf("丢失根序号: %s", raw)
			}
			for _, key := range []string{"id", "name"} {
				_, exists := projected[key]
				if exists != admin {
					t.Fatalf("根身份权限错误: %s", raw)
				}
			}
		}
	}
}
