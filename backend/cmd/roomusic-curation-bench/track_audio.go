package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

func writeTrack(ctx context.Context, ffmpeg, dir string, t corpusTrack) error {
	path := filepath.Join(dir, t.File)
	// 每毫秒恰好 16 个采样，atrim 在帧内按采样数截断最后一帧。
	args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-af",
		fmt.Sprintf("atrim=end_sample=%d", t.LengthMS*16), "-map_metadata", "-1", "-fflags", "+bitexact", "-flags:a", "+bitexact"}
	mp3 := filepath.Ext(path) == ".mp3"
	if mp3 {
		args = append(args, "-c:a", "libmp3lame", "-id3v2_version", "0", "-write_id3v1", "0")
	} else {
		args = append(args, "-c:a", "flac")
		for _, k := range slices.Sorted(maps.Keys(t.Tags)) {
			args = append(args, "-metadata", k+"="+t.Tags[k])
		}
	}
	if stderr, err := exec.CommandContext(ctx, ffmpeg, append(args, path)...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s：%w %s", t.File, err, stderr)
	}
	if mp3 {
		tag, err := gbkTag(t.Tags)
		if err != nil {
			return err
		}
		audio, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, append(tag, audio...), 0644)
	}
	return nil
}

// 只有专辑名使用错误的 Latin-1 声明，其他文本保留正常的 ID3v2.3 UTF-16 标签。
func gbkTag(tags map[string]string) ([]byte, error) {
	var frames []byte
	for _, pair := range [][2]string{{"TALB", "ALBUM"}, {"TIT2", "TITLE"}, {"TPE1", "ARTIST"}, {"TPE2", "ALBUMARTIST"},
		{"TRCK", "TRACKNUMBER"}, {"TPOS", "DISCNUMBER"}, {"TPUB", "LABEL"}, {"TMED", "MEDIA"}, {"TYER", "DATE"}} {
		s := tags[pair[1]]
		if s == "" {
			continue
		}
		encoding := byte(1)
		encoder := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder()
		if pair[0] == "TALB" {
			encoding = 0
			encoder = simplifiedchinese.GBK.NewEncoder()
		}
		data, err := encoder.Bytes([]byte(s))
		if err != nil {
			return nil, fmt.Errorf("编码 %s：%w", pair[0], err)
		}
		frames = append(frames, pair[0]...)
		frames = binary.BigEndian.AppendUint32(frames, uint32(len(data)+1))
		frames = append(frames, 0, 0, encoding)
		frames = append(frames, data...)
	}
	return parsertest.ID3v23(frames), nil
}
