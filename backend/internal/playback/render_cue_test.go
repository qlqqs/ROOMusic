package playback

import (
	"errors"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func TestRenderCUEMissingParentIsSourceChanged(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parent *string
	}{
		{name: "未记录父路径"},
		{name: "父路径为空", parent: textPointer("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := resolved{source: catalog.TrackSource{
				SourceKind: "cue_virtual", RelativePath: "album.cue#track=1", CueParentRelativePath: tc.parent,
			}}
			// 从实际打开入口核对：缺少父路径时不能尝试打开虚拟路径。
			opened, err := target.openSource()
			if opened != nil {
				opened.File.Close()
				t.Fatal("缺少 CUE 父路径却打开了文件")
			}
			classified := classifyRenderError(err)
			var renderErr *RenderError
			if !errors.Is(classified, ErrSourceChanged) || !errors.As(classified, &renderErr) || renderErr.FailureReason() != "source_changed" {
				t.Fatalf("错误 = %v，期望 source_changed 且保留 ErrSourceChanged", classified)
			}
		})
	}
}
