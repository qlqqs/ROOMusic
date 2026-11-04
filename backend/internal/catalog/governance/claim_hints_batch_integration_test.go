package governance

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLScanFeedbackClaimHintsBatch(t *testing.T) {
	f := newCorrectionFixture(t)
	ctx := context.Background()
	newer := f.versionOf("新版本", 2020)
	f.exec(`UPDATE releases SET relative_directory='dir' WHERE id=$1::uuid`, newer.releaseID)
	f.trackAt(newer, "dir/new-01.flac", "present")
	var olds []placementRelease
	for i := 0; i < 5; i++ {
		old := f.hiddenRelease(fmt.Sprintf("旧版本 %d", i))
		f.mustReviewDirect(old.releaseID)
		f.exec(`UPDATE tracks SET relative_path=$2,observed_at=TIMESTAMPTZ '2026-06-01T00:00:00Z'+$3::interval WHERE medium_id=$1::uuid`, old.mediumID, fmt.Sprintf("gone/%d.flac", i), fmt.Sprintf("%d days", i))
		if i%2 == 0 {
			f.exec(`UPDATE releases SET relative_directory='dir' WHERE id=$1::uuid`, old.releaseID)
		} else {
			f.exec(`UPDATE release_grouping_evidence SET source_refs='["dir/new-01.flac"]'::jsonb WHERE release_id=$1::uuid`, old.releaseID)
		}
		olds = append(olds, old)
	}
	sibling := f.versionOf("旧版同组的另一个版本", 2010)
	f.exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, olds[4].groupID, sibling.releaseID)
	other := f.versionOf("无关新版本", 2021)
	f.exec(`UPDATE releases SET relative_directory='elsewhere' WHERE id=$1::uuid`, other.releaseID)
	unknown := uuid.New()
	ids := []string{newer.releaseID, other.releaseID, olds[0].releaseID, unknown, newer.releaseID}
	batch, err := f.governance.LoadClaimHintsBatch(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	single, err := f.governance.LoadClaimHints(ctx, newer.releaseID)
	if err != nil || !reflect.DeepEqual(batch[newer.releaseID], single) || len(single) != 3 || single[0].ID != olds[4].releaseID {
		t.Fatalf("batch = %+v; single = %+v, %v", batch, single, err)
	}
	for _, id := range []string{other.releaseID, olds[0].releaseID, unknown} {
		if batch[id] == nil || len(batch[id]) != 0 {
			t.Fatalf("empty hints %s = %#v", id, batch[id])
		}
	}
	// 一致性快照中的目录摘要与提示不随另一连接完成认领而撕裂；新请求反映认领，撤销后恢复。
	scanID := uuid.New()
	f.exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'succeeded',NOW())`, scanID)
	f.exec(`INSERT INTO scan_added_releases(scan_run_id,release_id,group_id_at_creation,added_at,title_at_creation,artist_at_creation)
  VALUES($1::uuid,$2::uuid,$3::uuid,NOW(),'扫描标题','扫描艺人')`, scanID, newer.releaseID, newer.groupID)
	tx, err := f.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshotPage, err := catalog.NewStore(tx).ListScanAddedAlbums(ctx, scanID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	scenario := &claimScenario{fixture: f, source: olds[4], target: newer}
	_, claim := scenario.mustClaim(t)
	snapshotHints, err := NewStore(tx).LoadClaimHintsBatch(ctx, ids)
	if err != nil || !reflect.DeepEqual(snapshotHints, batch) {
		t.Fatalf("torn snapshot hints: %+v, %v", snapshotHints, err)
	}
	afterPage, err := catalog.NewStore(f.database).ListScanAddedAlbums(ctx, scanID, 1, 20)
	if err != nil || afterPage.Items[0].GroupIDAtScan != newer.groupID || *afterPage.Items[0].CurrentGroupID == *snapshotPage.Items[0].CurrentGroupID {
		t.Fatalf("claim dynamic page = %+v, %v", afterPage, err)
	}
	after, err := f.governance.LoadClaimHintsBatch(ctx, ids)
	if err != nil || len(after[newer.releaseID]) != 3 || after[newer.releaseID][0].ID != olds[3].releaseID {
		t.Fatalf("claimed hints = %+v, %v", after, err)
	}
	scenario.mustRevertClaim(t, claim.OperationID)
	restored, err := f.governance.LoadClaimHintsBatch(ctx, ids)
	if err != nil || !reflect.DeepEqual(restored, batch) {
		t.Fatalf("reverted hints = %+v, %v", restored, err)
	}
	// 旧版文件恢复之后不再待认领；历史新增项不受影响。
	f.exec(`UPDATE tracks SET source_status='present' WHERE medium_id=$1::uuid`, olds[4].mediumID)
	visibleAgain, err := f.governance.LoadClaimHintsBatch(ctx, ids)
	if err != nil || len(visibleAgain[newer.releaseID]) != 3 || visibleAgain[newer.releaseID][0].ID != olds[3].releaseID {
		t.Fatalf("restored source hints = %+v, %v", visibleAgain, err)
	}
}

func TestPostgreSQLScanFeedbackClaimHintsBeyondForwardLimit(t *testing.T) {
	f := newCorrectionFixture(t)
	old := f.hiddenRelease("旧版")
	f.exec(`UPDATE releases SET relative_directory='dir' WHERE id=$1::uuid`, old.releaseID)
	f.mustReviewDirect(old.releaseID)
	ids := []string{}
	for i := 0; i < 7; i++ {
		r := f.versionOf(fmt.Sprintf("新版 %d", i), 2020)
		f.exec(`UPDATE releases SET relative_directory='dir' WHERE id=$1::uuid`, r.releaseID)
		ids = append(ids, r.releaseID)
	}
	batch, err := f.governance.LoadClaimHintsBatch(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if len(batch[id]) != 1 || batch[id][0].ID != old.releaseID {
			t.Fatalf("reverse hint missing for %s: %+v", id, batch)
		}
	}
}
