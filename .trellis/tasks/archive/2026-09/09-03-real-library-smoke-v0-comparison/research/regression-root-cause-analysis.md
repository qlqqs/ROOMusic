# 缺陷复盘：真实音乐对照暴露的元数据与映射断层

## 1. 根因分类

- **B：跨层合同不完整。** V0、current PostgreSQL 和 canonical snapshot 对相同概念使用了
  不同表示：V0 的 `GroupingEvidence.MediumCount` 是组装前估计值，current 导出的是最终
  Medium 数；V0 把抓轨规则来源记作 `rule`，current 记作 `rip_log`；ALAC 在 V0 中以
  `aac` 暴露且缺少 current 已解析的 writer credit。字段同名不等于语义天然等价。
- **D：测试覆盖缺口。** 原合成 fixture 覆盖了分号 credit，却没有覆盖紧凑斜杠、逗号、
  `&`、嵌套分隔和官方别名，也没有把最终 Track artist、逐轨 credits 与 V0 corrected
  输出放进同一个端到端门禁。
- **E：隐含假设。** `applyRipLogEvidence` 把“字段非空”等同于“已有权威值”。目录名先补上
  `source_type`/`media_type` 后，明确 EAC/XLD 证据便无法提升 provenance；实际应区分
  `folder` 补充值与 tag/CUE 等权威来源。
- **E：隐含假设。** 比较器曾把“V0 为空而 current 非空”宽泛解释成合同差异，导致未知
  catalog 等字段也可能被放行；最终只允许 V0 `aac`/current `alac` 的 codec、五类音频事实
  和严格相邻反例约束下的 composer 差异，其余未知差异保持回归。
- **E：隐含假设。** 严格多碟候选曾用父归组目录生成 folder metadata，父目录仅负责归组却
  被误当作首个物理碟的 metadata owner，因而凭空产生 catalog；修复后以排序后的首个物理
  碟目录提供 folder evidence，父目录只在标题缺失时 fallback。

## 2. 先前方案为什么没有直接成功

1. **直接运行 V0 production：** Release Graph 已经生成，但后续 local-evidence
   persistence 因两个独立 V0 runtime 缺陷失败；把失败库截取成基准会混淆图算法与旧运行时。
   最终改为直接调用固定 scanner 核心并写 normalized SQLite。
2. **只看聚合数量：** V0/current 的 Track 数不同，但逐来源核对证明 current 多出的 59 条
   是 V0 已解析却静默未投影的有效物理 Track；单看数量会把 current 的无静默丢失合同误判成
   回归。
3. **首轮 schema 映射：** 原始对照产生大量字段差异，混合了真正回归、current 新能力和
   表示差异。先建立稳定来源键、字段 allowlist 和互斥分类后，才收敛出 97 条真正回归。
4. **首次 rip-log 修复：** owner 已从 `folder` 提升为 `rip_log`，但 canonical evidence
   仍把枚举值的 `CD`/`cd` 当成不同；补上与顶层 source/media 一致的 token 归一后才清零。
5. **首轮“零回归”结论：** 过宽的 current-only 空值分类曾掩盖严格多碟 catalog 差异；删除
   兜底并加入未知 catalog/CUE ISRC 邻近反例后，真实语料暴露出唯一 catalog 回归，最终在
   organizer 的 folder evidence owner 修复，而不是继续扩大比较器忽略规则。

## 3. 预防机制

| 优先级 | 机制 | 具体动作 | 状态 |
| --- | --- | --- | --- |
| P0 | 测试覆盖 | 为艺人/credit 保守拆分、官方别名、固定组合名和稳定展示顺序增加表驱动测试 | 已完成 |
| P0 | owner 测试 | 验证 rip-log 只覆盖 `folder` 补充值，不覆盖 tag/CUE，并替换而非追加决定 | 已完成 |
| P0 | 映射测试 | ALAC current-only composer、V0 stale Medium count、rip-log 来源映射均采用窄条件；邻近反例仍须报回归 | 已完成 |
| P0 | 运行门禁 | V0/current 出现 `current_regression`、未知分类或分类计数不闭合时，Smoke 必须非零退出 | 已完成 |
| P0 | 真实验收 | 每次最终结论从空 PostgreSQL 执行首扫和重扫，并以资产前后摘要证明只读 | 已完成 |
| P1 | 文档 | 把 migration、REST、艺人归一、rip-log 优先级与真实 Smoke 合同写入后端运行规范 | 已完成 |
| P1 | 版本化合同 | canonical snapshot 使用显式版本、稳定图身份、字段 allowlist 和零 `unclassified` 门禁 | 已完成 |

## 4. 系统性扩展

- **相似风险：** genre 多值、codec/container、证据来源与旧 grouping facts 都可能出现
  “名字一样、语义不同”；新增比较字段必须先定义两端 owner、时点和规范化规则。
- **设计改进：** 原始 tag observation、current 选定值和 canonical 对照值保持三个边界；
  parser 不为了对照篡改原始证据，持久化与比较器各自只做其明确拥有的转换。
- **流程改进：** 大语料对照先做来源集合和图闭合，再看字段；所有放行规则必须同时有一个
  正例和至少一个邻近反例，禁止以字段名或空值为宽泛兜底。
- **知识改进：** V0 是业务行为证据，不是当前 runtime/schema 模板；复用其确定性规则时
  必须把 adapter hash 和代码 hash 分开记录。

## 5. 知识沉淀

- [x] 更新 `.trellis/spec/backend/core0-runtime-contracts.md`。
- [x] 在当前任务保存本复盘和脱敏 Smoke 聚合报告。
- [x] 用单元、PostgreSQL 集成和完整真实 Smoke 固化合同。
- [x] 本应用仓库没有 `src/templates/markdown/spec/` 镜像目录，无可同步模板；项目内
  `.trellis/spec/` 是本次唯一规范源。
- [ ] 在 Phase 3.4 与本任务实现一起提交规范更新。
