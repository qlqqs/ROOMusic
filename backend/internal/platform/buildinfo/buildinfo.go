// Package buildinfo 只读取构建注入的版本；缺失或无效时返回开发版。
package buildinfo

import "regexp"

var version, commit string
var versionPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type Info struct{ Version, Commit string }

func Current() Info {
	var info Info
	if versionPattern.MatchString(version) {
		info.Version = version
	}
	if commitPattern.MatchString(commit) {
		info.Commit = commit
	}
	return info
}
