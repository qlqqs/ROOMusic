package app

import (
	"testing"
	"time"
)

// 当前行在退避后会清空会话编号。只在评分用的隔离库中记录最近一次领取，供结束时核对。
func metricObserveClaims(t *testing.T, app *integrationTestApplication) {
	t.Helper()
	mustExec(t, app, `CREATE TABLE metric_last_claim (
		item_id uuid PRIMARY KEY, conversation_id uuid NOT NULL, route text, attempts integer NOT NULL, observed_at timestamptz NOT NULL)`)
	mustExec(t, app, `CREATE FUNCTION metric_record_claim() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		INSERT INTO metric_last_claim VALUES (NEW.id, NEW.conversation_id,
			CASE WHEN NEW.kind='artist_identity' AND coalesce(NEW.route,'')='' THEN 'web' ELSE NEW.route END,
			NEW.attempts, clock_timestamp())
		ON CONFLICT (item_id) DO UPDATE SET conversation_id=excluded.conversation_id, route=excluded.route,
			attempts=excluded.attempts, observed_at=excluded.observed_at;
		RETURN NEW;
	END $$`)
	mustExec(t, app, `CREATE TRIGGER metric_record_claim AFTER UPDATE OF status ON curation_pending_items
		FOR EACH ROW WHEN (OLD.status <> 'in_progress' AND NEW.status='in_progress')
		EXECUTE FUNCTION metric_record_claim()`)
}

type metricSourceRetry struct {
	Target   string    `json:"target_id"`
	Item     string    `json:"item_id"`
	Kind     string    `json:"kind"`
	Route    string    `json:"route"`
	Attempts int       `json:"attempts"`
	RetryAt  time.Time `json:"next_attempt_at"`
}

func metricSourceRetries(t *testing.T, app *integrationTestApplication, at time.Time) map[string]metricSourceRetry {
	t.Helper()
	rows, err := app.database.QueryContext(t.Context(), `SELECT coalesce(p.target_id,p.artist_id),p.id,p.kind,p.route,p.attempts,p.next_attempt_at
		FROM curation_pending_items p JOIN metric_last_claim c ON c.item_id=p.id
		WHERE p.status='waiting_route' AND p.route IN ('lookup','web') AND p.route=c.route AND p.attempts=c.attempts
		  AND p.next_attempt_at>$1 AND EXISTS (
			SELECT FROM steward_tool_calls t JOIN steward_messages m ON m.id=t.message_id
			WHERE m.conversation_id=c.conversation_id AND m.created_at>=c.observed_at
			  AND t.status='failed' AND t.error_code IN ('source_unavailable','rate_limited'))`, at)
	requireCurationOK(t, err)
	defer rows.Close()
	waits := map[string]metricSourceRetry{}
	for rows.Next() {
		var r metricSourceRetry
		requireCurationOK(t, rows.Scan(&r.Target, &r.Item, &r.Kind, &r.Route, &r.Attempts, &r.RetryAt))
		waits[r.Item] = r
	}
	requireCurationOK(t, rows.Err())
	return waits
}

const metricWaitingSource = "waiting_source_retry"

func (r metricSourceRetry) matching() bool {
	return r.Kind == "release_match" || r.Kind == "recording_match"
}
