package artistgraph

import "github.com/qlqq/roomusic/backend/internal/catalog/metadata"

// ArtistInputNameCost 给后台限批提供原输入署名成本，不代表最终去重后的事实数。
// 同一raw可包含100个名字；复用权威拆分规则，不把一条credit/证据误当成一个艺人。
// 即使没有有效名字也保留一个输入单位，使有界输入读取可以可靠判定预算耗尽。
func ArtistInputNameCost(raw string) int {
	return max(1, len(metadata.CanonicalArtistNames(raw)))
}
