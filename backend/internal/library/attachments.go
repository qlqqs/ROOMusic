package library

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

type attachmentFile struct {
	path string
	info os.FileInfo
}

type attachmentDirectory struct {
	files []attachmentFile
	err   error
}

func listAttachments(root registeredRoot, dir string) attachmentDirectory {
	var files []attachmentFile
	dirs := []string{dir}
	for i := 0; i < len(dirs); i++ {
		entries, err := os.ReadDir(filepath.Join(root.Path, dirs[i]))
		if err != nil {
			return attachmentDirectory{err: err}
		}
		for _, entry := range entries {
			name := entry.Name()
			relative := filepath.ToSlash(filepath.Join(dirs[i], name))
			if entry.IsDir() {
				if i == 0 && !root.filter.skipDir(relative, name) {
					switch strings.ToLower(name) {
					case "art", "artwork", "scans", "extras":
						dirs = append(dirs, relative)
					}
				}
				continue
			}
			if root.filter.skipFile(name) || !entry.Type().IsRegular() {
				continue
			}
			switch strings.ToLower(filepath.Ext(name)) {
			case ".pdf", ".jpeg", ".jpg", ".png", ".gif", ".bmp", ".tif", ".tiff", ".webp":
				info, err := entry.Info()
				if err != nil {
					return attachmentDirectory{err: err}
				}
				files = append(files, attachmentFile{relative, info})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return attachmentDirectory{files: files}
}

func candidateAttachments(root registeredRoot, c ingest.OrganizedCandidate, cache map[string]attachmentDirectory) attachmentDirectory {
	dir := c.Anchor.Scope
	if dir == "" && c.Anchor.Kind != ingest.CandidateMultiDisc && c.OriginalKind != ingest.CandidateMultiDisc {
		return attachmentDirectory{}
	}
	if d, ok := cache[dir]; ok {
		return d
	}
	d := listAttachments(root, dir)
	if cache != nil {
		cache[dir] = d
	}
	return d
}

func (d attachmentDirectory) read(root registeredRoot) ([]ingest.Attachment, error) {
	if d.err != nil {
		return nil, d.err
	}
	out := make([]ingest.Attachment, 0, len(d.files))
	for _, file := range d.files {
		a := ingest.Attachment{RelativePath: file.path, Kind: "document"}
		if !strings.EqualFold(filepath.Ext(file.path), ".pdf") {
			f, err := os.Open(filepath.Join(root.Path, file.path))
			if err != nil {
				return nil, err
			}
			cfg, format, err := image.DecodeConfig(f)
			f.Close()
			if err != nil {
				return nil, err
			}
			a.Kind, a.Format, a.Width, a.Height = "image", format, cfg.Width, cfg.Height
		}
		out = append(out, a)
	}
	return out, nil
}
