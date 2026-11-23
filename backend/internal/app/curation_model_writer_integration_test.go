package app

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLCurationWriterConfirmedBy(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	ctx := t.Context()
	confirmer := f.application.createUser(t, "admin")
	disabled := f.application.createUser(t, "admin")
	mustExec(t, f.application, `UPDATE users SET disabled_at=now() WHERE id=$1`, disabled.id)
	conversation := modelConversation(t, f)
	batch := f.batch(t, "model")

	for _, tc := range []struct {
		name, actor, batch, code string
		revision                 int64
		status                   int
	}{
		{name: "confirmed_by_other_admin", actor: confirmer.id, revision: 1},
		{name: "batch_rejected", actor: confirmer.id, batch: batch, revision: 1, status: 400, code: "invalid_input"},
		{name: "disabled_admin", actor: disabled.id, revision: 1, status: 409, code: "curation_disabled"},
		{name: "member", actor: f.ordinary.id, revision: 1, status: 409, code: "curation_disabled"},
		{name: "stale_revision", actor: confirmer.id, revision: 2, status: 409, code: "revision_conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := autoRequest(f.a, "synthetic", tc.batch, map[string]any{"label": tc.name})
			req.Source.Kind = "model"
			req.ConfirmedBy, req.ConversationID, req.ExpectedRevision = tc.actor, conversation, tc.revision
			result, err := f.writer.Apply(ctx, req)
			if tc.status != 0 {
				curationCRequireProblem(t, err, tc.status, tc.code)
				f.textOwner(t, "label", "confirmed_by_other_admin",
					programString(t, f.application, `SELECT id FROM operations WHERE target_id=$1 AND operation_type='release_auto_correction'`, f.a))
				return
			}
			requireCurationOK(t, err)
			f.textOwner(t, "label", tc.name, result.OperationID)
			b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE id=$1 AND actor_id=$2 AND batch_id IS NULL
				AND origin->>'conversation_id'=$3`, result.OperationID, tc.actor, conversation)
		})
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE target_id=$1 AND operation_type='release_auto_correction'`, f.a)
}

func TestPostgreSQLCurationWriterModelLinkRevert(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	sourceTrack, remote := f.tagged(t)
	modelLink := curationCLink("track", sourceTrack, uuid.New())
	req := curationCLinkRequest(f.a, f.batch(t, "model"), modelLink)
	req.ExternalSource = "musicbrainz"
	req.Source = curation.Source{Kind: "model", Provider: curation.NormalizeProvider("Synthetic/Model"), Basis: "合成模型判断"}
	got := curationCApplyLinks(t, f.curationFixture, req)
	modelRecord, err := operations.Load(t.Context(), f.application.database, got.OperationID)
	requireCurationOK(t, err)
	requireExternalJSON(t, modelRecord.Origin, operations.Origin{
		Source: "curation", ProviderKind: "model", Provider: "synthetic-model", Basis: req.Source.Basis,
	})
	modelAfter := curationCChanges(req, []curation.LinkIntent{modelLink})
	modelAfter["source"] = "musicbrainz"
	modelAfter["provider"] = "synthetic-model"
	requireExternalJSON(t, modelRecord.After, modelAfter)
	undo := revertExternal(t, f.curationFixture, got.OperationID, f.a, 1)
	curationCRequireLink(t, f.curationFixture, modelLink, "")
	curationCRequireMarker(t, f.curationFixture, "track", sourceTrack, "external_link", "musicbrainz\x00"+modelLink.ExternalID,
		undo, map[string]string{"source": "musicbrainz", "external_id": modelLink.ExternalID})

	f.run(t)
	f.assertRelease(t, f.a, "matched", "tag_id")
	programOp := f.linkOwner(t, f.a)
	programRecord, err := operations.Load(t.Context(), f.application.database, programOp)
	requireCurationOK(t, err)
	releaseLink := curationCLink("release", f.a, remote.ID)
	releaseLink.ExternalGroupID = remote.Group.ID
	programRequest := curationCLinkRequest(f.a, "", releaseLink)
	programRequest.Method, programRequest.Confidence = "tag_id", "certain"
	programRequest.External = curation.ExternalSummary{Title: remote.Title, Artist: "Artist A", Date: remote.Date, DiscCount: 1, TrackCount: 1}
	programAfter := curationCChanges(programRequest, []curation.LinkIntent{
		releaseLink,
		curationCLink("release_group", f.groupA, remote.Group.ID),
		curationCLink("track", sourceTrack, remote.Media[0].Tracks[0].RecordingID),
	})
	programAfter["provider"] = "musicbrainz"
	requireExternalJSON(t, programRecord.After, programAfter)
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE id=$1 AND origin->>'provider_kind'='source'
		AND origin->>'provider'='musicbrainz'`, programOp)
	f.undo(t, programOp)
	f.assertCount(t, 0, `SELECT count(*) FROM external_links WHERE operation_id=$1`, programOp)
	f.assertCount(t, 1, `SELECT count(*) FROM curation_suppressions WHERE target_id=$1 AND subject='external_link' AND value->>'external_id'=$2`, f.a, remote.ID)
}

func TestPostgreSQLCurationWriterSuppressExternalLink(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	_, remote := f.tagged(t)
	modelSuppressLink(t, f, "release", f.a, remote.ID)
	f.assertCount(t, 1, `SELECT count(*) FROM curation_suppressions WHERE target_id=$1 AND subject='external_link'
		AND operation_id IS NULL AND created_by=$2`, f.a, f.admin.id)
	f.run(t)
	f.assertCount(t, 0, `SELECT count(*) FROM external_links WHERE object_id=$1 AND external_id=$2`, f.a, remote.ID)
}
