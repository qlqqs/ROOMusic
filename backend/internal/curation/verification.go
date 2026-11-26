package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// VerificationCandidate 的 before/after 与外部定位由生产提案的后端提供，提交判断的工具只接收结论。
type VerificationCandidate struct {
	ReleaseID     string `json:"release_id"`
	TrackID       string `json:"track_id,omitempty"`
	Field         string `json:"field"`
	Before        string `json:"before"`
	After         string `json:"after"`
	ArtistA       string `json:"artist_a,omitempty"`
	ArtistB       string `json:"artist_b,omitempty"`
	ExternalID    string `json:"external_id,omitempty"`
	ExternalValue string `json:"external_value,omitempty"`
	OperationID   string `json:"operation_id,omitempty"`
	Structure     bool   `json:"structure,omitempty"`
}

func normalizedName(name string) string { return textnorm.Loose(textnorm.Text(name)) }

// QueueVerification 供程序匹配及巡检提交候选，已结清的同一证据不会重新排队。
func QueueVerification(ctx context.Context, tx *sql.Tx, kind, digest string, v VerificationCandidate) error {
	if kind != "verify_name" && kind != "verify_artist_merge" || !uuid.Valid(v.ReleaseID) || digest == "" {
		return operations.Invalid("invalid_input", "核对项种类或目标无效")
	}
	// 文字纠正跳过规范化后相同的写法；不同本地身份即使仅有标点之别，也交给合并佐证规则。
	if v.Before == "" || v.After == "" || v.Before == v.After || (kind == "verify_name" && normalizedName(v.Before) == normalizedName(v.After)) {
		return nil
	}
	if kind == "verify_artist_merge" && (!uuid.Valid(v.ArtistA) || !uuid.Valid(v.ArtistB) || v.ArtistA == v.ArtistB) {
		return operations.Invalid("invalid_input", "艺人对无效")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	id := DerivedKey(v.ReleaseID, operations.Fingerprint([]any{kind, digest, v}))
	_, err = tx.ExecContext(ctx, `INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,rule_version,input_digest,identity_digest,evidence)
 VALUES($1,'release',$2,$3,'waiting_model',1,$4,$4,$5) ON CONFLICT(id) DO NOTHING`, id, v.ReleaseID, kind, digest, raw)
	return err
}

// MaterializeVerificationItems 同时接入升级前和程序新留下的有效署名提案。
func (w *Writer) MaterializeVerificationItems(ctx context.Context) error {
	_, err := w.db.ExecContext(ctx, `INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,rule_version,input_digest,identity_digest,evidence)
 SELECT p.id,'release',p.release_id,'verify_artist_credit','waiting_model',1,p.identity_digest,p.identity_digest,p.evidence
 FROM curation_artist_credit_proposals p JOIN curation_work_states s ON s.subject='release' AND s.target_id=p.release_id AND s.identity_digest=p.identity_digest
 WHERE p.status='open' AND NOT EXISTS(SELECT FROM curation_pending_items old WHERE old.id=p.id) AND `+proposalSourceExists+` ON CONFLICT(id) DO NOTHING`)
	return err
}

type VerificationCitation struct {
	URL   string `json:"url"`
	Quote string `json:"quote"`
}

type VerificationInput struct {
	ItemID   string                `json:"item_id"`
	Verdict  string                `json:"verdict"`
	Reason   string                `json:"reason"`
	Citation *VerificationCitation `json:"citation,omitempty"`
}
type LibrarySupport struct {
	Basis   string   `json:"basis"`
	Matches int      `json:"matches"`
	Total   int      `json:"total"`
	Items   []string `json:"items"`
}
type VerificationPlan struct {
	Support *LibrarySupport `json:"library_support,omitempty"`

	nameProof    *VerificationCandidate
	citationPage string
	Candidate    VerificationCandidate `json:"candidate"`
	Method       string                `json:"method"`
	Revision     int64                 `json:"revision,omitempty"`
	Citation     *VerificationCitation `json:"citation,omitempty"`
}

// 两个捕获组分别是姓名。整句须明确表达别名关系，单独提到两名不构成证据。
var artistIdentityStatements = []*regexp.Regexp{
	regexp.MustCompile(`^(.+?)\s*[，,]?\s*(?:又名|亦名|也叫|又称|别名(?:为|是)?|艺名(?:为|是)?|本名(?:为|是)?)\s*(.+)$`),
	regexp.MustCompile(`^(.+?)\s*是\s*(.+?)\s*的(?:艺名|本名|别名)$`),
	regexp.MustCompile(`^(.+?)\s*(?:与|和)\s*(.+?)\s*是同一(?:个|位|支)?(?:人|艺人|歌手|音乐人|艺术家|乐队|乐团)(?:的不同(?:名字|艺名))?$`),
	regexp.MustCompile(`(?i)^(.+?)(?:,? (?:is also known as|also known as|aka|is the (?:stage name|pseudonym) of)) (.+)$`),
	regexp.MustCompile(`(?i)^(.+?) and (.+?) are the same (?:person|artist|band)$`),
}

func webArtistIdentity(body, quote, first, second string) bool {
	// 从整页划分陈述，避免引用只截取了否定或疑问中的肯定片段。
	first, second = strings.ToLower(first), strings.ToLower(second)
	supported := false
	start := 0
	for end, mark := range body + "。" {
		if !strings.ContainsRune(".。!?！？\n", mark) {
			continue
		}
		raw := strings.TrimSpace(body[start:end])
		start = end + len(string(mark))
		sentence := strings.ToLower(strings.Join(strings.Fields(raw), " "))
		if !strings.Contains(sentence, first) || !strings.Contains(sentence, second) {
			continue
		}
		affirmative := false
		for _, statement := range artistIdentityStatements {
			names := statement.FindStringSubmatch(sentence)
			if names != nil && ((names[1] == first && names[2] == second) || (names[1] == second && names[2] == first)) {
				affirmative = true
				break
			}
		}
		if !affirmative || mark == '?' || mark == '？' {
			return false
		}
		supported = supported || strings.Contains(quote, raw)
	}
	return supported
}

func noEvidence() error        { return operations.Conflict("no_evidence", "没有可核实的依据") }
func staleVerification() error { return operations.Conflict("item_stale", "核对项依据已变化") }

// 只从已发布、仍生效的对应读取署名依据，再逐位核对本地名字。
func creditCandidate(ctx context.Context, q operations.Queryer, item PendingItem, manual bool) (VerificationCandidate, error) {
	var v VerificationCandidate
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT p.evidence FROM curation_artist_credit_proposals p JOIN curation_work_states s ON s.subject='release' AND s.target_id=p.release_id AND s.identity_digest=p.identity_digest
 WHERE p.id=$1 AND (p.status='open' OR ($3 AND p.status='declined')) AND p.identity_digest=$2 AND `+proposalSourceExists, item.ID, item.IdentityDigest, manual).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, staleVerification()
	}
	if err != nil {
		return v, err
	}
	var p ArtistCreditProposal
	if err = json.Unmarshal(raw, &p); err != nil {
		return v, err
	}
	credits, err := catalog.LoadCurationArtistCredits(ctx, q, p.ReleaseID)
	if err != nil {
		return v, err
	}
	group := credits.Tracks[p.Local.TrackID]
	switch p.Local.Role {
	case "release_artist":
		group = credits.ReleaseArtists
	case "album_artist":
		group = credits.AlbumArtists
	}
	at := p.Local.Position - 1
	if at < 0 || at >= len(group) || group[at].ArtistID != p.Local.ArtistID || group[at].Name != p.Local.Name || MusicBrainzSpecialArtist(p.External.ArtistID) {
		return v, staleVerification()
	}
	return VerificationCandidate{ReleaseID: p.ReleaseID, TrackID: p.Local.TrackID, Field: "artist_link", Before: p.Local.Name, After: p.External.CreditName, ExternalValue: p.External.PrimaryName, ArtistA: p.Local.ArtistID, ExternalID: p.External.ArtistID, OperationID: p.Basis.OperationID}, nil
}

func (w *Writer) VerificationPlan(ctx context.Context, item PendingItem, in VerificationInput, nameProof *VerificationCandidate) (VerificationPlan, error) {
	var citationPage string
	if in.Citation != nil {
		run, ok := ModelRunFrom(ctx)
		if !ok || run.EvidencePage == nil || item.Route != "web" || strings.TrimSpace(in.Citation.Quote) == "" {
			return VerificationPlan{}, noEvidence()
		}
		body, err := run.EvidencePage(ctx, item.ID, in.Citation.URL)
		if err != nil {
			return VerificationPlan{}, err
		}
		if !strings.Contains(body, in.Citation.Quote) {
			return VerificationPlan{}, noEvidence()
		}
		citationPage = body
	}
	return verificationPlan(ctx, w.db, item, in.Citation, citationPage, false, nameProof)
}

func verificationPlan(ctx context.Context, q operations.Queryer, item PendingItem, cite *VerificationCitation, citationPage string, manual bool, nameProof *VerificationCandidate) (VerificationPlan, error) {
	var p VerificationPlan
	if item.Kind == "verify_artist_credit" {
		v, err := creditCandidate(ctx, q, item, manual)
		p.Candidate = v
		p.Method = "external_credit"
		return p, err
	}
	v := &p.Candidate
	if err := json.Unmarshal(item.Evidence, v); err != nil {
		return p, err
	}
	if v.ReleaseID != item.TargetID {
		return p, staleVerification()
	}
	switch item.Kind {
	case "verify_name":
		if nameProof == nil {
			return p, noEvidence()
		}
		*v = *nameProof
		p.nameProof = nameProof
		if !v.Structure || normalizedName(v.Before) == normalizedName(v.After) {
			return p, noEvidence()
		}
		var original string
		var err error
		if v.TrackID != "" && (v.Field == "title" || v.Field == "artist") {
			err = q.QueryRowContext(ctx, `SELECT CASE $3 WHEN 'title' THEN t.title ELSE t.artist END,t.edit_revision FROM track_effective t JOIN media m ON m.id=t.medium_id WHERE t.id=$1 AND m.release_id=$2`, v.TrackID, v.ReleaseID, v.Field).Scan(&original, &p.Revision)
		} else if v.TrackID == "" && (v.Field == "album_artist" || v.Field == "title") {
			err = q.QueryRowContext(ctx, `SELECT CASE $2 WHEN 'title' THEN title ELSE album_artist END,edit_revision FROM release_effective WHERE id=$1`, v.ReleaseID, v.Field).Scan(&original, &p.Revision)
		} else {
			return p, staleVerification()
		}
		if errors.Is(err, sql.ErrNoRows) || err == nil && original != v.Before {
			return p, staleVerification()
		}
		if err != nil {
			return p, err
		}
		subject, id := "release", v.ReleaseID
		if v.TrackID != "" {
			subject, id = "track", v.TrackID
		}
		var live bool
		err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM external_links l JOIN operations o ON o.id=l.operation_id WHERE l.source='musicbrainz' AND l.object_type=$1 AND l.object_id=$2 AND l.external_id=$3 AND l.operation_id=NULLIF($4,'')::uuid AND NOT EXISTS(SELECT FROM operations u WHERE u.reverts_operation_id=o.id))`, subject, id, v.ExternalID, v.OperationID).Scan(&live)
		if err != nil {
			return p, err
		}
		if !live {
			return p, staleVerification()
		}
		if v.ExternalValue != "" && normalizedName(v.ExternalValue) == normalizedName(v.After) {
			p.Method = "external_value"
			return p, nil
		}
		majority, err := nameMajority(ctx, q, *v)
		if err != nil {
			return p, err
		}
		if cite != nil && normalizedName(v.ExternalValue) != "" && normalizedName(v.ExternalValue) != normalizedName(v.Before) && strings.Contains(normalizedName(cite.Quote), normalizedName(v.After)) {
			p.Method = "web_name"
			p.Citation = cite
			return p, nil
		}
		if majority == nil {
			return p, noEvidence()
		}
		p.Support = majority
		p.Method = "library_majority"
	case "verify_artist_merge":
		if !uuid.Valid(v.ArtistA) || !uuid.Valid(v.ArtistB) {
			return p, staleVerification()
		}
		if err := artistPairNames(ctx, q, *v); err != nil {
			return p, err
		}
		if err := checkArtistSeparation(ctx, q, v.ArtistA, v.ArtistB); err != nil {
			return p, err
		}
		if cite != nil && !webArtistIdentity(citationPage, cite.Quote, v.Before, v.After) {
			return p, operations.Conflict("no_strong_evidence", "网页引文未能明确证实两个名字属于同一艺人")
		}
		if manual {
			p.Method = "manual_confirmation"
			return p, nil
		}
		if v.ExternalID != "" {
			strong, err := artistEvidence(ctx, q, v.ArtistA, v.ArtistB, v.ExternalID)
			if err != nil {
				return p, err
			}
			if strong {
				p.Method = "external_identity"
				return p, nil
			}
		}
		proof, err := libraryArtistEvidence(ctx, q, *v)
		if err != nil {
			return p, err
		}
		if proof != nil {
			p.Support = proof
			p.Method = "library_identity"
			return p, nil
		}
		if cite == nil {
			return p, noEvidence()
		}
		p.citationPage = citationPage
		p.Method = "web_identity"
		p.Citation = cite
	default:
		return p, operations.Invalid("invalid_input", "未知核对项目")
	}
	return p, nil
}

