package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

// rootFingerprinter 在注册 root 内计算来源文件的内容指纹：先重新校验相对路径、containment 与普通文件，
// 再交给 parser 读取。它实现 catalog 定义的 Fingerprinter 端口。
type rootFingerprinter struct{ root registeredRoot }

func (fingerprinter rootFingerprinter) Fingerprint(ctx context.Context, relativePath string) (string, error) {
	identity, err := createTrackSourceIdentity(fingerprinter.root.ID, relativePath)
	if err != nil {
		return "", err
	}
	path := filepath.Join(fingerprinter.root.Path, filepath.FromSlash(identity.RelativePath))
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("fingerprint source is not a regular file")
	}
	if !isWithin(fingerprinter.root.Path, path) {
		return "", errors.New("fingerprint source is outside the registered root")
	}
	return parser.ContentFingerprint(ctx, path)
}

func (service *Service) fingerprinterFor(root registeredRoot) catalog.Fingerprinter {
	if service.fingerprinter != nil {
		return service.fingerprinter(root)
	}
	return rootFingerprinter{root: root}
}

// planRootIdentities 规划 root 的曲目身份，并把规划诊断写入扫描诊断、把计数写入日志。walkComplete 是该 root
// 遍历证据是否完整。planned 为假表示规划本身失败，暂存行没有完整的规划，调用方不得写入；problem 非空表示
// root 应记为不完整（规划失败、有文件或 scope 无法处理，或诊断写入失败）。
func (service *Service) planRootIdentities(ctx context.Context, executor scanExecutor, scanID string, root registeredRoot, walkComplete bool) (planned bool, problem error) {
	plan, planErr := catalog.PlanStagedRoot(ctx, executor, scanID, root.ID, catalog.PlanOptions{WalkComplete: walkComplete, Fingerprinter: service.fingerprinterFor(root)})
	var problems []error
	record := func(kind, relativePath, message string) {
		if err := service.recordDiagnosticWith(ctx, executor, scanID, relativePath, kind, message); err != nil {
			problems = append(problems, fmt.Errorf("record %s diagnostic: %w", kind, err))
		}
	}
	for _, diagnostic := range plan.Diagnostics {
		record(diagnostic.Kind, diagnostic.RelativePath, diagnostic.Message)
	}
	if planErr != nil {
		record("catalog_write_failure", "", "无法规划曲目身份")
		return false, errors.Join(append(problems, fmt.Errorf("plan track identities: %w", planErr))...)
	}
	if plan.ScopeFailures > 0 {
		record("catalog_write_failure", "", "无法整理暂存观察")
		problems = append(problems, errors.New("staged scope could not be organized"))
	}
	if plan.FingerprintFailures > 0 {
		problems = append(problems, errors.New("fingerprint computation failed"))
	}
	service.logIdentityPlan(scanID, root, walkComplete, plan)
	return true, errors.Join(problems...)
}

// logIdentityPlan 每个 root 规划结束时记一条聚合日志，只含计数，不含路径。
func (service *Service) logIdentityPlan(scanID string, root registeredRoot, walkComplete bool, plan catalog.RootPlan) {
	if service.logger == nil {
		return
	}
	service.logger.Info("scan identity resolved", "event", "library.scan.identity_resolved", "scan_run_id", scanID, "library_root_id", root.ID,
		"walk_complete", walkComplete, "matched", plan.Matched, "rebound", plan.Rebound, "ambiguous_keys", plan.AmbiguousKeys,
		"content_replaced", plan.ContentReplaced, "deferred_scopes", plan.DeferredScopes,
		"fingerprints_computed", plan.FingerprintsComputed, "fingerprints_reused", plan.FingerprintsReused,
		"fingerprint_failures", plan.FingerprintFailures, "scope_failures", plan.ScopeFailures)
}
