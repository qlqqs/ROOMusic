package stewardtools

import (
	"context"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// BrowseTools 提供登记根内的目录和文件读取。
func BrowseTools(service *library.Service) []tool.Definition {
	const root = `"root_id":` + configID
	directory := definition("list_library_directory", "用 list_library_roots 的 root_id 列出音乐目录中的直接子项及扫描发行归属。path 是根内相对路径，空为根。"+pageDescription,
		tool.RoleAdmin, root+`,"path":{"type":"string","maxLength":4096},`+pageProperties, `"root_id"`,
		func(ctx context.Context, _ tool.Caller, p input) (any, error) {
			items, total, err := service.ListLibraryDirectory(ctx, p.RootID, p.Path, p.Page, p.PageSize)
			if err != nil {
				return nil, browseError(err)
			}
			return map[string]any{"path": p.Path, "items": items, "pagination": map[string]int{"page": p.Page, "page_size": p.PageSize, "total": total}}, nil
		})
	file := definition("read_library_file", "读取音乐目录内一个文件。root_id 来自 list_library_roots，path 是根内相对路径。返回至多 64 KiB 的 CUE、抓轨日志、NFO 或其他支持的文本，或返回音频标签、音频参数、图片尺寸。",
		tool.RoleAdmin, root+`,"path":`+configPath, `"root_id","path"`,
		func(ctx context.Context, _ tool.Caller, p input) (any, error) {
			out, err := service.ReadLibraryFile(ctx, p.RootID, p.Path)
			return out, browseError(err)
		})
	// 为 64 KiB 原文、4 KiB 相对路径的 JSON 转义（每字节最多六字节）与固定字段留空间。
	file.MaxOutputBytes = 416 << 10
	return []tool.Definition{directory, file}
}

func browseError(err error) error {
	switch {
	case errors.Is(err, library.ErrInvalidLibraryPath):
		return &tool.Error{Code: tool.CodeInvalidInput, Message: "path 必须是根内相对路径"}
	case errors.Is(err, library.ErrLibraryPathNotFound):
		return &tool.Error{Code: tool.CodeTargetNotFound, Message: "目标不存在"}
	case errors.Is(err, library.ErrLibraryFileTooLarge):
		return &tool.Error{Code: "too_large", Message: "文本文件超过 64 KiB"}
	case errors.Is(err, library.ErrInvalidLibraryFile):
		return &tool.Error{Code: "invalid_file", Message: "无法解析文件内容"}
	default:
		return err
	}
}
