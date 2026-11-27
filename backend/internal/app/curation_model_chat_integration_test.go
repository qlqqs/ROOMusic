package app

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (f *schedulerFixture) startChat(t *testing.T) <-chan *httptest.ResponseRecorder {
	t.Helper()
	response := f.application.request(t, "POST", stewardBase+"/conversations", map[string]string{"mode": "steward"}, &f.admin, nil)
	if response.Code != 201 {
		t.Fatalf("新建对话 HTTP %d %s", response.Code, response.Body)
	}
	id := decodeJSONMap(t, response)["id"].(string)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- f.application.request(t, "POST", stewardBase+"/conversations/"+id+"/messages", map[string]string{"text": "核对合成发行"}, &f.admin, nil)
	}()
	return done
}

func finishSchedulerChat(t *testing.T, done <-chan *httptest.ResponseRecorder) {
	t.Helper()
	select {
	case response := <-done:
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"reason":"completed"`) {
			t.Fatalf("对话未正常完成 HTTP %d %s", response.Code, response.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("对话未结束")
	}
}

func TestPostgreSQL整理第一档禁用字段修改且对话可审查(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	_, revision := f.correct(t, f.a, 1, map[string]any{"label": setChange("人工厂牌")})
	item := f.pending(t, f.a)
	f.enable(t)
	q := f.next(t, false)
	if len(q.Tools) != 0 {
		t.Fatalf("第一档附带工具 %+v", q.Tools)
	}
	q.tool(t, "correct_release", schedulerCorrection(f.a, revision, "edition", setChange("模型版本")))
	schedulerWait(t, "无效工具答复释放项目", func() bool { return f.item(t, item.ID).EmptyCount == 1 })
	f.pause(t)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='edition'`, f.a)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM steward_reviews`)
	chat := f.startChat(t)
	f.next(t, false).tool(t, "correct_release", schedulerCorrection(f.a, revision, "edition", setChange("对话版本")))
	f.next(t, true).review(t, "approved")
	f.next(t, false).complete(t)
	finishSchedulerChat(t, chat)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM release_corrections WHERE release_id=$1 AND field='edition' AND text_value='对话版本'`, f.a)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM release_effective WHERE id=$1 AND edition='对话版本'`, f.a)
}

func TestPostgreSQLCurationModelBackgroundSlot(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	var chats []<-chan *httptest.ResponseRecorder
	var requests []schedulerRequest
	for range 3 {
		chats = append(chats, f.startChat(t))
		requests = append(requests, f.next(t, false))
	}
	item := f.pending(t, f.a)
	f.enable(t)
	model := f.next(t, false)
	if got := f.item(t, item.ID); got.Status != "in_progress" || !strings.Contains(string(model.Messages[len(model.Messages)-1].Content), item.ID) {
		t.Fatalf("三个对话并发时后台未领取 %+v", got)
	}
	b3RequireCount(t, f.application, 3, `SELECT count(*) FROM steward_conversations WHERE user_id=$1 AND kind='chat' AND status='running'`, f.admin.id)
	f.pause(t)
	model.complete(t)
	for i, q := range requests {
		q.complete(t)
		finishSchedulerChat(t, chats[i])
	}
}
