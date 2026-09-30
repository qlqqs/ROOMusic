package catalog

import (
	"context"
	"testing"
)

// 规划必须有指纹端口：没有它就无法为没有免费指纹的文件计算内容，宁可在触碰数据库之前失败。
func TestPlanStagedRootRequiresFingerprinter(t *testing.T) {
	if _, err := PlanStagedRoot(context.Background(), nil, "scan", "root", PlanOptions{WalkComplete: true}); err == nil {
		t.Fatal("缺少 Fingerprinter 的规划被接受")
	}
}
