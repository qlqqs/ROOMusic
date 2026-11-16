package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/steward/provider"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestC8ReplayModelBindingAndTextFallback(t *testing.T) {
	origin := providerOrigin{Protocol: provider.ProtocolMessages, Endpoint: "https://synthetic.invalid", Model: "model-a"}
	content := textMessage(provider.RoleAssistant, "保留文字")
	content.Origin = origin
	content.ProviderBlocks = []provider.OpaqueBlock{{Protocol: provider.ProtocolMessages, Data: json.RawMessage(`{"type":"text","text":"opaque-only"}`)}}
	input, err := messageInput(content, provider.Usage{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"model-a", "model-b", ""} {
		t.Run("current_"+model, func(t *testing.T) {
			target := origin
			target.Model = model
			replay, err := replayMessagesFor([]Message{{Role: input.Role, Content: input.Content, ProviderBlocks: input.ProviderBlocks}}, target)
			if err != nil {
				t.Fatal(err)
			}
			if len(replay) != 1 || replay[0].Content[0].Text != "保留文字" || (len(replay[0].ProviderBlocks) > 0) != (model == origin.Model) {
				t.Fatalf("回放=%+v", replay)
			}
		})
	}
	// 旧数据没有模型归属时，同端点也只能恢复文字。
	content.Origin.Model = ""
	legacy, err := messageInput(content, provider.Usage{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := replayMessagesFor([]Message{{Role: legacy.Role, Content: legacy.Content, ProviderBlocks: legacy.ProviderBlocks}}, origin)
	if err != nil || len(replay[0].ProviderBlocks) != 0 || replay[0].Content[0].Text != "保留文字" {
		t.Fatalf("旧数据回放=%+v 错误=%v", replay, err)
	}
}

func TestC8ProviderBlocksSerializedSizeBoundary(t *testing.T) {
	for _, delta := range []int{-1, 0, 1} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			content := textMessage(provider.RoleAssistant, "保留文字")
			content.Origin = providerOrigin{Protocol: provider.ProtocolMessages, Endpoint: "https://synthetic.invalid", Model: "model-a"}
			content.ProviderBlocks = []provider.OpaqueBlock{{Protocol: provider.ProtocolMessages, Data: json.RawMessage(`{"text":""}`)}}
			base, _ := json.Marshal(providerHistory{providerOrigin: content.Origin, Blocks: content.ProviderBlocks})
			content.ProviderBlocks[0].Data = json.RawMessage(`{"text":"` + strings.Repeat("x", (256<<10)-len(base)+delta) + `"}`)
			input, err := messageInput(content, provider.Usage{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if (len(input.ProviderBlocks) > 0) != (delta <= 0) {
				t.Fatalf("序列化边界 delta=%d，保存=%d", delta, len(input.ProviderBlocks))
			}
			if delta <= 0 && len(input.ProviderBlocks) != (256<<10)+delta {
				t.Fatalf("实际封装大小=%d", len(input.ProviderBlocks))
			}
			if !bytes.Contains(input.Content, []byte("保留文字")) || bytes.Contains(input.Content, []byte(strings.Repeat("x", 100))) {
				t.Fatal("正文含原始块或丢失文字")
			}
		})
	}
}

func TestC8RuntimeSearchBoundsAndFinishedLog(t *testing.T) {
	var logs bytes.Buffer
	var values []provider.Citation
	for i := 0; i < 30; i++ {
		values = append(values, provider.Citation{URL: fmt.Sprintf("https://example.org/%d", i), Title: strings.Repeat("题", 201)})
	}
	p := c7ProviderFunc(func(context.Context, provider.Request) (provider.Stream, error) {
		return &fakeStream{events: []provider.Event{
			{Type: provider.EventSearch, Search: &provider.Search{SearchID: "s", Query: strings.Repeat("私密查询", 101), Results: values, Status: "completed"}},
			{Type: provider.EventCitations, Citations: values[:15]}, {Type: provider.EventCitations, Citations: values[15:]},
			{Type: provider.EventText, Text: "回答"}, modelDone(provider.FinishNormal),
		}}, nil
	})
	store := &c8RunStore{}
	searchCount, citationCount := 0, 0
	err := NewRunner(store, tool.NewRegistry(), slog.New(slog.NewJSONHandler(&logs, nil))).Run(context.Background(), runParams(p), func(e Event) {
		if e.Search != nil {
			searchCount++
			if len(e.Search.Results) != 10 || len([]rune(e.Search.Query)) != 400 || len([]rune(e.Search.Results[0].Title)) != 200 {
				t.Errorf("搜索越界=%+v", e.Search)
			}
		}
		if e.Type == EventCitations {
			citationCount++
			if len(e.Citations) != 20 {
				t.Errorf("引用数量=%d", len(e.Citations))
			}
		}
	})
	if err != nil || searchCount != 1 || citationCount != 1 {
		t.Fatalf("运行=%v 搜索=%d 引用=%d", err, searchCount, citationCount)
	}
	var stored messageContent
	if err := json.Unmarshal(store.detail.Messages[1].Content, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Searches) != 1 || len(stored.Searches[0].Results) != 10 || len(stored.Citations) != 20 {
		t.Fatal("落库投影越界")
	}
	found := 0
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["event"] == "steward.search.finished" {
			found++
			if row["message"] != "管家模型搜索结束" || row["module"] != "steward" {
				t.Fatalf("日志=%v", row)
			}
		}
	}
	if found != 1 || strings.Contains(logs.String(), "私密查询") || strings.Contains(logs.String(), "example.org") {
		t.Fatal("搜索日志缺失或泄漏资料")
	}
}
