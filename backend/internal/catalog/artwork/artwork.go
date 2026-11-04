// Package artwork 管理 ROOMusic 数据目录下的受管封面：内容寻址的文件加 release_artworks 关联。
// 它只写数据目录，从不触碰音乐库；封面的发现（音乐根内的文件夹图片）属于扫描边界，不在这里。
package artwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Executor 是保存与删除封面关联所需的最小数据库能力。
type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Queryer 是读取封面关联所需的最小数据库能力。
type Queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Store 是数据目录下 artwork 子目录的受管封面存储。
type Store struct{ directory string }

// NewStore 返回以 dataDirectory/artwork 为根的封面存储。
func NewStore(dataDirectory string) *Store {
	return &Store{directory: filepath.Join(dataDirectory, "artwork")}
}

// Resource 是已确认存在的受管封面。ID 就是 storage_key：内容哈希加扩展名，同一个 ID 的字节永远不变，
// 所以它本身就能充当 HTTP 的强校验值。
type Resource struct {
	ID       string
	MIMEType string
}

// Lookup 按资源标识查封面关联，只读数据库、不读文件：调用方可以在确认资源存在后，
// 凭客户端已有的校验值直接答复“未修改”，省掉读文件。找不到关联或存储键不安全时返回错误。
func (s *Store) Lookup(ctx context.Context, database Queryer, resourceID string) (Resource, error) {
	var key, mime string
	if err := database.QueryRowContext(ctx, "SELECT storage_key,mime_type FROM release_artworks WHERE storage_key=$1", resourceID).Scan(&key, &mime); err != nil {
		return Resource{}, err
	}
	if !safeStorageKey(key) {
		return Resource{}, errors.New("unsafe artwork storage key")
	}
	return Resource{ID: key, MIMEType: mime}, nil
}

// Read 读取封面文件的字节。文件不存在或不可读时返回错误。
func (s *Store) Read(resource Resource) ([]byte, error) {
	if !safeStorageKey(resource.ID) {
		return nil, errors.New("unsafe artwork storage key")
	}
	return os.ReadFile(filepath.Join(s.directory, resource.ID))
}

