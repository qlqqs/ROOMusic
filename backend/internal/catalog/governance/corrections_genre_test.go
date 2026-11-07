package governance

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

func TestParseCorrectionGenreChanges(t *testing.T) {
	parsed, err := ParseCorrectionChanges(map[string]RawCorrectionChange{
		"genre": {Mode: "set", Value: json.RawMessage(`[" Jazz ","ＰＯＰ","pop",""," Folk, World, & Country ","R&B/Soul","Rock; Metal"]`)},
	})
	want := []string{"Jazz", "ＰＯＰ", "Folk, World, & Country", "R&B/Soul", "Rock; Metal"}
	if err != nil || !slices.Equal(parsed["genre"].Genres, want) {
		t.Fatalf("规范化流派 = %v, %v；期望 %v", parsed, err, want)
	}

	overLimit := make([]string, 21)
	for i := range overLimit {
		overLimit[i] = fmt.Sprintf("流派%d", i)
	}
	overLimitJSON, _ := json.Marshal(overLimit)
	for _, test := range []struct{ name, value, message string }{
		{"非数组", `"Jazz"`, "流派必须是文本列表"},
		{"非文本项", `["Jazz",null]`, "流派必须是文本列表"},
		{"空列表", `[]`, "流派至少填一个；要清空请选择留空"},
		{"空白列表", `[" ","\t"]`, "流派至少填一个；要清空请选择留空"},
		{"超过数量", string(overLimitJSON), "流派最多 20 个"},
		{"控制字符", `["Ja\u0007zz"]`, "流派含有不允许的字符"},
		{"超过字节", `["` + strings.Repeat("x", 4097) + `"]`, "流派超过 4096 字节"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseCorrectionChanges(map[string]RawCorrectionChange{
				"genre": {Mode: "set", Value: json.RawMessage(test.value)},
			})
			var failure *operations.Failure
			if !errors.As(err, &failure) || failure.Code != "invalid_correction" || failure.Message != test.message {
				t.Fatalf("错误 = %v；期望 invalid_correction / %s", err, test.message)
			}
		})
	}
	if _, err := ParseCorrectionChanges(map[string]RawCorrectionChange{
		"genre": {Mode: "set", Value: json.RawMessage(`["` + strings.Repeat("x", 4096) + `"]`)},
	}); err != nil {
		t.Fatalf("4096 字节的流派被拒绝：%v", err)
	}
}

func TestCorrectionFingerprintGenreList(t *testing.T) {
	fingerprint := func(raw string) string {
		t.Helper()
		changes, err := ParseCorrectionChanges(map[string]RawCorrectionChange{
			"genre": {Mode: "set", Value: json.RawMessage(raw)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return (CorrectionRequest{ReleaseID: "11111111-1111-4111-8111-111111111111", ExpectedRevision: 1, Changes: changes}).Fingerprint()
	}
	base := fingerprint(`["Jazz","Pop"]`)
	if base != fingerprint(`[" Jazz ","Pop","pop",""]`) {
		t.Fatal("等价的规范化列表应有相同指纹")
	}
	if base == fingerprint(`["Pop","Jazz"]`) || base == fingerprint(`["Jazz","Rock"]`) {
		t.Fatal("列表顺序或内容变化必须改变指纹")
	}
}

func TestRestoredIntentGenre(t *testing.T) {
	field, _ := correctableFieldNamed("genre")
	for _, test := range []struct {
		value any
		want  []string
	}{
		{"Jazz", []string{"Jazz"}},
		{[]any{"Jazz", "Pop"}, []string{"Jazz", "Pop"}},
	} {
		intent, err := restoredIntent(field, &catalog.CorrectionIntent{Mode: "set", Value: test.value})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(intent.Value.([]string), test.want) {
			t.Fatalf("恢复流派 = %v；期望 %v", intent.Value, test.want)
		}
	}
	state := correctionRecordState{
		Fields:    map[string]*catalog.CorrectionIntent{"genre": {Mode: "set", Value: "Jazz"}},
		Effective: map[string]any{"genre": "Pop"},
	}
	if !state.normalizeGenres() || !slices.Equal(state.Fields["genre"].Value.([]string), []string{"Jazz"}) ||
		!slices.Equal(state.Effective["genre"].([]string), []string{"Pop"}) {
		t.Fatalf("旧流派记录未转换成列表：%+v", state)
	}
}

func TestGenreCorrectionIntentComparison(t *testing.T) {
	field, _ := correctableFieldNamed("genre")
	previous := catalog.CorrectionIntent{Mode: "set", Value: []string{"Jazz", "Pop"}}
	for _, test := range []struct {
		genres []string
		same   bool
	}{
		{[]string{"Jazz", "Pop"}, true},
		{[]string{"Pop", "Jazz"}, false},
		{[]string{"Jazz"}, false},
	} {
		next := nextIntent(field, CorrectionChange{Mode: "set", Genres: test.genres})
		if sameIntent(field, previous, true, next) != test.same {
			t.Errorf("比较 %v 与 %v，应相同=%v", previous.Value, test.genres, test.same)
		}
	}
}
