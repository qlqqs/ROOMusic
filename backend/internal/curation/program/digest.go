package program

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

// 仅接受本包构造的字符串、整数、布尔值及其结构；encoding/json 对 map 的键排序。
func jsonValue(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func digest(v any) string {
	h := sha256.Sum256(jsonValue(v))
	return hex.EncodeToString(h[:])
}

// ReleaseDigest 使用目录契约的固定顺序（介质、曲目、标识、标记），保留人工设空的存在位。
func ReleaseDigest(e catalog.CurationEvidence) string { return digest(e) }

func mediaMarkers(e catalog.CurationEvidence, kind, target, subject string) []catalog.CurationSuppression {
	var markers []catalog.CurationSuppression
	for _, s := range e.Suppressions {
		if s.TargetType == kind && s.TargetID == target && (s.Subject == subject || s.Subject == subject+"_source") {
			markers = append(markers, s)
		}
	}
	return markers
}

func ArtworkDigest(e catalog.CurationEvidence) string {
	return digest(struct {
		ExternalID, ExternalGroupID string
		Local, External, File       bool
		Markers                     []catalog.CurationSuppression
	}{e.ExternalID, e.ExternalGroupID, e.LocalArtwork, e.ExternalArtwork, e.ArtworkFile, mediaMarkers(e, "release", e.ID, "artwork")})
}

func LyricsDigest(e catalog.CurationEvidence, t catalog.CurationTrack) string {
	album := e.Title
	if e.TitlePlaceholder {
		album = ""
	}
	return digest(struct {
		Title, Artist, Album               string
		Duration                           int
		ArtistPlaceholder, Local, External bool
		Markers                            []catalog.CurationSuppression
	}{t.Title, t.Artist, album, t.DurationSeconds, t.ArtistPlaceholder, t.LocalLyrics, t.ExternalLyrics, mediaMarkers(e, "track", t.ID, "lyrics")})
}