func nameMajority(ctx context.Context, q operations.Queryer, v VerificationCandidate) (*LibrarySupport, error) {
	// 分别统计同一外部对象的其它版本和同一艺人的其它真实记录。每组按发行或曲目去重。
	object, id, relation, column, artistField, role := "release", v.ReleaseID, "release_effective", "release_id", "album_artist", "album_artist"
	if v.TrackID != "" {
		object, id, relation, column, artistField, role = "track", v.TrackID, "track_effective", "track_id", "artist", "track_artist"
	}
	query := `WITH artists AS (
 SELECT DISTINCT c.artist_id FROM artist_effective_credits c JOIN artist_credits ac ON ac.id=c.credit_id
 WHERE c.` + column + `=$2 AND c.role=$5 AND ac.layer<>'auto'),
 members AS (
 SELECT 'external_object' AS basis,l.object_id AS id FROM external_links l
 WHERE l.source='musicbrainz' AND l.object_type=$4 AND l.external_id=$1
 UNION
 SELECT 'artist',c.` + column + ` FROM artist_effective_credits c JOIN artists a USING(artist_id)
 JOIN artist_credits ac ON ac.id=c.credit_id WHERE c.role=$5 AND ac.layer<>'auto')
 SELECT m.basis,r.id,CASE $3 WHEN 'title' THEN r.title ELSE r.` + artistField + ` END
 FROM members m JOIN ` + relation + ` r ON r.id=m.id WHERE r.id<>$2
 AND NOT EXISTS(SELECT FROM ` + object + `_auto_values a WHERE a.` + column + `=r.id AND a.field=$3)
 ORDER BY m.basis,r.id`
	rows, err := q.QueryContext(ctx, query, v.ExternalID, id, v.Field, object, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string]*LibrarySupport{}
	for rows.Next() {
		var basis, id, spelling string
		if err := rows.Scan(&basis, &id, &spelling); err != nil {
			return nil, err
		}
		if spelling == "" {
			continue
		}
		stats := groups[basis]
		if stats == nil {
			stats = &LibrarySupport{Basis: basis}
			groups[basis] = stats
		}
		stats.Total++
		if normalizedName(spelling) == normalizedName(v.After) {
			stats.Matches++
			if len(stats.Items) < 5 {
				stats.Items = append(stats.Items, id)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, basis := range []string{"external_object", "artist"} {
		if s := groups[basis]; s != nil && s.Matches > s.Total/2 {
			return s, nil
		}
	}
	return nil, nil
}

func libraryArtistEvidence(ctx context.Context, q operations.Queryer, v VerificationCandidate) (*LibrarySupport, error) {
	var a, b string
	err := q.QueryRowContext(ctx, `SELECT a.track_id,b.track_id
 FROM artist_effective_credits a JOIN tracks ta ON ta.id=a.track_id JOIN media ma ON ma.id=ta.medium_id
 JOIN artist_effective_credits b ON b.role=a.role AND b.position=a.position AND b.track_id<>a.track_id
 JOIN tracks tb ON tb.id=b.track_id JOIN media mb ON mb.id=tb.medium_id AND mb.release_id=ma.release_id
 JOIN artist_credits ca ON ca.id=a.credit_id JOIN artist_credits cb ON cb.id=b.credit_id
 WHERE a.role='track_artist' AND a.artist_id=$1 AND b.artist_id=$2 AND ca.layer<>'auto' AND cb.layer<>'auto'
 ORDER BY a.track_id,b.track_id LIMIT 1`, v.ArtistA, v.ArtistB).Scan(&a, &b)
	if err == nil {
		return &LibrarySupport{Basis: "track_credit_position", Matches: 1, Total: 1, Items: []string{a, b}}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT c.artist_id,r.id,r.title,
 (SELECT count(*) FROM tracks t JOIN media m ON t.medium_id=m.id WHERE m.release_id=r.id AND t.source_status='present')
 FROM artist_effective_credits c JOIN artist_credits ac ON ac.id=c.credit_id JOIN release_effective r ON r.id=c.release_id
 WHERE c.artist_id IN ($1,$2) AND ac.layer<>'auto' AND NOT EXISTS(SELECT FROM release_auto_values a WHERE a.release_id=r.id AND a.field='title')
 ORDER BY c.artist_id,r.id`, v.ArtistA, v.ArtistB)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type album struct {
		title string
		count int
	}
	first := min(v.ArtistA, v.ArtistB)
	seen := map[album][]string{}
	for rows.Next() {
		var artist, id, title string
		var n int
		if err := rows.Scan(&artist, &id, &title, &n); err != nil {
			return nil, err
		}
		k := album{normalizedName(title), n}
		if k.title == "" || k.count == 0 {
			continue
		}
		if artist == first {
			if len(seen[k]) < 2 {
				seen[k] = append(seen[k], id)
			}
			continue
		}
		for _, other := range seen[k] {
			if other != id {
				return &LibrarySupport{Basis: "album_title_and_count", Matches: 1, Total: 1, Items: []string{other, id}}, nil
			}
		}
	}
	return nil, rows.Err()
}

type verificationWrite struct {
	item                  PendingItem
	plan                  VerificationPlan
	key, actor, exception string
}

func (v verificationWrite) check(ctx context.Context, tx *sql.Tx) error {
	manual := v.actor != ""
	if err := CheckMatchItem(ctx, tx, v.item, v.item.ConversationID, manual); err != nil {
		return err
	}
	if v.plan.nameProof != nil {
		entries, err := catalog.LoadCurationEvidence(ctx, tx, []string{v.item.TargetID})
		if err != nil {
			return err
		}
		if e, ok := entries[v.item.TargetID]; !ok || ReleaseIdentityDigest(e) != v.item.IdentityDigest {
			return staleVerification()
		}
	}
	fresh, err := verificationPlan(ctx, tx, v.item, v.plan.Citation, v.plan.citationPage, manual, v.plan.nameProof)
	if err != nil {
		return err
	}
	if operations.Fingerprint(fresh) != operations.Fingerprint(v.plan) {
		return staleVerification()
	}
	return nil
}
func (v verificationWrite) finish(ctx context.Context, tx *sql.Tx, operationID string) error {
	if v.item.Kind == "verify_artist_credit" {
		if err := queueCreditMerges(ctx, tx, v.item, v.plan.Candidate); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE curation_artist_credit_proposals SET status='accepted' WHERE id=$1`, v.item.ID); err != nil {
			return err
		}
	}
	if v.actor != "" {
		return FinishException(ctx, tx, v.exception, v.actor, "accepted", []string{operationID})
	}
	return ResolvePendingItem(ctx, tx, v.item.ID, v.item.ConversationID, "model_verified")
}

func (p VerificationPlan) source(model string) Source {
	s := Source{Kind: "model", Provider: NormalizeProvider(model), Basis: p.Method, WriterLevel: catalog.LevelModelExternal}
	switch p.Method {
	case "library_identity", "library_majority":
		s.Provider = "library_evidence"
		s.WriterLevel = catalog.LevelModelReference
	case "web_identity", "web_name":
		s.Provider = "web_search"
		s.WriterLevel = catalog.LevelModelReference
	}
	return s
}

func (w *Writer) ApplyVerification(ctx context.Context, item PendingItem, p VerificationPlan, batch, key, model string) (string, error) {
	return w.applyVerification(ctx, verificationWrite{item: item, plan: p, key: key}, batch, model)
}
func (w *Writer) applyVerification(ctx context.Context, write verificationWrite, batch, model string) (string, error) {
	p, item := write.plan, write.item
	v := p.Candidate
	source := p.source(model)
	switch item.Kind {
	case "verify_artist_credit":
		out, err := w.ApplyLinks(ctx, LinkRequest{verification: &write, RequireAll: true, ReleaseID: item.TargetID, Source: source, ExternalSource: "musicbrainz", Method: p.Method, Confidence: "reviewed", BatchID: batch, Key: write.key, ConversationID: item.ConversationID, ConfirmedBy: write.actor, Links: []LinkIntent{{ObjectType: "artist", ObjectID: v.ArtistA, ExternalID: v.ExternalID, ArtistPrimaryName: v.ExternalValue}}})
		return out.OperationID, err
	case "verify_name":
		req := Request{verification: &write, ReleaseID: item.TargetID, ExpectedRevision: p.Revision, Fields: map[string]catalog.CorrectionIntent{v.Field: {Mode: catalog.CorrectionSet, Value: v.After}}, Source: source, BatchID: batch, Key: write.key, ConversationID: item.ConversationID}
		out, err := w.applyFields(ctx, req, v.TrackID, v.TrackID != "")
		return out.OperationID, err
	case "verify_artist_merge":
		a, b := min(v.ArtistA, v.ArtistB), max(v.ArtistA, v.ArtistB)
		spec, err := w.sourceSpec(ctx, operations.TypeArtistMerge, a, operations.Fingerprint([]any{item.ID, p}), source, batch, write.key, "", write.actor, item.ConversationID)
		if err != nil {
			return "", err
		}
		check := func(ctx context.Context, tx *sql.Tx) error {
			if err := write.check(ctx, tx); err != nil {
				return err
			}
			if err := checkWrite(ctx, tx, spec, write.actor, item.TargetID, 0, 0); err != nil {
				return err
			}
			return checkArtistSeparation(ctx, tx, a, b)
		}
		merge := w.artists.CurationMergeExecutor(a, b, check)
		res, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, id string) (operations.Outcome, error) {
			out, err := merge(ctx, tx, id)
			if err == nil {
				err = write.finish(ctx, tx, id)
			}
			return out, err
		})
		if err != nil {
			return "", err
		}
		var out ArtistMergeApplied
		err = json.Unmarshal(res.Response, &out)
		return out.OperationID, err
	}
	return "", operations.Invalid("invalid_input", "没有可写的核对项目")
}

