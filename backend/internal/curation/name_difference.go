package curation

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var nameSuffix = regexp.MustCompile(`(?i)[\s(\[（【-]+(?:(?:[0-9]{4}\s+)?remaster(?:ed)?(?:\s+[0-9]{4})?|live(?:\s+at\s+[^)\]]+)?|(?:feat\.?|featuring)\s+[^)\]]+)[)\]）】 ]*$`)

// 只有同一文字的一至两个编辑才交模型。先排除等价、版本后缀和跨文字的写法。
func nearbyName(a, b string) bool {
	key := func(s string) string { return strings.ReplaceAll(normalizedName(s), " ", "") }
	x, y := []rune(key(a)), []rune(key(b))
	if len(x) == 0 || len(y) == 0 || string(x) == string(y) || key(nameSuffix.ReplaceAllString(a, "")) == key(b) || key(nameSuffix.ReplaceAllString(b, "")) == key(a) {
		return false
	}
	for len(x) > 0 && len(y) > 0 && x[0] == y[0] {
		x = x[1:]
		y = y[1:]
	}
	for len(x) > 0 && len(y) > 0 && x[len(x)-1] == y[len(y)-1] {
		x = x[:len(x)-1]
		y = y[:len(y)-1]
	}
	scripts := func(rs []rune) int {
		mask := 0
		for _, r := range rs {
			switch {
			case unicode.Is(unicode.Latin, r):
				mask |= 1
			case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
				mask |= 2
			}
		}
		return mask
	}
	l, r := scripts(x), scripts(y)
	if l != 0 && r != 0 && l&r == 0 {
		return false
	}
	if len(x) > len(y)+2 || len(y) > len(x)+2 {
		return false
	}
	prev := make([]int, len(y)+1)
	curr := make([]int, len(prev))
	for j := range prev {
		prev[j] = j
	}
	for i, c := range x {
		curr[0] = i + 1
		best := curr[0]
		for j, d := range y {
			cost := 1
			if c == d {
				cost = 0
			}
			curr[j+1] = min(prev[j+1]+1, curr[j]+1, prev[j]+cost)
			best = min(best, curr[j+1])
		}
		if best > 2 {
			return false
		}
		prev, curr = curr, prev
	}
	return prev[len(y)] <= 2
}

func namePair(a, b string) (string, string) {
	a, b = normalizedName(a), normalizedName(b)
	return min(a, b), max(a, b)
}

// 迁移只能保存旧观察的原文。首次调度在事务中按当前字表计算名字键，并重分类旧的活动核对项。
func (w *Writer) refreshLegacyNames(ctx context.Context) error {
	var pending bool
	err := w.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_artist_negative_pairs WHERE legacy)
 OR EXISTS(SELECT FROM curation_pending_items WHERE kind IN ('verify_artist_credit','verify_artist_merge','verify_name') AND status<>'resolved' AND rule_version<2)
 OR EXISTS(SELECT FROM curation_artist_credit_proposals WHERE status='open' AND NOT evidence ? 'pair_low')`).Scan(&pending)
	if err != nil || !pending {
		return err
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	err = tx.QueryRowContext(ctx, `WITH taken AS(DELETE FROM curation_artist_negative_pairs WHERE legacy RETURNING *) SELECT coalesce(jsonb_agg(to_jsonb(taken)),'[]') FROM taken`).Scan(&raw)
	if err != nil {
		return err
	}
	var legacy []struct {
		A             string `json:"name_low"`
		B             string `json:"name_high"`
		Reason, Model string
		At            time.Time `json:"decided_at"`
	}
	if err = json.Unmarshal(raw, &legacy); err != nil {
		return err
	}
	for _, p := range legacy {
		a, b := namePair(p.A, p.B)
		if a != "" && b != "" {
			if _, err = tx.ExecContext(ctx, `INSERT INTO curation_artist_negative_pairs(name_low,name_high,reason,model,decided_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, a, b, p.Reason, p.Model, p.At); err != nil {
				return err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT 'proposal',id,evidence FROM curation_artist_credit_proposals WHERE status='open' AND NOT evidence ? 'pair_low'
 UNION ALL SELECT kind,id,evidence FROM curation_pending_items WHERE kind IN ('verify_artist_credit','verify_artist_merge','verify_name') AND status<>'resolved' AND rule_version<2`)
	if err != nil {
		return err
	}
	type change struct {
		kind, id string
		raw      []byte
	}
	var changes []change
	for rows.Next() {
		var c change
		if err = rows.Scan(&c.kind, &c.id, &c.raw); err != nil {
			rows.Close()
			return err
		}
		changes = append(changes, c)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, c := range changes {
		var v VerificationCandidate
		if c.kind == "proposal" || c.kind == "verify_artist_credit" {
			var p ArtistCreditProposal
			if err = json.Unmarshal(c.raw, &p); err != nil {
				return err
			}
			v.Before, v.After = p.Local.Name, p.External.CreditName
		} else if err = json.Unmarshal(c.raw, &v); err != nil {
			return err
		}
		a, b := namePair(v.Before, v.After)
		switch c.kind {
		case "proposal":
			_, err = tx.ExecContext(ctx, `UPDATE curation_artist_credit_proposals SET evidence=evidence||jsonb_build_object('pair_low',$2::text,'pair_high',$3::text) WHERE id=$1`, c.id, a, b)
		case "verify_name":
			_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET rule_version=2,status=CASE WHEN $2 THEN status ELSE 'resolved' END,resolution=CASE WHEN $2 THEN resolution ELSE 'kept' END WHERE id=$1 AND status<>'resolved'`, c.id, v.Structure && nearbyName(v.Before, v.After))
		default:
			_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET rule_version=2,evidence=evidence||jsonb_build_object('pair_low',$2::text,'pair_high',$3::text) WHERE id=$1 AND status<>'resolved'`, c.id, a, b)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
