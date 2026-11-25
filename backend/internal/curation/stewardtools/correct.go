package stewardtools

import (
	"context"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func AutoCorrectRelease(writer *curation.Writer) tool.Handler {
	return autoCorrect(writer, false)
}

func AutoCorrectTrack(writer *curation.Writer) tool.Handler {
	return autoCorrect(writer, true)
}

func autoCorrect(writer *curation.Writer, track bool) tool.Handler {
	a := adapter{writer: writer}
	return func(ctx context.Context, c tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
		var in struct {
			TrackID          string                                    `json:"track_id"`
			ReleaseID        string                                    `json:"release_id"`
			ExpectedRevision int64                                     `json:"expected_revision"`
			Changes          map[string]governance.RawCorrectionChange `json:"changes"`
			Basis            tool.Basis                                `json:"basis"`
		}
		if json.Unmarshal(raw, &in) != nil || in.ExpectedRevision < 1 || len(in.Changes) == 0 {
			return nil, invalid()
		}
		run, items, err := a.claimed(ctx, c)
		if err != nil {
			return nil, err
		}
		item, err := selectItem(items, in.ReleaseID, true)
		if err != nil {
			return nil, err
		}
		parse := governance.ParseCorrectionChanges
		if track {
			parse = governance.ParseTrackCorrectionChanges
		}
		changes, err := parse(in.Changes)
		if err != nil {
			return nil, toolError(err)
		}
		fields := make(map[string]catalog.CorrectionIntent, len(changes))
		for name, change := range changes {
			if change.Mode == catalog.CorrectionClear {
				return nil, &tool.Error{Code: "invalid_correction", Message: "自动整理不能清除修正"}
			}
			intent := catalog.CorrectionIntent{Mode: change.Mode}
			if change.Mode == catalog.CorrectionSet {
				switch name {
				case "year":
					intent.Value = change.Year
				case "genre":
					intent.Value = change.Genres
				default:
					intent.Value = change.Text
				}
			}
			fields[name] = intent
		}
		ctx = bindItem(ctx, run, c, item)
		batch, err := run.Batch(ctx)
		if err != nil {
			return nil, toolError(err)
		}
		source := modelSource(run, in.Basis.Note)
		source.WriterLevel = catalog.LevelModelLocal
		var result curation.Applied
		if track {
			result, err = writer.ApplyTrack(ctx, curation.TrackRequest{TrackID: in.TrackID, ReleaseID: item.TargetID, Fields: fields, Source: source, BatchID: batch, Key: c.IdempotencyKey, RequestID: c.RequestID, ExpectedRevision: in.ExpectedRevision, ConversationID: run.ConversationID})
		} else {
			result, err = writer.Apply(ctx, curation.Request{ReleaseID: item.TargetID, Fields: fields, Source: source, BatchID: batch, Key: c.IdempotencyKey, RequestID: c.RequestID, ExpectedRevision: in.ExpectedRevision, ConversationID: run.ConversationID})
		}
		if err != nil {
			return nil, toolError(err)
		}
		return json.Marshal(result)
	}
}
