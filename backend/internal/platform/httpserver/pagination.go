package httpserver

import (
	"errors"
	"net/http"
	"strconv"
)

// ParsePagination 解析列表接口共用的 page 与 page_size：page 默认 1、至少 1；page_size 默认 defaultPageSize，
// 范围 1 到 maxPageSize。页偏移 (page-1)*pageSize 不能溢出 int，否则传给数据库的偏移会回绕成负数。
// 任何不合法的值都返回错误，调用方写出 400 invalid_pagination。
func ParsePagination(request *http.Request, defaultPageSize, maxPageSize int) (page, pageSize int, err error) {
	page, pageSize = 1, defaultPageSize
	query := request.URL.Query()
	if value := query.Get("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil || page < 1 {
			return 0, 0, errors.New("invalid page")
		}
	}
	if value := query.Get("page_size"); value != "" {
		pageSize, err = strconv.Atoi(value)
		if err != nil || pageSize < 1 || pageSize > maxPageSize {
			return 0, 0, errors.New("invalid page size")
		}
	}
	if page-1 > int(^uint(0)>>1)/pageSize {
		return 0, 0, errors.New("page offset exceeds bound")
	}
	return page, pageSize, nil
}
