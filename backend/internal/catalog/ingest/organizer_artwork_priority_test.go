package ingest

import (
	"fmt"
	"reflect"
	"testing"
)

// 全根独占标记与原始父目录观察都满足时，Box 叶子才允许使用父目录封面。
func TestArtworkPriorityBox父目录封面要求独占(t *testing.T) {
	for _, tc := range []struct {
		name      string
		exclusive [2]bool
		extraPath string
		want      bool
	}{
		{"仅碟目录且均有独占标记", [2]bool{true, true}, "", true},
		{"普通兄弟目录音频阻止兜底", [2]bool{true, true}, "Box/普通专辑/other.flac", false},
		{"父目录直接音频阻止兜底", [2]bool{true, true}, "Box/direct.flac", false},
		{"缺少独占标记", [2]bool{false, false}, "", false},
		{"部分观察缺少独占标记", [2]bool{true, false}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := artworkPriorityObservation(t, "Box/Disc 1/first.flac", "专辑甲", 4, false)
			second := artworkPriorityObservation(t, "Box/Disc 3/second.flac", "专辑乙", 8, false)
			first.DiscNumber, second.DiscNumber = 1, 3
			first.TrackNumber, second.TrackNumber = 1, 1
			first.DiscParentExclusive, second.DiscParentExclusive = tc.exclusive[0], tc.exclusive[1]
			items := []SourceObservation{first, second}
			if tc.extraPath != "" {
				items = append(items, artworkPriorityObservation(t, tc.extraPath, "普通专辑", 12, false))
			}
			got := OrganizeObservations(items)
			if len(got) != len(items) {
				t.Fatalf("各目录应独立形成候选：实际=%d，期望=%d", len(got), len(items))
			}
			leaves := 0
			for _, candidate := range got {
				if candidate.Anchor.Scope != first.Directory && candidate.Anchor.Scope != second.Directory {
					if candidate.BoxParentArtwork {
						t.Error("普通候选不应开启 Box 父目录封面兜底")
					}
					continue
				}
				leaves++
				if candidate.Anchor.Kind != CandidateBoxLeaf || candidate.BoxParentArtwork != tc.want {
					t.Errorf("%s：候选类型=%s，父目录兜底=%t，期望 box_leaf/%t", candidate.Anchor.Scope, candidate.Anchor.Kind, candidate.BoxParentArtwork, tc.want)
				}
			}
			if leaves != 2 {
				t.Fatalf("应保留两个 Box 叶子，实际=%d", leaves)
			}
		})
	}
}

func TestArtworkPriority格式拆分Box叶子保留父目录封面标记(t *testing.T) {
	flac := artworkPriorityObservation(t, "Box/Disc 1/01.flac", "专辑甲", 4, false)
	mp3 := artworkPriorityObservation(t, "Box/Disc 1/01.mp3", "专辑甲", 8, false)
	other := artworkPriorityObservation(t, "Box/Disc 3/other.flac", "专辑乙", 12, false)
	items := []SourceObservation{flac, mp3, other}
	for i := range items {
		items[i].TrackNumber = 1
		items[i].DiscNumber = 1
		items[i].DiscParentExclusive = true
	}
	items[2].DiscNumber = 3
	got := OrganizeObservations(items)
	if len(got) != 3 {
		t.Fatalf("格式拆分叶子应形成两个版本，并保留另一个叶子，实际候选数=%d", len(got))
	}
	formats := map[string]bool{}
	for _, candidate := range got {
		if !candidate.BoxParentArtwork {
			t.Errorf("%s：独占父目录封面标记丢失", candidate.Anchor.Scope)
		}
		if candidate.Anchor.Scope == other.Directory {
			if candidate.Anchor.Kind != CandidateBoxLeaf {
				t.Errorf("未拆分叶子类型=%s，期望 box_leaf", candidate.Anchor.Kind)
			}
			continue
		}
		if candidate.Anchor.Scope != flac.Directory || candidate.Anchor.Kind != CandidateFormatSplit || candidate.OriginalKind != CandidateBoxLeaf {
			t.Errorf("格式拆分叶子锚点或原类型不符：锚点=%+v，原类型=%s", candidate.Anchor, candidate.OriginalKind)
		}
		formats[candidate.Anchor.Partition] = true
	}
	if !reflect.DeepEqual(formats, map[string]bool{"format:flac": true, "format:mp3": true}) {
		t.Errorf("格式版本不符：%v", formats)
	}
}

