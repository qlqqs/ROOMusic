package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// ArtistMergedSource 是仍可恢复的旧身份；名字键只提示当前可按别名拆回的部分。
type ArtistMergedSource struct {
	ArtistID    string    `json:"artist_id"`
	Name        string    `json:"name"`
	Revision    int64     `json:"revision"`
	NameKeyIDs  []string  `json:"name_key_ids"`
	OperationID string    `json:"operation_id"`
	MergedAt    time.Time `json:"merged_at"`
}
type ArtistMergedSources struct {
	Items []ArtistMergedSource `json:"items"`
}

// ReadArtistMergedSources 不从显示摘要猜测身份；恢复依据来自治理拥有的类型化持久快照。
func ReadArtistMergedSources(ctx context.Context, database catalog.ReadTransactionBeginner, artistID string) (ArtistMergedSources, error) {
	if !uuid.Valid(artistID) {
		return ArtistMergedSources{}, operations.Invalid("invalid_id", "艺人标识无效")
	}
	return catalog.ReadWithLocalJITOff(ctx, database, func(tx *sql.Tx) (ArtistMergedSources, error) {
		return readArtistMergedSources(ctx, tx, strings.ToLower(artistID))
	})
}

func readArtistMergedSources(ctx context.Context, q operations.Queryer, id string) (ArtistMergedSources, error) {
	result := ArtistMergedSources{Items: []ArtistMergedSource{}}
	ready, err := artistgraph.Ready(ctx, q)
	if err != nil {
		return result, err
	}
	if !ready {
		return result, catalog.ErrArtistCatalogPreparing
	}
	identities, err := loadArtistIdentities(ctx, q, []string{id})
	if err != nil {
		return result, err
	}
	target, ok := identities[id]
	if !ok {
		return result, catalog.ErrArtistNotFound
	}
	// 与其余艺人子路由一致，旧链接读取当前 canonical；写命令仍拒绝 redirect。
	if target.RedirectArtistID != nil {
		id = *target.RedirectArtistID
	}
	rows, err := q.QueryContext(ctx, `SELECT a.id::text,COALESCE(p.effective_display_name,a.initial_name),a.edit_revision,o.redirect_operation_id::text
 FROM artist_overrides o JOIN artists a ON a.id=o.artist_id
 LEFT JOIN artist_profiles p ON p.artist_id=a.id
 WHERE o.redirect_artist_id=$1::uuid ORDER BY a.id`, id)
	if err != nil {
		return result, err
	}
	sources := []ArtistMergedSource{}
	for rows.Next() {
		var source ArtistMergedSource
		var operationID *string
		if err := rows.Scan(&source.ArtistID, &source.Name, &source.Revision, &operationID); err != nil {
			rows.Close()
			return result, err
		}
		if operationID == nil {
			rows.Close()
			return result, errMergedSourceEvidence
		}
		source.OperationID = *operationID
		sources = append(sources, source)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	history := mergedSourceHistory{load: func(operationID string) (operations.Record, error) { return operations.Load(ctx, q, operationID) }, entries: map[string]mergedSourceEvidence{}}
	keySet := map[string]bool{}
	for _, source := range sources {
		evidence, err := history.origin(ctx, source.ArtistID, id, source.OperationID)
		if err != nil {
			return result, err
		}
		source.OperationID, source.MergedAt = evidence.id, evidence.createdAt
		source.NameKeyIDs = append([]string{}, evidence.keys...)
		for _, key := range evidence.keys {
			keySet[key] = true
		}
		result.Items = append(result.Items, source)
	}
	owned, err := mergedSourceOwnedKeys(ctx, q, id, keySet)
	if err != nil {
		return result, err
	}
	for i := range result.Items {
		source := &result.Items[i]
		keys := []string{}
		for _, key := range source.NameKeyIDs {
			if owned[key] {
				keys = append(keys, key)
			}
		}
		source.NameKeyIDs = keys
	}
	sort.Slice(result.Items, func(i, j int) bool {
		a, b := result.Items[i], result.Items[j]
		if !a.MergedAt.Equal(b.MergedAt) {
			return a.MergedAt.After(b.MergedAt)
		}
		return a.ArtistID < b.ArtistID
	})
	return result, nil
}

// 只有全局路由仍属于目标的键可直接走 aliases；第三人的作用域不因提示而被抢回。
func mergedSourceOwnedKeys(ctx context.Context, q operations.Queryer, id string, keys map[string]bool) (map[string]bool, error) {
	ids := make([]string, 0, len(keys))
	for key := range keys {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	owned := map[string]bool{}
	for start := 0; start < len(ids); start += maxArtistBindings {
		end := min(start+maxArtistBindings, len(ids))
		rows, err := q.QueryContext(ctx, `SELECT k.id::text FROM artist_name_keys k
 LEFT JOIN artist_name_bindings b ON b.key_id=k.id
 LEFT JOIN artist_overrides o ON o.artist_id=COALESCE(b.artist_id,k.initial_artist_id)
 WHERE k.id=ANY($1::text[]::uuid[]) AND COALESCE(o.redirect_artist_id,b.artist_id,k.initial_artist_id)=$2::uuid ORDER BY k.id`, ids[start:end], id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return nil, err
			}
			owned[key] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return owned, nil
}

var errMergedSourceEvidence = errors.New("invalid merged source evidence")

type mergedSourceEvidence struct {
	id, targetID  string
	createdAt     time.Time
	before, after map[string]artistIdentity
	keys          []string
}
type mergedSourceHistory struct {
	load    func(string) (operations.Record, error)
	entries map[string]mergedSourceEvidence
}

func (h *mergedSourceHistory) evidence(id string) (mergedSourceEvidence, error) {
	if entry, ok := h.entries[id]; ok {
		return entry, nil
	}
	if !uuid.Valid(id) {
		return mergedSourceEvidence{}, errMergedSourceEvidence
	}
	record, err := h.load(id)
	if err != nil {
		return mergedSourceEvidence{}, err
	}
	entry, err := decodeMergedSourceEvidence(record)
	if err != nil {
		return entry, err
	}
	h.entries[id] = entry
	return entry, nil
}

func (h *mergedSourceHistory) origin(ctx context.Context, artistID, targetID, operationID string) (mergedSourceEvidence, error) {
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return mergedSourceEvidence{}, err
		}
		if seen[operationID] {
			return mergedSourceEvidence{}, errMergedSourceEvidence
		}
		seen[operationID] = true
		entry, err := h.evidence(operationID)
		if err != nil {
			return entry, err
		}
		before, bok := entry.before[artistID]
		after, aok := entry.after[artistID]
		if !bok || !aok || entry.targetID != targetID || after.RedirectArtistID == nil || *after.RedirectArtistID != targetID || after.RedirectOperationID == nil || *after.RedirectOperationID != operationID {
			return mergedSourceEvidence{}, errMergedSourceEvidence
		}
		// 扁平化重定向不是该旧身份再次主动合并；沿它自己的上一条 redirect 证据回溯。
		if before.RedirectArtistID == nil {
			if after.Revision != before.Revision+1 {
				return mergedSourceEvidence{}, errMergedSourceEvidence
			}
			return entry, nil
		}
		if before.RedirectOperationID == nil || after.Revision != before.Revision {
			return mergedSourceEvidence{}, errMergedSourceEvidence
		}
		targetID, operationID = *before.RedirectArtistID, *before.RedirectOperationID
	}
}

func decodeMergedSourceEvidence(record operations.Record) (mergedSourceEvidence, error) {
	entry := mergedSourceEvidence{id: record.ID, targetID: record.TargetID, createdAt: record.CreatedAt, before: map[string]artistIdentity{}, after: map[string]artistIdentity{}, keys: []string{}}
	var before, after artistRecordState
	if record.Type != operations.TypeArtistMerge || record.TargetType != operations.TargetArtist || !uuid.Valid(record.TargetID) || json.Unmarshal(record.Before, &before) != nil || json.Unmarshal(record.After, &after) != nil || before.SchemaVersion != 1 || after.SchemaVersion != 1 {
		return entry, errMergedSourceEvidence
	}
	for _, a := range before.Artists {
		if !uuid.Valid(a.ID) || a.Revision < 1 {
			return entry, errMergedSourceEvidence
		}
		if _, exists := entry.before[a.ID]; exists {
			return entry, errMergedSourceEvidence
		}
		entry.before[a.ID] = a
	}
	for _, a := range after.Artists {
		if !uuid.Valid(a.ID) || a.Revision < 1 {
			return entry, errMergedSourceEvidence
		}
		if _, exists := entry.after[a.ID]; exists {
			return entry, errMergedSourceEvidence
		}
		entry.after[a.ID] = a
	}
	// before 的绑定/覆盖才是这次真正搬入的键；changes 文本、当前 initial_artist 都不是历史证据。
	keys := map[string]bool{}
	for _, binding := range before.Bindings {
		keys[binding.KeyID] = true
	}
	for _, assignment := range before.Assignments {
		keys[assignment.KeyID] = true
	}
	if len(keys) > maxArtistBindings {
		return entry, errMergedSourceEvidence
	}
	for key := range keys {
		if !uuid.Valid(key) {
			return entry, errMergedSourceEvidence
		}
		entry.keys = append(entry.keys, key)
	}
	sort.Strings(entry.keys)
	return entry, nil
}
