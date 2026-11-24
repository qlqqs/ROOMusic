// Package buildinfo 只读取构建注入的版本；缺失或无效时返回开发版。
package buildinfo

import "regexp"

var version, commit string
var versionPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type Info struct{ Version, Commit string }

func ValidVersion(value string) bool { return versionPattern.MatchString(value) }

func Current() Info {
	var info Info
	if ValidVersion(version) {
		info.Version = version
	}
	if commitPattern.MatchString(commit) {
		info.Commit = commit
	}
	return info
}

// ProjectURL 是公开项目地址，供要求应用联系地址的数据源识别客户端。
const ProjectURL = "https://github.com/qlqqs/ROOMusic"