// 多轨 CUE 淘汰两个物理父来源后，按原始封面范围迁移，并保留幸存曲目已有的图片。
func TestArtworkPriorityCUE合并按原封面范围迁移(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		name := "两份封面字节"
		if deferred {
			name = "字节与延迟封面并存"
		}
		t.Run(name, func(t *testing.T) {
			first := artworkPriorityObservation(t, "Media/Disc 1/image.flac", "专辑", 4, false)
			second := artworkPriorityObservation(t, "Media/Disc 3/image.flac", "专辑", 8, deferred)
			first.ArtworkScope, second.ArtworkScope = "原始碟一封面范围", "原始碟三封面范围"
			items := []SourceObservation{first, second}
			cuePaths := map[string]bool{}
			for index, parent := range []SourceObservation{first, second} {
				for track := 1; track <= 2; track++ {
					relative := fmt.Sprintf("Sheets/album.cue#track-%d", index*2+track)
					cuePaths[relative] = true
					items = append(items, SourceObservation{
						RelativePath: relative, Directory: "Sheets", Album: "专辑", Artist: "测试艺人",
						SourceKind: "cue_virtual", TrackNumber: index*2 + track,
						CueSheetPath: "Sheets/album.cue", CueParentRelativePath: parent.RelativePath,
					})
				}
			}
			// 一个幸存曲目已经有图，迁移时只能使用其余空位。
			existing := artworkPriorityObservation(t, items[2].RelativePath, "专辑", 12, false)
			existing.ArtworkScope = "幸存曲目既有封面范围"
			items[2].Artwork, items[2].ArtworkMIME = existing.Artwork, existing.ArtworkMIME
			items[2].ArtworkRelativePath, items[2].ArtworkScope = existing.ArtworkRelativePath, existing.ArtworkScope

			got := resolveCueCandidateGroups(items)
			if len(got) != 4 {
				t.Fatalf("两个物理父来源应被四个 CUE 虚拟轨取代，实际曲目数为 %d", len(got))
			}
			type artworkFields struct {
				Data       []byte
				MIME, Path string
				Deferred   bool
			}
			fields := func(o SourceObservation) artworkFields {
				return artworkFields{o.Artwork, o.ArtworkMIME, o.ArtworkRelativePath, o.ArtworkDeferred}
			}
			want := map[string]artworkFields{
				first.ArtworkScope: fields(first), second.ArtworkScope: fields(second), existing.ArtworkScope: fields(existing),
			}
			seen := map[string]bool{}
			for _, survivor := range got {
				if !isCueObservation(survivor) || !cuePaths[survivor.RelativePath] {
					t.Fatalf("物理来源未被淘汰或幸存曲目身份改变：%s", survivor.RelativePath)
				}
				delete(cuePaths, survivor.RelativePath)
				if len(survivor.Artwork) == 0 && !survivor.ArtworkDeferred {
					if survivor.RelativePath == existing.RelativePath {
						t.Fatal("幸存曲目原有封面被清除")
					}
					continue
				}
				expected, ok := want[survivor.ArtworkScope]
				if !ok || seen[survivor.ArtworkScope] || !reflect.DeepEqual(fields(survivor), expected) {
					t.Errorf("封面范围 %q 未独立保留原字节、MIME、来源路径及延迟标记", survivor.ArtworkScope)
				}
				if survivor.RelativePath == existing.RelativePath && survivor.ArtworkScope != existing.ArtworkScope {
					t.Error("迁移覆盖了幸存曲目原有封面")
				}
				seen[survivor.ArtworkScope] = true
			}
			for scope := range want {
				if !seen[scope] {
					t.Errorf("封面范围 %q 丢失，未迁入尚无封面的幸存曲目", scope)
				}
			}
		})
	}
}