// safeStorageKey 只接受 Save 会写出的形状：非空、不超过 255 字节，只含字母、数字、点、下划线与连字符，
// 且不是 "." 或 ".."。这样键既不会逃出数据目录，也不会破坏作为 ETag 写进响应头的引号语法。
func safeStorageKey(key string) bool {
	if key == "" || len(key) > 255 || key == "." || key == ".." {
		return false
	}
	for index := 0; index < len(key); index++ {
		character := key[index]
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

// Save 校验封面字节并保存为 Release 的受管封面：内容寻址写入数据目录，再写 release_artworks 关联，
// 关联写入失败时清理刚写入的文件，替换旧封面后清理不再被引用的旧文件。
// sourceType 记录来源（"embedded" 或 "folder"）；调用方负责取得字节。
func (s *Store) Save(ctx context.Context, executor Executor, releaseID string, data []byte, mimeType, sourceType string) error {
	prepared, err := s.describe(data, mimeType)
	if err != nil {
		return err
	}
	var previousKey sql.NullString
	if err := executor.QueryRowContext(ctx, "SELECT storage_key FROM release_artworks WHERE release_id=$1::uuid", releaseID).Scan(&previousKey); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := s.writePrepared(data, &prepared); err != nil {
		return err
	}
	hash, key, mimeType, width, height, path, wroteManagedFile := prepared.hash, prepared.key, prepared.mimeType, prepared.width, prepared.height, prepared.path, prepared.wroteManagedFile
	_, err = executor.ExecContext(ctx, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type) VALUES($1::uuid,$2,$3,$4,$5,$6,$7) ON CONFLICT(release_id) DO UPDATE SET content_hash=EXCLUDED.content_hash,mime_type=EXCLUDED.mime_type,width=EXCLUDED.width,height=EXCLUDED.height,storage_key=EXCLUDED.storage_key,source_type=EXCLUDED.source_type`, releaseID, hash, mimeType, width, height, key, sourceType)
	if err != nil {
		if wroteManagedFile {
			if cleanupErr := removeUnreferencedArtwork(ctx, executor, path, key); cleanupErr != nil {
				return errors.Join(err, fmt.Errorf("remove failed artwork: %w", cleanupErr))
			}
		}
		return err
	}
	if previousKey.Valid && previousKey.String != key && filepath.Base(previousKey.String) == previousKey.String {
		if cleanupErr := removeUnreferencedArtwork(ctx, executor, filepath.Join(s.directory, previousKey.String), previousKey.String); cleanupErr != nil {
			return fmt.Errorf("remove replaced artwork: %w", cleanupErr)
		}
	}
	return nil
}

type preparedArtwork struct {
	hash, key, mimeType, path string
	width, height             int
	wroteManagedFile          bool
}

// describe 只校验字节并确定内容寻址资源，不接触文件或数据库。
func (s *Store) describe(data []byte, mimeType string) (preparedArtwork, error) {
	if len(data) > 16<<20 {
		return preparedArtwork{}, fmt.Errorf("artwork too large")
	}
	detectedMIME := SniffMIME(data)
	if mimeType == "" {
		mimeType = detectedMIME
	}
	if detectedMIME == "" || mimeType != detectedMIME {
		return preparedArtwork{}, fmt.Errorf("unsupported artwork")
	}
	width, height := dimensions(data, mimeType)
	if width <= 0 || height <= 0 {
		return preparedArtwork{}, fmt.Errorf("invalid artwork")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	key := hash + "." + strings.TrimPrefix(strings.TrimPrefix(mimeType, "image/"), "x-")
	return preparedArtwork{hash: hash, key: key, mimeType: mimeType, path: filepath.Join(s.directory, key), width: width, height: height}, nil
}

// prepare 在取得发行/组锁之前完成校验和受管文件 I/O。
func (s *Store) prepare(data []byte, mimeType string) (preparedArtwork, error) {
	prepared, err := s.describe(data, mimeType)
	if err != nil {
		return preparedArtwork{}, err
	}
	err = s.writePrepared(data, &prepared)
	return prepared, err
}

func (s *Store) writePrepared(data []byte, prepared *preparedArtwork) error {
	key := prepared.key
	artworkDirectory := s.directory
	if err := os.MkdirAll(artworkDirectory, 0o750); err != nil {
		return err
	}
	path := filepath.Join(artworkDirectory, key)
	needsWrite := true
	info, statErr := os.Lstat(path)
	if statErr == nil {
		if info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() && info.Size() == int64(len(data)) {
			existing, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			needsWrite = !bytes.Equal(existing, data)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return statErr
	}
	wroteManagedFile := false
	if needsWrite {
		temporary, createErr := os.CreateTemp(artworkDirectory, ".artwork-*")
		if createErr != nil {
			return createErr
		}
		temporaryPath := temporary.Name()
		defer os.Remove(temporaryPath)
		if chmodErr := temporary.Chmod(0o640); chmodErr != nil {
			temporary.Close()
			return chmodErr
		}
		if _, writeErr := temporary.Write(data); writeErr != nil {
			temporary.Close()
			return writeErr
		}
		if closeErr := temporary.Close(); closeErr != nil {
			return closeErr
		}
		if renameErr := os.Rename(temporaryPath, path); renameErr != nil {
			return renameErr
		}
		wroteManagedFile = true
	}
	prepared.wroteManagedFile = wroteManagedFile
	return nil
}

// Remove 删除 Release 的封面关联；文件在没有其他 Release 引用时一并删除。
func (s *Store) Remove(ctx context.Context, executor Executor, releaseID string) error {
	var key sql.NullString
	err := executor.QueryRowContext(ctx, "DELETE FROM release_artworks WHERE release_id=$1::uuid RETURNING storage_key", releaseID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if key.Valid && filepath.Base(key.String) == key.String {
		if cleanupErr := removeUnreferencedArtwork(ctx, executor, filepath.Join(s.directory, key.String), key.String); cleanupErr != nil {
			return fmt.Errorf("remove released artwork: %w", cleanupErr)
		}
	}
	return nil
}

func removeUnreferencedArtwork(ctx context.Context, executor Queryer, path, key string) error {
	var references int
	if err := executor.QueryRowContext(ctx, "SELECT COUNT(*) FROM release_artworks WHERE storage_key=$1", key).Scan(&references); err != nil {
		return err
	}
	if references != 0 {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// dimensions 从图片头部读取宽高；格式不受支持或数据被截断时返回 0, 0。
func dimensions(data []byte, mimeType string) (int, int) {
	switch mimeType {
	case "image/png":
		if len(data) >= 24 {
			return int(binary.BigEndian.Uint32(data[16:20])), int(binary.BigEndian.Uint32(data[20:24]))
		}
	case "image/gif":
		if len(data) >= 10 {
			return int(binary.LittleEndian.Uint16(data[6:8])), int(binary.LittleEndian.Uint16(data[8:10]))
		}
	case "image/jpeg":
		for offset := 2; offset+9 < len(data); {
			if data[offset] != 0xff {
				offset++
				continue
			}
			marker := data[offset+1]
			offset += 2
			if marker == 0xd8 || marker == 0xd9 || marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
				continue
			}
			if offset+2 > len(data) {
				break
			}
			length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			if length < 2 || offset+length > len(data) {
				break
			}
			if (marker >= 0xc0 && marker <= 0xc3 || marker >= 0xc5 && marker <= 0xc7 || marker >= 0xc9 && marker <= 0xcb || marker >= 0xcd && marker <= 0xcf) && length >= 7 {
				return int(binary.BigEndian.Uint16(data[offset+5 : offset+7])), int(binary.BigEndian.Uint16(data[offset+3 : offset+5]))
			}
			offset += length
		}
	case "image/webp":
		if len(data) < 30 {
			break
		}
		switch string(data[12:16]) {
		case "VP8X":
			width := 1 + int(data[24]) + int(data[25])<<8 + int(data[26])<<16
			height := 1 + int(data[27]) + int(data[28])<<8 + int(data[29])<<16
			return width, height
		case "VP8 ":
			if data[23] == 0x9d && data[24] == 0x01 && data[25] == 0x2a {
				return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
			}
		case "VP8L":
			if data[20] == 0x2f {
				width := 1 + int(data[21]) + int(data[22]&0x3f)<<8
				height := 1 + int(data[22]>>6) + int(data[23])<<2 + int(data[24]&0x0f)<<10
				return width, height
			}
		}
	}
	return 0, 0
}

// SniffMIME 按文件头识别受支持的封面格式（jpeg、png、gif、webp），无法识别时返回空串。
func SniffMIME(data []byte) string {
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return "image/gif"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}
