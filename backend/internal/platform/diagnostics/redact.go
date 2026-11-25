// Package diagnostics 提供不含凭据与绝对路径的进程诊断。
package diagnostics

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	uriPattern        = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s<>"']+`)
	credentialPattern = regexp.MustCompile(`(?i)(?:\b(?:[a-z0-9_]*_)?(?:host|hostaddr|dbname|user|password|secret|token|key|authorization|cookie|sslmode|database_url|dsn)["']?\s*[:=]|\b(?:authorization\s*:\s*)?(?:basic|token|bearer)\s+\S+|\bsk-(?:ant-)?[a-z0-9_-]+|[^\s/:@]+:[^\s/@]+@[^\s/]+)`)
	// 只在词首识别绝对路径；保留 MIME 与普通相对路径中的斜杠。空格属于路径直到分隔符。
	pathPattern        = regexp.MustCompile(`(^|[\s=:"'(\[（：])(?:[A-Za-z]:[\\/]|/|\\\\)[^\r\n<>"'|,;)]*`)
	routePattern       = regexp.MustCompile(`^(?:[A-Z]+ )?/api(?:/[a-zA-Z0-9_{}.-]+)*/?$|^(?:[A-Z]+ )?/(?:healthz|readyz|metrics)/?$`)
	clientRoutePattern = regexp.MustCompile(`^/$|^(/(\$[A-Za-z][A-Za-z0-9]*|\$|[a-z]+(-[a-z]+)*))+/?$`)
)

// Redactor 只做词法替换，不在日志路径上访问文件系统。
type Redactor struct{ roots []string }

func NewRedactor(roots ...string) *Redactor {
	clean := make([]string, 0, len(roots))
	for _, root := range roots {
		if filepath.IsAbs(root) {
			clean = append(clean, filepath.Clean(root))
		}
	}
	sort.Slice(clean, func(i, j int) bool { return len(clean[i]) > len(clean[j]) })
	return &Redactor{roots: clean}
}

// Path 对已知根保留相对路径；其它路径仅保留文件名。
func (r *Redactor) Path(path string) string {
	for _, root := range r.roots {
		rel, err := filepath.Rel(root, filepath.Clean(path))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			if rel == "." {
				return safeBase(root)
			}
			return filepath.ToSlash(rel)
		}
	}
	return safeBase(path)
}

// Text 在白名单字段上再次脱敏；无法安全拆分的凭据、带空格路径整体替换。
func (r *Redactor) Text(value string) string {
	return r.text(value, "")
}

func (r *Redactor) text(value, key string) string {
	if len(value) > 4096 {
		return "[omitted]"
	}
	if credentialPattern.MatchString(value) {
		return "[redacted]"
	}
	value = uriPattern.ReplaceAllString(value, "[redacted]")
	if key == "client_route" {
		if clientRoutePattern.MatchString(value) {
			return value
		}
		return "[redacted]"
	}
	if key == "route_template" && (value == "<unmatched>" || routePattern.MatchString(value)) {
		return value
	}
	return pathPattern.ReplaceAllString(value, "[redacted]")
}

func safeBase(path string) string {
	name := filepath.Base(strings.ReplaceAll(path, `\`, "/"))
	if credentialPattern.MatchString(name) {
		return "[redacted]"
	}
	if name == "/" || name == "." || name == "" {
		return "root"
	}
	return name
}
