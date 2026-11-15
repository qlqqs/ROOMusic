package ingest

import (
	"fmt"
	"reflect"
	"testing"
)

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
