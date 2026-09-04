# 真实音乐库 Smoke 结果

> 本报告只保存聚合计数、身份摘要和终态，不包含路径、凭据或逐项媒体 metadata。

## 执行入口

`ROOMUSIC_REAL_LIBRARY_SMOKE=1 ./scripts/real-library-smoke.sh --music-root <真实音乐根> --v0-archive <固定 V0 归档>`

- 运行身份：roomusic-smoke-1788468474-4365185d60d066ba
- V0 归档 SHA-256：fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d
- V0 standalone adapter SHA-256：3b28dc336b0ec507bb3e67bc141f946a596a085d41a94ce0ad026b662e4521c3
- corpus 文件数：399
- corpus 总字节数：8089415701
- corpus 摘要前/后：7e281dadfbba859fb2ed68ce3e0caae186de2c1105ae2ddb9a6e609596d9cac4 / 7e281dadfbba859fb2ed68ce3e0caae186de2c1105ae2ddb9a6e609596d9cac4
- 资产前后是否一致：是
- manifest SHA-256：db64b18d56d0265fddc9ca584d14775ae8ec1fa9a0178979a5d77ac954507ce5

## 扫描终态

| 实现 | 首次终态 | 首次耗时（秒） | 二次终态 | 二次耗时（秒） |
| --- | --- | ---: | --- | ---: |
| V0 standalone corrected | completed | 5 | 不适用 | 不适用 |
| current | succeeded | 5 | succeeded | 5 |

## Canonical 聚合

| 快照 | Release | Medium | Track | File |
| --- | ---: | ---: | ---: | ---: |
| V0 standalone corrected | 68 | 69 | 225 | 284 |
| current A | 68 | 69 | 284 | 284 |
| current B | 68 | 69 | 284 | 284 |

## Current REST 抽查与对照

- current 首轮列表总数：68
- current 二轮列表总数：68
- current A/B canonical 差异数：0
- current 首轮诊断分类：{"cue_reference.missing_index":53,"cue_reference.missing_reference":47}
- current 二轮诊断分类：{"cue_reference.missing_index":53,"cue_reference.missing_reference":47}
- V0/current canonical 差异数：804
- V0/current 差异分类计数：{"capability_gap":49,"intentional_contract_difference":751,"schema_mapping_gap":4}
- current regression：0
- 未分类差异：0

## 处置

- V0 输出标记：v0_release_graph_generated_corrected
- V0 生成方式：standalone_scanner；范围：release_graph_only；degraded=false
- V0 排除范围：local evidence、quality badges、scan diagnostics、production runtime status
- 当前 A/B 幂等差异：已由 canonical comparator 判定为 0
- V0/current 差异分类：全部进入有证据的窄分类；无 current regression 或未知分类
- capability gap：保持在已批准范围外；intentional contract difference：按当前合同接受
- schema mapping gap：按两端字段所有权和生成时点的显式映射处置
- 资产变更：无

## 独立复验

- 最终轮与前一完整成功轮的 V0 canonical、current A/B comparison 和 V0/current
  comparison 摘要一致。
- current snapshot 仅因测试夹具修正改变 `code_hash` 审计身份；删除该字段后的 A/B
  canonical 内容一致，业务结果没有漂移。
- 最终轮退出后，`roomusic-smoke-*` 容器、volume、network 和本轮镜像残留均为 0。
