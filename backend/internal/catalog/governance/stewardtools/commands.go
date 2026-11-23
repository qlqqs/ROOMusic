package stewardtools

import (
	"encoding/json"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"strings"
)

func (a adapter) command(name string, raw json.RawMessage) (command, error) {
	var p input
	if err := json.Unmarshal(raw, &p); err != nil {
		return command{}, invalid()
	}
	if name == "merge_releases" {
		c := governance.ReleaseMergeRequest{TargetID: strings.ToLower(p.TargetReleaseID), SourceID: strings.ToLower(p.SourceReleaseID), Token: p.Token}
		if !uuid.Valid(c.TargetID) || !uuid.Valid(c.SourceID) || c.TargetID == c.SourceID || c.Token == "" {
			return command{}, invalid()
		}
		return command{operations.TypeReleaseMerge, c.Fingerprint(), governance.ReleaseMergeExecutor(c)}, nil
	}
	if p.ExpectedRevision < 1 {
		return command{}, invalid()
	}
	p.ReleaseID = strings.ToLower(p.ReleaseID)
	p.GroupID = strings.ToLower(p.GroupID)
	p.ArtistID = strings.ToLower(p.ArtistID)
	if strings.HasPrefix(name, "correct_") || name == "change_release_group" || name == "review_release_issue" || name == "claim_release" {
		if !uuid.Valid(p.ReleaseID) {
			return command{}, invalid()
		}
	}
	switch name {
	case "correct_track":
		if !uuid.Valid(p.TrackID) || len(p.Changes) == 0 {
			return command{}, invalid()
		}
		changes, err := governance.ParseTrackCorrectionChanges(p.Changes)
		if err != nil {
			return command{}, err
		}
		c := governance.TrackCorrectionRequest{ReleaseID: p.ReleaseID, TrackID: strings.ToLower(p.TrackID), ExpectedRevision: p.ExpectedRevision, Changes: changes}
		return command{governance.TypeTrackCorrection, c.Fingerprint(), governance.TrackCorrectionExecutor(c)}, nil
	case "correct_release":
		changes, err := governance.ParseCorrectionChanges(p.Changes)
		if err != nil {
			return command{}, err
		}
		c := governance.CorrectionRequest{ReleaseID: p.ReleaseID, ExpectedRevision: p.ExpectedRevision, Changes: changes}
		return command{operations.TypeReleaseCorrection, c.Fingerprint(), governance.CorrectionExecutor(c)}, nil
	case "change_release_group":
		if (p.Action == "join" && !uuid.Valid(p.GroupID)) || (p.Action == "detach" && p.GroupID != "") || (p.Action != "join" && p.Action != "detach") {
			return command{}, invalid()
		}
		c := governance.GroupChangeRequest{ReleaseID: p.ReleaseID, ExpectedRevision: p.ExpectedRevision, Action: p.Action, GroupID: p.GroupID}
		return command{operations.TypeReleaseGroupChange, c.Fingerprint(), governance.GroupChangeExecutor(c)}, nil
	case "set_default_release":
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if !uuid.Valid(p.GroupID) || len(fields["release_id"]) == 0 || (string(fields["release_id"]) != "null" && !uuid.Valid(p.ReleaseID)) {
			return command{}, invalid()
		}
		c := governance.GroupDefaultRequest{GroupID: p.GroupID, ReleaseID: p.ReleaseID, ExpectedRevision: p.ExpectedRevision}
		return command{operations.TypeReleaseGroupDefault, c.Fingerprint(), governance.GroupDefaultExecutor(c)}, nil
	case "review_release_issue":
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if !governance.ReviewableIssue(p.Issue) || (p.Action == "review" && p.BasisToken == "") || (p.Action == "unreview" && fields["basis_token"] != nil) || (p.Action != "review" && p.Action != "unreview") {
			return command{}, invalid()
		}
		c := governance.IssueReviewRequest{ReleaseID: p.ReleaseID, ExpectedRevision: p.ExpectedRevision, Issue: p.Issue, Action: p.Action, Basis: p.BasisToken}
		return command{operations.TypeReleaseIssueReview, c.Fingerprint(), governance.IssueReviewExecutor(c)}, nil
	case "claim_release":
		if !uuid.Valid(p.SourceReleaseID) || strings.EqualFold(p.SourceReleaseID, p.ReleaseID) || p.SourceRevision < 1 || p.Preview == "" {
			return command{}, invalid()
		}
		c := governance.ClaimRequest{ReleaseID: p.ReleaseID, ExpectedRevision: p.ExpectedRevision, SourceID: strings.ToLower(p.SourceReleaseID), SourceRevision: p.SourceRevision, Preview: p.Preview}
		return command{operations.TypeReleaseClaim, c.Fingerprint(), governance.ClaimExecutor(c)}, nil
	}
	// 展示依据不进入现有命令；拆分的证据令牌恢复成执行器的 basis 字段。
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "basis")
	if token, ok := fields["basis_token"]; ok {
		fields["basis"] = token
		delete(fields, "basis_token")
	}
	body, _ := json.Marshal(fields)
	switch name {
	case "merge_artists":
		var c governance.ArtistMergeRequest
		if json.Unmarshal(body, &c) != nil {
			return command{}, invalid()
		}
		c.ArtistID = p.ArtistID
		if err := c.Normalize(); err != nil {
			return command{}, err
		}
		return command{operations.TypeArtistMerge, c.Fingerprint(), a.artists.MergeExecutor(c)}, nil
	case "split_artist":
		var c governance.ArtistSplitRequest
		if json.Unmarshal(body, &c) != nil {
			return command{}, invalid()
		}
		c.ArtistID = p.ArtistID
		if err := c.Normalize(); err != nil {
			return command{}, err
		}
		return command{operations.TypeArtistSplit, c.Fingerprint(), a.artists.SplitExecutor(c)}, nil
	case "rename_artist":
		var c governance.ArtistRenameRequest
		if json.Unmarshal(body, &c) != nil {
			return command{}, invalid()
		}
		c.ArtistID = p.ArtistID
		if c.Mode == "auto" && fields["name"] != nil {
			return command{}, invalid()
		}
		if err := c.Normalize(); err != nil {
			return command{}, err
		}
		return command{operations.TypeArtistRename, c.Fingerprint(), a.artists.RenameExecutor(c)}, nil
	case "ignore_artist_candidate":
		var c governance.ArtistCandidateIgnoreRequest
		if json.Unmarshal(body, &c) != nil {
			return command{}, invalid()
		}
		c.ArtistID = p.ArtistID
		if err := c.Normalize(); err != nil {
			return command{}, err
		}
		return command{operations.TypeArtistCandidateIgnore, c.Fingerprint(), a.artists.CandidateIgnoreExecutor(c)}, nil
	}
	return command{}, invalid()
}
