package maintenance

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func TestArtistBackfillReadinessRequiresPublishedCurrentGeneration(t *testing.T) {
	ready := artistState{State: searchindex.State{
		Status: "ready", ManifestSealed: true, TargetGeneration: 2, PublishedGeneration: 2,
		TextnormVersion: textnorm.Version, PublishedTextnormVersion: textnorm.Version,
		SearchProjectionVersion: artistProjectionVersion, PublishedSearchProjectionVersion: artistProjectionVersion,
	}}
	if !ready.Ready() {
		t.Fatal("完整发布状态未就绪")
	}
	tests := map[string]func(*artistState){
		"preparing":       func(s *artistState) { s.Status = "preparing" },
		"failed":          func(s *artistState) { s.Status = "failed" },
		"manifest":        func(s *artistState) { s.ManifestSealed = false },
		"generation":      func(s *artistState) { s.PublishedGeneration-- },
		"textnorm":        func(s *artistState) { s.PublishedTextnormVersion-- },
		"projection":      func(s *artistState) { s.PublishedSearchProjectionVersion-- },
		"unknown_version": func(s *artistState) { s.SearchProjectionVersion++; s.PublishedSearchProjectionVersion++ },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			s := ready
			change(&s)
			if s.Ready() {
				t.Fatal("不完整或未知版本仍可读取")
			}
		})
	}
}
