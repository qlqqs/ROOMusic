package catalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// LocalFile 保存核对本地辅助所需的扫描事实。SourcePath 对 CUE 虚轨仍唯一。
type LocalFile struct {
	SourcePath   string                       `json:"source_path"`
	Size         int64                        `json:"size"`
	Modified     int64                        `json:"modified_us"`
	ScanTags     metadata.ScanTags            `json:"scan_tags"`
	Credits      []metadata.ArtistTagEvidence `json:"credit_tags,omitempty"`
	Observations map[string]LocalObservation  `json:"observations,omitempty"`
	Readings     map[string]map[string]string `json:"readings,omitempty"`
}
type LocalObservation struct {
	Value    string `json:"value"`
	Source   string `json:"source"`
	Inferred bool   `json:"inferred"`
}

// LocalDirectoryPeers 找同一目录，以及同一非根父目录的直属音频候选。
func LocalDirectoryPeers(ctx context.Context, q Queryer, ids []string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `WITH audio AS NOT MATERIALIZED (
 SELECT m.release_id,t.source_root_id AS root_id,regexp_replace(COALESCE(t.cue_parent_relative_path,t.relative_path),'(^|/)[^/]*$','') AS dir
 FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_status='present'
 ), wanted AS (
 SELECT DISTINCT root_id,CASE WHEN strpos(dir,'/')>0 THEN regexp_replace(dir,'/[^/]+$','') END AS parent,dir
 FROM audio WHERE release_id=ANY($1::uuid[])
 ), peers AS (
 SELECT a.release_id FROM audio a JOIN wanted w USING(root_id,dir) WHERE a.dir<>''
 UNION SELECT a.release_id FROM audio a JOIN (SELECT DISTINCT root_id,parent FROM wanted WHERE parent IS NOT NULL) w
 ON a.root_id=w.root_id AND strpos(a.dir,'/')>0 AND regexp_replace(a.dir,'/[^/]+$','')=w.parent)
 SELECT id::text FROM curation_eligible_releases WHERE id IN (SELECT release_id FROM peers) OR id=ANY($1::uuid[]) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func LoadLocalFiles(ctx context.Context, q Queryer, ids []string) (map[string]LocalFile, error) {
	rows, err := q.QueryContext(ctx, `SELECT t.id::text,COALESCE(t.relative_path,''),COALESCE(t.source_size,0),COALESCE((extract(epoch FROM t.source_modified_at)*1000000)::bigint,0),
 COALESCE(t.scan_tags,'{}'), COALESCE((SELECT jsonb_agg(to_jsonb(a)-'track_id'-'id' ORDER BY role,tag_key,source_kind,occurrence_index) FROM artist_tag_evidence a WHERE track_id=t.id),'[]'),
 COALESCE((SELECT jsonb_object_agg(field_name,jsonb_build_object('value',value,'source',source_kind,'inferred',inferred)) FROM track_observations WHERE track_id=t.id AND field_name IS NOT NULL),'{}')
 FROM media m JOIN tracks t ON t.medium_id=m.id WHERE t.source_status='present' AND m.release_id=ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := map[string]LocalFile{}
	for rows.Next() {
		var id string
		var f LocalFile
		var tags, credits, obs []byte
		if err := rows.Scan(&id, &f.SourcePath, &f.Size, &f.Modified, &tags, &credits, &obs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(tags, &f.ScanTags); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(credits, &f.Credits); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(obs, &f.Observations); err != nil {
			return nil, err
		}
		f.Readings = localReadings(f)
		for _, field := range []string{"album", "title", "artist", "album_artist"} {
			delete(f.Observations, "legacy_bytes_"+field)
		}
		f.ScanTags.LegacyArtistParts = nil
		files[id] = f
	}
	return files, rows.Err()
}

func localReadings(f LocalFile) map[string]map[string]string {
	choices := []string{"GBK", "Big5", "Shift-JIS"}
	if f.ScanTags.LegacyEncodings != nil {
		choices = slices.Clone(*f.ScanTags.LegacyEncodings)
	}
	out := map[string]map[string]string{}
	readings := map[string][]metadata.LegacyReading{}
	for _, enc := range choices {
		decode := func(raw []byte) string {
			list, ok := readings[string(raw)]
			if !ok {
				list = metadata.LegacyReadings(raw)
				readings[string(raw)] = list
			}
			for _, r := range list {
				if r.Encoding == enc {
					return r.Value
				}
			}
			return ""
		}
		fields := map[string]string{}
		ok := true
		for _, field := range []string{"album", "title", "album_artist", "artist"} {
			if f.Observations[field].Source != "garbled" {
				continue
			}
			raw, _ := base64.StdEncoding.DecodeString(f.Observations["legacy_bytes_"+field].Value)
			value := metadata.DecodeLegacyField(field, raw, f.ScanTags.LegacyArtistParts[field], decode)
			if value == "" {
				ok = false
				break
			}
			fields[field] = value
		}
		if ok && len(fields) > 0 {
			out[enc] = fields
		}
	}
	return out
}
