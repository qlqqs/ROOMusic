package library

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

// candidateSignature 只读取候选成员的暂存状态和附属文件的目录项/元信息，不读取文件内容。
// 任一输入无法列举或取状态时返回错误，由保存流程照常写入而不尝试跳过。
func candidateSignature(root registeredRoot, candidate ingest.OrganizedCandidate) (string, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "g%d\nk%s\n", ingest.GroupingVersion, ingest.CandidateIdentity(root.ID, candidate.Anchor))
	var members []ingest.SourceObservation
	sheets := map[string]bool{}
	for _, tracks := range candidate.Mediums {
		for _, track := range tracks {
			members = append(members, track.Observation)
			if sheet := track.Observation.CueSheetPath; sheet != "" {
				sheets[sheet] = true
			}
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].RelativePath < members[j].RelativePath })
	for _, member := range members {
		fmt.Fprintf(hash, "m%s\x00%d\x00%s\x00%s\n", member.RelativePath, member.SourceSize, signatureTime(member.SourceModifiedAt), member.ParserVersion)
	}
	appendFile := func(kind, relativePath string) error {
		info, err := os.Lstat(filepath.Join(root.Path, filepath.FromSlash(relativePath)))
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s%s\x00%d\x00%s\n", kind, relativePath, info.Size(), signatureTime(info.ModTime()))
		return nil
	}
	sheetPaths := make([]string, 0, len(sheets))
	for sheet := range sheets {
		sheetPaths = append(sheetPaths, sheet)
	}
	sort.Strings(sheetPaths)
	for _, sheet := range sheetPaths {
		if err := appendFile("s", sheet); err != nil {
			return "", err
		}
	}
	for relativePath, err := range listFolderArtworkCandidates(root.Path, candidate.ArtworkDirectories(), root.filter) {
		if err != nil {
			return "", err
		}
		if err := appendFile("a", relativePath); err != nil {
			return "", err
		}
	}
	for _, directory := range candidateSourceDirectories(candidate) {
		fullDirectory := filepath.Join(root.Path, filepath.FromSlash(directory))
		entries, err := os.ReadDir(fullDirectory)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		// ReadDir 已按文件名字节序排序；与抓轨证据相同，只考虑普通 .log 文件。
		for _, entry := range entries {
			if !strings.EqualFold(filepath.Ext(entry.Name()), ".log") || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, err := os.Lstat(filepath.Join(fullDirectory, entry.Name()))
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			fmt.Fprintf(hash, "l%s\x00%s\x00%d\x00%s\n", directory, entry.Name(), info.Size(), signatureTime(info.ModTime()))
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func signatureTime(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")
}
