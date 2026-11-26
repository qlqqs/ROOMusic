package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type metricRequests struct {
	Count int     `json:"count"`
	Mean  float64 `json:"mean"`
	Max   int     `json:"max"`
}

func (r *metricRequests) add(n, samples int) {
	r.Count += n
	r.Max = max(r.Max, n)
	r.Mean = float64(r.Count) / float64(samples)
}

type metricSummary struct {
	Shape            string                       `json:"shape"`
	Samples          int                          `json:"samples"`
	Requests         metricRequests               `json:"requests"`
	Endpoints        map[string]metricRequests    `json:"endpoints"`
	MBSeconds        int                          `json:"estimated_musicbrainz_seconds"`
	Entered          int                          `json:"entered_model_items"`
	DecidableEntered int                          `json:"program_decidable_entered_samples"`
	Model            map[string]metricsModelUsage `json:"model_usage"`
	Complete         int                          `json:"completed_samples"`
	CompletionRate   float64                      `json:"completion_rate"`
	Pending          int                          `json:"open_pending_items"`
	Exceptions       int                          `json:"open_exceptions"`
	WrongLinks       int                          `json:"wrong_links"`
	WrongMerges      int                          `json:"wrong_merges"`
	WrongFields      int                          `json:"wrong_fields"`
}

func newMetricSummary(shape string) *metricSummary {
	return &metricSummary{Shape: shape, Endpoints: map[string]metricRequests{}, Model: map[string]metricsModelUsage{
		"pending_step": {}, "review": {}, "artist_identity": {}, "other": {}, "patrol": {}, "local_cleanup": {},
	}}
}

func (r *metricSummary) add(e metricEntry, s metricScore, model map[string]metricsModelUsage) {
	r.Samples++
	total := 0
	for endpoint, n := range e.requests {
		v := r.Endpoints[endpoint]
		v.add(n, r.Samples)
		r.Endpoints[endpoint] = v
		if strings.HasPrefix(endpoint, "musicbrainz/") {
			r.MBSeconds += n
		}
		total += n
	}
	r.Requests.add(total, r.Samples)
	for category, usage := range model {
		sum := r.Model[category]
		sum.Calls += usage.Calls
		sum.InputTokens += usage.InputTokens
		sum.OutputTokens += usage.OutputTokens
		r.Model[category] = sum
	}
	if s.Complete {
		r.Complete++
	}
	r.CompletionRate = float64(r.Complete) / float64(r.Samples)
	if e.sample.ProgramDecidable && s.Entered > 0 {
		r.DecidableEntered++
	}
	r.Entered += s.Entered
	r.Pending += s.OpenPending
	r.Exceptions += s.OpenExceptions
	r.WrongLinks += s.WrongLinks
	r.WrongMerges += s.WrongMerges
	r.WrongFields += s.WrongFields
}

func metricReport(t *testing.T, app *integrationTestApplication, entries []metricEntry, model *metricsModel) {
	t.Helper()
	model.mu.Lock()
	scores := metricScoreEntries(t, app, entries, model.routes)
	model.mu.Unlock()
	usage := model.usage()
	details := make([]metricDetail, 0, len(entries))
	groups := map[string]*metricSummary{}
	total := newMetricSummary("合计")
	for i, e := range entries {
		detail := scores[i].metricDetail
		details = append(details, detail)
		row, err := json.Marshal(detail)
		requireCurationOK(t, err)
		t.Logf("样本明细 %s", row)
		r := groups[e.sample.Shape]
		if r == nil {
			r = newMetricSummary(e.sample.Shape)
			groups[e.sample.Shape] = r
		}
		r.add(e, scores[i], usage[e.sample.ID])
		total.add(e, scores[i], usage[e.sample.ID])
	}
	var shapes []*metricSummary
	for _, shape := range slices.Sorted(maps.Keys(groups)) {
		shapes = append(shapes, groups[shape])
	}
	var log strings.Builder
	log.WriteString("\n形态 | 样本 | 请求均值/最多 | MB 秒 | 进模型项/程序可定样本 | 调用/token | 完成 | 待定/例外 | 错连/并/改\n")
	for _, r := range append(slices.Clone(shapes), total) {
		for key, v := range r.Endpoints {
			v.Mean = float64(v.Count) / float64(r.Samples)
			r.Endpoints[key] = v
		}
		calls, tokens := 0, 0
		for _, u := range r.Model {
			calls += u.Calls
			tokens += u.InputTokens + u.OutputTokens
		}
		fmt.Fprintf(&log, "%s | %d | %.2f/%d | %d | %d/%d | %d/%d | %d/%d | %d/%d | %d/%d/%d\n", r.Shape, r.Samples, r.Requests.Mean, r.Requests.Max, r.MBSeconds, r.Entered, r.DecidableEntered, calls, tokens, r.Complete, r.Samples, r.Pending, r.Exceptions, r.WrongLinks, r.WrongMerges, r.WrongFields)
	}
	t.Log(log.String())
	t.Logf("来源/端点汇总（请求数、均值、最多）%+v；模型分类（调用、输入与输出 token）%+v", total.Endpoints, total.Model)
	if total.DecidableEntered != 0 {
		t.Errorf("%d 个程序可定样本进入了模型识别", total.DecidableEntered)
	}
	peaks := model.checkItemBudgets(t)
	t.Logf("D16 档位的逐项用量峰值：%+v", peaks)
	if path := os.Getenv("ROOMUSIC_CURATION_METRICS_OUT"); path != "" {
		data, err := json.MarshalIndent(map[string]any{"model": model.mode, "token_estimate": "请求与 SSE 响应分别按字节数除以 4 向下取整", "acoustid_enabled": false, "samples": details, "by_shape": shapes, "total": total, "item_usage_peaks": peaks}, "", "  ")
		requireCurationOK(t, err)
		requireCurationOK(t, os.MkdirAll(filepath.Dir(path), 0755))
		requireCurationOK(t, os.WriteFile(path, append(data, '\n'), 0644))
	}
}