func (w *Writer) AcceptVerification(ctx context.Context, e ModelException, actor, key string) error {
	item, err := w.PendingItem(ctx, e.PendingItemID)
	if err != nil {
		return err
	}
	var in VerificationInput
	if err = json.Unmarshal(e.Input, &in); err != nil {
		return err
	}
	if item.ID != in.ItemID || (item.Kind != "verify_artist_merge" && item.Kind != "verify_artist_credit") {
		return staleVerification()
	}
	p, err := verificationPlan(ctx, w.db, item, nil, "", true, nil)

	if err != nil {
		return err
	}
	var saved struct {
		Changes []struct {
			Field         string
			Before, After string
		}
	}
	if err = json.Unmarshal(e.Preview, &saved); err != nil {
		return err
	}
	if len(saved.Changes) != 1 || saved.Changes[0].Field != p.Candidate.Field || saved.Changes[0].Before != p.Candidate.Before || saved.Changes[0].After != p.Candidate.After {
		return staleVerification()
	}
	_, err = w.applyVerification(ctx, verificationWrite{item: item, plan: p, key: key, actor: actor, exception: e.ID}, "", e.Provider)
	return err
}

func (w *Writer) FinishStaleVerification(ctx context.Context, item PendingItem) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = CheckMatchItem(ctx, tx, item, item.ConversationID, false); err != nil {
		return err
	}
	if err = ResolvePendingItem(ctx, tx, item.ID, item.ConversationID, "stale"); err != nil {
		return err
	}
	if item.Kind == "verify_artist_credit" {
		if _, err = tx.ExecContext(ctx, `UPDATE curation_artist_credit_proposals SET status='stale' WHERE id=$1 AND status='open'`, item.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// 根据身份当前实际使用或登记的名字核对两端，防止过时的名字对套用到其他身份。
func artistPairNames(ctx context.Context, q operations.Queryer, v VerificationCandidate) error {
	var count int
	err := q.QueryRowContext(ctx, `WITH expected(id,name) AS (VALUES($1::uuid,$2::text),($3::uuid,$4::text))
 SELECT count(*) FROM expected e JOIN artists a ON a.id=e.id LEFT JOIN artist_profiles p ON p.artist_id=a.id
 WHERE NOT EXISTS(SELECT FROM artist_overrides o WHERE o.artist_id=e.id AND o.redirect_artist_id IS NOT NULL)
 AND (e.name IN(a.initial_name,p.effective_display_name) OR EXISTS(SELECT FROM artist_search_names n WHERE n.artist_id=e.id AND n.original_value=e.name))`, v.ArtistA, v.Before, v.ArtistB, v.After).Scan(&count)
	if err == nil && count != 2 {
		return staleVerification()
	}
	return err
}

// 同位身份获采纳后，另行判断库中已有关联或使用这个外部名字的其它艺人。
func queueCreditMerges(ctx context.Context, tx *sql.Tx, item PendingItem, v VerificationCandidate) error {
	var p ArtistCreditProposal
	if err := json.Unmarshal(item.Evidence, &p); err != nil {
		return err
	}
	names := append([]string{p.External.PrimaryName, p.External.CreditName}, p.External.Aliases...)
	rows, err := tx.QueryContext(ctx, `WITH linked AS (
 SELECT l.object_id id FROM external_links l WHERE l.source='musicbrainz' AND l.object_type='artist' AND l.external_id=$1
 UNION SELECT artist_id FROM artist_search_names WHERE original_value=ANY($2::text[])),
 roots AS (SELECT DISTINCT COALESCE(o.redirect_artist_id,l.id) id FROM linked l LEFT JOIN artist_overrides o ON o.artist_id=l.id)
 SELECT a.id,coalesce(p.effective_display_name,a.initial_name) FROM roots r JOIN artists a ON a.id=r.id LEFT JOIN artist_profiles p ON p.artist_id=a.id WHERE a.id<>$3 ORDER BY a.id`, v.ExternalID, names, v.ArtistA)
	if err != nil {
		return err
	}
	var candidates []VerificationCandidate
	for rows.Next() {
		var b, name string
		if err := rows.Scan(&b, &name); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, VerificationCandidate{ReleaseID: item.TargetID, Field: "artist_merge", Before: v.Before, After: name, ArtistA: v.ArtistA, ArtistB: b, ExternalID: v.ExternalID})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, m := range candidates {
		if err := QueueVerification(ctx, tx, "verify_artist_merge", item.IdentityDigest, m); err != nil {
			return err
		}
	}
	return nil
}
