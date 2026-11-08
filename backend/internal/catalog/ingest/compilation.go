package ingest

import "github.com/qlqq/roomusic/backend/internal/catalog/metadata"

// applyCompilation 保留真实专辑艺人，否则按占位词或非目录观察的严格多数合辑标志决定。
func applyCompilation(observations []SourceObservation, artist, albumArtist *FieldDecision) {
	if albumArtist.Value != "" && !metadata.IsCompilationPlaceholder(albumArtist.Value) {
		return
	}
	physical, compilations := 0, 0
	for _, observation := range observations {
		if observation.SourceKind == "folder" {
			continue
		}
		physical++
		if observation.Compilation {
			compilations++
		}
	}
	if !metadata.IsCompilationPlaceholder(artist.Value) && compilations*2 <= physical {
		return
	}
	*artist = FieldDecision{
		Field: "artist", Value: metadata.CompilationDisplayName, Source: "compilation",
		Confidence: "high", Action: "auto_apply", RuleID: "compilation_v1", Candidates: artist.Candidates,
	}
	*albumArtist = FieldDecision{}
}
