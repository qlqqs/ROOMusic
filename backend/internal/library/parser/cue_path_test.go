package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCueNameEqualNormalizesUnicodeAndCase(t *testing.T) {
	for _, testCase := range []struct {
		name, left, right string
		want              bool
	}{
		{name: "相同名字", left: "image.flac", right: "image.flac", want: true},
		{name: "NFC 与 NFD", left: "Café.flac", right: "Cafe\u0301.flac", want: true},
		{name: "NFD 与 NFC", left: "Cafe\u0301.flac", right: "Café.flac", want: true},
		{name: "大小写", left: "IMAGE.FLAC", right: "image.flac", want: true},
		{name: "规范化加大小写", left: "CAFÉ.FLAC", right: "cafe\u0301.flac", want: true},
		{name: "保留重音区别", left: "Café.flac", right: "Cafe.flac"},
		{name: "保留空白区别", left: "image.flac", right: " image.flac"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := cueNameEqual(testCase.left, testCase.right); got != testCase.want {
				t.Fatalf("cueNameEqual(%q, %q) = %t，期望 %t", testCase.left, testCase.right, got, testCase.want)
			}
		})
	}
}

func TestCueReferenceLooseMatchingRequiresUniqueFile(t *testing.T) {
	for _, testCase := range []struct {
		name, reference, wantPath string
		files, directories        []string
		ambiguous                 bool
	}{
		{name: "NFC 引用 NFD 文件", reference: "Café.flac", files: []string{"Cafe\u0301.flac"}, wantPath: "Cafe\u0301.flac"},
		{name: "NFD 引用 NFC 文件", reference: "Cafe\u0301.flac", files: []string{"Café.flac"}, wantPath: "Café.flac"},
		{name: "大小写唯一匹配", reference: "IMAGE.FLAC", files: []string{"image.flac"}, wantPath: "image.flac"},
		{name: "规范化加大小写唯一匹配", reference: "CAFÉ.FLAC", files: []string{"cafe\u0301.flac"}, wantPath: "cafe\u0301.flac"},
		{name: "精确路径优先", reference: "Café.flac", files: []string{"Café.flac", "CAFE\u0301.FLAC"}, wantPath: "Café.flac"},
		{name: "同名优先于 stem", reference: "CAFÉ.WAV", files: []string{"cafe\u0301.wav", "Café.flac"}, wantPath: "cafe\u0301.wav"},
		{name: "大小写歧义", reference: "IMAGE.FLAC", files: []string{"Image.flac", "image.flac"}, ambiguous: true},
		{name: "规范化歧义", reference: "CAFÉ.FLAC", files: []string{"Café.flac", "Cafe\u0301.flac"}, ambiguous: true},
		{name: "同名歧义不再 stem 回退", reference: "CAFÉ.WAV", files: []string{"Café.wav", "Cafe\u0301.wav", "Café.flac"}, ambiguous: true},
		{name: "stem 规范化加大小写", reference: "CAFÉ.wav", files: []string{"cafe\u0301.FLAC"}, wantPath: "cafe\u0301.FLAC"},
		{name: "stem 歧义不再换格式", reference: "CAFÉ.wav", files: []string{"Café.flac", "Cafe\u0301.FLAC", "café.mp3"}, ambiguous: true},
		{name: "stem 按扩展名优先级唯一匹配", reference: "CAFÉ.aiff", files: []string{"café.mp3", "Cafe\u0301.flac"}, wantPath: "Cafe\u0301.flac"},
		{name: "目录不占文件匹配名额", reference: "CAFÉ.FLAC", files: []string{"Café.flac"}, directories: []string{"Cafe\u0301.flac"}, wantPath: "Café.flac"},
		{name: "目录部分不宽松匹配", reference: "disc/IMAGE.FLAC", files: []string{"Disc/image.flac"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			for _, directory := range testCase.directories {
				if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range testCase.files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				// 引用匹配不要求解码音频内容。
				if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cuePath := filepath.Join(root, "album.cue")
			content := fmt.Sprintf("FILE %q WAVE\n TRACK 01 AUDIO\n  INDEX 01 00:00:00\n", testCase.reference)
			if err := os.WriteFile(cuePath, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			document, err := ParseCueDocument(cuePath, AudioDuration)
			if err != nil {
				t.Fatal(err)
			}
			if len(document.Files) != 1 || len(document.Tracks) != 1 {
				t.Fatalf("引用或轨道数量错误：%+v", document)
			}
			reference, track := document.Files[0], document.Tracks[0]
			if testCase.wantPath != "" {
				if reference.Status != "present" || reference.Path != testCase.wantPath || reference.ResolvedPath != filepath.Join(root, testCase.wantPath) || track.ReferenceStatus != "present" || track.ReferencedFile != testCase.wantPath || len(document.Diagnostics) != 0 {
					t.Fatalf("未采用唯一匹配的实际文件名：%+v", document)
				}
				return
			}
			if reference.Status != "missing" || track.ReferenceStatus != "missing" || reference.Path != testCase.reference || track.ReferencedFile != testCase.reference || len(document.Diagnostics) != 1 {
				t.Fatalf("没有唯一匹配时不应采用文件：%+v", document)
			}
			wantDiagnostic := "missing FILE reference: "
			if testCase.ambiguous {
				wantDiagnostic = "ambiguous FILE reference: "
			}
			if !strings.HasPrefix(document.Diagnostics[0], wantDiagnostic) {
				t.Fatalf("诊断 = %q，期望前缀 %q", document.Diagnostics[0], wantDiagnostic)
			}
		})
	}
}

func TestCueLooseMatchingStillRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.flac")
	if err := os.WriteFile(outside, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "cafe\u0301.FLAC")); err != nil {
		t.Fatal(err)
	}
	status, _, diagnostic := validateCueReference(filepath.Join(root, "album.cue"), root, "CAFÉ.wav")
	if status != "unsafe" || !strings.Contains(diagnostic, "symlink escapes containment root") {
		t.Fatalf("宽松匹配放行越界链接：status=%q diagnostic=%q", status, diagnostic)
	}
}
