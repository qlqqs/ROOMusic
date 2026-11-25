package stewardtools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const urlSchema = `{"type":"string","minLength":1,"maxLength":2048}`

type availability = func(context.Context, tool.Caller) bool

func (a adapter) identityTool(available availability) tool.Definition {
	return tool.Definition{Name: "resolve_artist_identity", Description: "提交已核对的 MusicBrainz 或 Wikidata 艺人身份，附判断依据与引用网页，经独立审查后建立对应。", MinRole: tool.RoleAdmin, Scope: tool.ScopeShared, Revertible: true, Available: available,
		Input:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"item_id":` + idSchema + `,"source":{"type":"string","enum":["musicbrainz","wikidata"]},"external_id":{"type":"string","minLength":2,"maxLength":36},"basis":{"type":"object","additionalProperties":false,"properties":{"note":` + noteSchema + `,"citations":{"type":"array","maxItems":5,"items":` + urlSchema + `}},"required":["note","citations"]}},"required":["item_id","source","external_id","basis"]}`),
		Output: json.RawMessage(`{"type":"object"}`), Preview: a.previewIdentity, Handler: a.resolveIdentity}
}

type identityInput struct {
	ItemID     string `json:"item_id"`
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
	Basis      struct {
		Note      string   `json:"note"`
		Citations []string `json:"citations"`
	} `json:"basis"`
}

func parseIdentity(raw json.RawMessage) (identityInput, error) {
	var in identityInput
	if json.Unmarshal(raw, &in) != nil || strings.TrimSpace(in.Basis.Note) == "" || len(in.Basis.Citations) == 0 || len(in.Basis.Citations) > 5 {
		return in, invalid()
	}
	for _, address := range in.Basis.Citations {
		if _, err := curation.ParseArtistWebURL(address); err != nil {
			return in, toolError(err)
		}
	}
	return in, nil
}

func (a adapter) artistCall(ctx context.Context, c tool.Caller, id string, in any) (context.Context, *curation.ModelRun, []curation.PendingItem, error) {
	if !uuid.Valid(id) || !uuid.Valid(c.IdempotencyKey) {
		return ctx, nil, nil, invalid()
	}
	run, items, err := a.claimed(ctx, c)
	if err != nil {
		return ctx, nil, nil, err
	}
	bound := *run
	bound.PendingItemID, bound.ActorID, bound.ToolInputDigest = id, c.User.ID, operations.Fingerprint(in)
	return curation.WithModelRun(ctx, &bound), &bound, items, nil
}

func selectArtistItem(items []curation.PendingItem, id string) (curation.PendingItem, error) {
	item, err := selectItem(items, id, false)
	if err != nil {
		return item, err
	}
	if item.Kind != "artist_identity" || item.TargetType != "artist" {
		return item, invalid()
	}
	return item, nil
}

func (a adapter) previewIdentity(ctx context.Context, c tool.Caller, raw json.RawMessage) (tool.Preview, error) {
	in, err := parseIdentity(raw)
	if err != nil {
		return tool.Preview{}, err
	}
	run, items, err := a.claimed(ctx, c)
	if err != nil {
		return tool.Preview{}, err
	}
	item, err := selectArtistItem(items, in.ItemID)
	if err != nil {
		return tool.Preview{}, err
	}
	plan, err := a.matcher.PlanArtistIdentity(ctx, item, in.Source, in.ExternalID)
	if err != nil {
		return tool.Preview{}, toolError(err)
	}
	var evidence struct {
		Names []string `json:"names"`
	}
	if err = json.Unmarshal(item.Evidence, &evidence); err != nil {
		return tool.Preview{}, err
	}
	p := tool.Preview{Summary: "建立艺人外部对应", Target: strings.Join(evidence.Names, " / ")}
	for _, change := range plan.Changes {
		p.Changes = append(p.Changes, tool.Change{Field: change.Field, Before: change.Before, After: change.After})
	}
	_, proof, err := artistPageEvidence(ctx, run, item, in.Basis.Citations)
	if err != nil {
		if !missingPageEvidence(err) {
			return p, toolError(err)
		}
		p.Contradicted = true
		p.Verification = json.RawMessage(`{"reason":"no_verified_page"}`)
		return p, nil
	}
	p.Verification, err = json.Marshal(proof)
	return p, err
}

func (a adapter) resolveIdentity(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
	in, err := parseIdentity(raw)
	if err != nil {
		return nil, err
	}
	ctx, run, items, err := a.artistCall(ctx, c, in.ItemID, in)
	if err != nil {
		return nil, err
	}
	prior, err := a.writer.ReplayArtistTool(ctx, c.IdempotencyKey, operations.TypeArtistLink)
	if prior != nil || err != nil {
		return prior, toolError(err)
	}
	item, err := selectArtistItem(items, in.ItemID)
	if err != nil {
		return nil, err
	}
	plan, err := a.matcher.PlanArtistIdentity(ctx, item, in.Source, in.ExternalID)
	if err != nil {
		return nil, toolError(err)
	}
	if c.ReviewID == "" || c.ReviewDecision != "approved" {
		return nil, &tool.Error{Code: tool.CodePermissionDenied, Message: "身份写入需要独立审查"}
	}
	proof, err := a.previewIdentity(ctx, c, raw)
	if err != nil {
		return nil, err
	}
	if proof.Contradicted {
		return nil, toolError(operations.Conflict("no_evidence", "身份对应缺少网页佐证"))
	}
	batch, err := run.Batch(ctx)
	if err != nil {
		return nil, toolError(err)
	}
	applied, err := a.matcher.ApplyArtistIdentity(ctx, plan, program.ApplyOptions{Source: modelSource(run, in.Basis.Note), Key: c.IdempotencyKey, Batch: batch, ConversationID: run.ConversationID, PendingItemID: item.ID})
	if err != nil {
		return nil, toolError(err)
	}
	return json.Marshal(applied)
}
