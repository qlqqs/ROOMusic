package searchindex

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func TestStateReadyRequiresPublishedVersionTuple(t *testing.T) {
	ready := State{TargetGeneration: 3, PublishedGeneration: 3,
		TextnormVersion: textnorm.Version, PublishedTextnormVersion: textnorm.Version,
		SearchProjectionVersion: ProjectionVersion, PublishedSearchProjectionVersion: ProjectionVersion,
		Status: "ready", ManifestSealed: true}
	if !ready.Ready() {
		t.Fatal("完整发布元组应就绪")
	}
	for name, change := range map[string]func(*State){
		"未发布":         func(s *State) { s.Status = "building" },
		"失败":          func(s *State) { s.Status = "failed" },
		"清单未封闭":       func(s *State) { s.ManifestSealed = false },
		"代次不匹配":       func(s *State) { s.PublishedGeneration-- },
		"非法零代次":       func(s *State) { s.TargetGeneration, s.PublishedGeneration = 0, 0 },
		"规范化版本不匹配":    func(s *State) { s.PublishedTextnormVersion++ },
		"投影版本不匹配":     func(s *State) { s.PublishedSearchProjectionVersion++ },
		"二进制不认识规范化版本": func(s *State) { s.TextnormVersion++; s.PublishedTextnormVersion++ },
		"二进制不认识投影版本":  func(s *State) { s.SearchProjectionVersion++; s.PublishedSearchProjectionVersion++ },
	} {
		t.Run(name, func(t *testing.T) {
			state := ready
			change(&state)
			if state.Ready() {
				t.Fatal("不完整或未知元组不能开启新读取")
			}
		})
	}
}
