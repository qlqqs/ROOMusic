package governance

import (
	"encoding/hex"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const (
	maxArtistKeys      = 100
	maxArtistGroups    = 100
	maxArtistReleases  = 1000
	maxArtistTracks    = 100000
	maxArtistBindings  = 1000
	maxArtistOverrides = 10000
)

// NormalizeArtistName 只规范显示空白，不用搜索键替换显示文字。
func NormalizeArtistName(name string) (string, error) {
	if !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 || len(name) > 4096 {
		return "", operations.Invalid("invalid_artist_name", "艺人名含有不允许的字符或超过 4096 字节")
	}
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", operations.Invalid("invalid_artist_name", "艺人名不能为空")
	}
	return name, nil
}

func artistIDs(ids []string, limit int, required bool) ([]string, error) {
	if len(ids) > limit || (required && len(ids) == 0) {
		return nil, invalidArtistScope()
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !uuid.Valid(id) {
			return nil, invalidArtistScope()
		}
		id = strings.ToLower(id)
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}
func invalidArtistScope() error {
	return operations.Invalid("invalid_artist_scope", "艺人范围无效")
}
func artistScopeChanged() error {
	return operations.Conflict("artist_scope_changed", "署名范围已变化，请重新预览")
}
func artistScopeTooLarge() error {
	return operations.Conflict("artist_scope_too_large", "本次影响范围过大，请缩小范围")
}
func validArtistRevision(id string, revision int64) error {
	if !uuid.Valid(id) || revision < 1 || revision >= 9007199254740991 {
		return operations.Invalid("invalid_input", "艺人标识或版本号无效")
	}
	return nil
}

// ArtistMergeRequest 合并双方的版本号都参与指纹与事务复核。
type ArtistMergeRequest struct {
	ArtistID               string `json:"-"`
	ExpectedRevision       int64  `json:"expected_revision"`
	SourceArtistID         string `json:"source_artist_id"`
	SourceExpectedRevision int64  `json:"source_expected_revision"`
}

func (r *ArtistMergeRequest) Normalize() error {
	if err := validArtistRevision(r.ArtistID, r.ExpectedRevision); err != nil {
		return err
	}
	if err := validArtistRevision(r.SourceArtistID, r.SourceExpectedRevision); err != nil {
		return err
	}
	r.ArtistID = strings.ToLower(r.ArtistID)
	r.SourceArtistID = strings.ToLower(r.SourceArtistID)
	return nil
}
func (r ArtistMergeRequest) Fingerprint() string {
	return operations.Fingerprint(struct {
		Type, ID string
		Request  ArtistMergeRequest
	}{operations.TypeArtistMerge, strings.ToLower(r.ArtistID), r})
}

// ArtistSplitScope 使用明确名字键与组选择；实际发行和曲目快照由服务端计算。
type ArtistSplitScope struct {
	Mode       string   `json:"mode"`
	NameKeyIDs []string `json:"name_key_ids"`
	GroupIDs   []string `json:"group_ids,omitempty"`
}

func (s *ArtistSplitScope) Normalize() error {
	var err error
	s.NameKeyIDs, err = artistIDs(s.NameKeyIDs, maxArtistKeys, true)
	if err != nil {
		return err
	}
	switch s.Mode {
	case "aliases":
		if len(s.GroupIDs) != 0 {
			return invalidArtistScope()
		}
		s.GroupIDs = nil
	case "albums":
		s.GroupIDs, err = artistIDs(s.GroupIDs, maxArtistGroups, true)
	default:
		return invalidArtistScope()
	}
	return err
}

type ArtistSplitTarget struct {
	Kind             string  `json:"kind"`
	DisplayName      *string `json:"display_name,omitempty"`
	ArtistID         string  `json:"artist_id,omitempty"`
	ExpectedRevision int64   `json:"expected_revision,omitempty"`
}
type ArtistSplitRequest struct {
	ArtistID string `json:"-"`
	ArtistSplitScope
	ExpectedRevision int64             `json:"expected_revision"`
	Basis            string            `json:"basis"`
	Target           ArtistSplitTarget `json:"target"`
}

func (r *ArtistSplitRequest) Normalize() error {
	if err := validArtistRevision(r.ArtistID, r.ExpectedRevision); err != nil {
		return err
	}
	r.ArtistID = strings.ToLower(r.ArtistID)
	if err := r.ArtistSplitScope.Normalize(); err != nil {
		return err
	}
	decoded, err := hex.DecodeString(r.Basis)
	if err != nil || len(decoded) != 32 {
		return invalidArtistScope()
	}
	r.Basis = strings.ToLower(r.Basis)
	switch r.Target.Kind {
	case "new":
		if r.Target.ArtistID != "" || r.Target.ExpectedRevision != 0 {
			return invalidArtistScope()
		}
		if r.Target.DisplayName != nil {
			value, err := NormalizeArtistName(*r.Target.DisplayName)
			if err != nil {
				return err
			}
			r.Target.DisplayName = &value
		}
	case "restore":
		if r.Target.DisplayName != nil {
			return invalidArtistScope()
		}
		if err := validArtistRevision(r.Target.ArtistID, r.Target.ExpectedRevision); err != nil {
			return err
		}
		r.Target.ArtistID = strings.ToLower(r.Target.ArtistID)
	default:
		return invalidArtistScope()
	}
	return nil
}
func (r ArtistSplitRequest) Fingerprint() string {
	return operations.Fingerprint(struct {
		Type, ID string
		Request  ArtistSplitRequest
	}{operations.TypeArtistSplit, strings.ToLower(r.ArtistID), r})
}

type ArtistRenameRequest struct {
	ArtistID         string  `json:"-"`
	ExpectedRevision int64   `json:"expected_revision"`
	Mode             string  `json:"mode"`
	Name             *string `json:"name,omitempty"`
}

func (r *ArtistRenameRequest) Normalize() error {
	if err := validArtistRevision(r.ArtistID, r.ExpectedRevision); err != nil {
		return err
	}
	r.ArtistID = strings.ToLower(r.ArtistID)
	switch r.Mode {
	case "set":
		if r.Name == nil {
			return operations.Invalid("invalid_artist_name", "艺人名不能为空")
		}
		name, err := NormalizeArtistName(*r.Name)
		if err != nil {
			return err
		}
		r.Name = &name
	case "auto":
		if r.Name != nil {
			return operations.Invalid("invalid_input", "恢复自动名称不能带名称")
		}
	default:
		return operations.Invalid("invalid_input", "名称修改方式无效")
	}
	return nil
}
func (r ArtistRenameRequest) Fingerprint() string {
	return operations.Fingerprint(struct {
		Type, ID string
		Request  ArtistRenameRequest
	}{operations.TypeArtistRename, strings.ToLower(r.ArtistID), r})
}

type ArtistKeyPair struct {
	LeftKeyID  string `json:"left_key_id"`
	RightKeyID string `json:"right_key_id"`
}
type ArtistCandidateIgnoreRequest struct {
	ArtistID              string          `json:"-"`
	ExpectedRevision      int64           `json:"expected_revision"`
	OtherArtistID         string          `json:"other_artist_id"`
	OtherExpectedRevision int64           `json:"other_expected_revision"`
	KeyPairs              []ArtistKeyPair `json:"key_pairs"`
	Ignored               *bool           `json:"ignored"`
}

func (r *ArtistCandidateIgnoreRequest) Normalize() error {
	if err := validArtistRevision(r.ArtistID, r.ExpectedRevision); err != nil {
		return err
	}
	if err := validArtistRevision(r.OtherArtistID, r.OtherExpectedRevision); err != nil {
		return err
	}
	if r.Ignored == nil || len(r.KeyPairs) == 0 || len(r.KeyPairs) > 100 {
		return invalidArtistScope()
	}
	r.ArtistID = strings.ToLower(r.ArtistID)
	r.OtherArtistID = strings.ToLower(r.OtherArtistID)
	pairs := make([]ArtistKeyPair, 0, len(r.KeyPairs))
	seen := map[ArtistKeyPair]bool{}
	for _, p := range r.KeyPairs {
		if !uuid.Valid(p.LeftKeyID) || !uuid.Valid(p.RightKeyID) {
			return invalidArtistScope()
		}
		p.LeftKeyID = strings.ToLower(p.LeftKeyID)
		p.RightKeyID = strings.ToLower(p.RightKeyID)
		if p.LeftKeyID == p.RightKeyID {
			return invalidArtistScope()
		}
		if p.LeftKeyID > p.RightKeyID {
			p.LeftKeyID, p.RightKeyID = p.RightKeyID, p.LeftKeyID
		}
		if !seen[p] {
			seen[p] = true
			pairs = append(pairs, p)
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].LeftKeyID != pairs[j].LeftKeyID {
			return pairs[i].LeftKeyID < pairs[j].LeftKeyID
		}
		return pairs[i].RightKeyID < pairs[j].RightKeyID
	})
	r.KeyPairs = pairs
	return nil
}
func (r ArtistCandidateIgnoreRequest) Fingerprint() string {
	return operations.Fingerprint(struct {
		Type, ID string
		Request  ArtistCandidateIgnoreRequest
	}{operations.TypeArtistCandidateIgnore, strings.ToLower(r.ArtistID), r})
}
