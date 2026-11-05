package governance

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func TestPostgreSQLArtistClaimRefreshesBothGraphsAndRevert(t *testing.T) {
	f := newCorrectionFixture(t)
	source := f.visibleRelease("来源图回归")
	target := f.visibleRelease("目标图回归")
	p := newArtistCorrectionProjection(t, f, source, target)
	p.correct(source, catalog.CorrectionSet)
	scenario := &claimScenario{fixture: f, source: source, target: target, album: source.groupID}
	scenario.hide()
	// missing 由旧夹具模拟；通过目标的真实修正刷新主名票数，不手调图刷新器。
	f.mustCorrect(target.releaseID, f.revisionOf(target.releaseID), map[string]CorrectionChange{"genre": {Mode: catalog.CorrectionSet, Text: "来源已缺失"}})
	p.assertRelease(source, artistProjectionCorrection, "correction", false)
	p.assertRelease(target, artistProjectionScan, "scan", true)
	p.assertProfile(artistProjectionScan)
	_, claimed := scenario.mustClaim(t)
	// 旧发行清掉修正后仍保留 scan 事实，新发行用迁来的 correction 层；scope 不迁移。
	p.assertRelease(source, artistProjectionScan, "scan", false)
	p.assertRelease(target, artistProjectionCorrection, "correction", true)
	p.assertProfile(artistProjectionCorrection)
	scenario.mustRevertClaim(t, claimed.OperationID)
	p.assertRelease(source, artistProjectionCorrection, "correction", false)
	p.assertRelease(target, artistProjectionScan, "scan", true)
	p.assertProfile(artistProjectionScan)
}
