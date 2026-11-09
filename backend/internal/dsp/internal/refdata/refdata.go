// Package refdata 仅供带 refcompare 构建标签的测试定位本机参考数据。
package refdata

import (
	"os"
	"path/filepath"
	"testing"
)

// Dir 返回本机参考数据目录；目录缺失时跳过调用测试，不读取或修改目录内容。
func Dir(t testing.TB) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("定位用户主目录失败：%v", err)
	}
	dir := filepath.Join(home, ".local", "share", "roomusic-reference")
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		t.Skip("本机参考数据目录缺失，跳过 refcompare 对照")
	}
	if err != nil {
		t.Fatalf("检查本机参考数据目录失败：%v", err)
	}
	if !info.IsDir() {
		t.Fatal("本机参考数据路径不是目录")
	}
	return dir
}
