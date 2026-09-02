# 自动整理最小闭环裁剪分析

## 结论

自动整理、证据和只读问题可见性不应再拆成三个并列产品能力。它们可以收敛成一个最小闭环：

```text
输入证据 -> 自动整理决定 -> 可用 Release Graph
                         -> 不确定/失败的只读投影
```

不建议删除证据或问题可见性。V0 的 coverage-first 会把中低置信决定直接用于正常图谱；如果不保留最小判断依据，也不提供找到这些决定的入口，系统会把可能错误的结果伪装成确定事实，无法解释、验收或定位归组错误。

建议删除的是完整治理能力和独立工作台，而不是可解释性本身。

## 必须保留

### 自动整理结果

- 稳定的 Release/Medium/Track 形状与当前显示值。
- 普通专辑、多碟、Box/collection leaf、散落文件、CUE 整轨及 CUE+分轨去重等 V0 核心规则。
- 重扫幂等与仅完整成功扫描执行 missing 对账。

### 最小当前证据

字段决定至少保存：

- `field` 与 selected value；
- selected source；
- `confidence`；
- `action=auto_apply|uncertain_apply`；
- 稳定 `rule_id`；
- 只有发生冲突或不确定时才保存必要 raw candidates/reason。

归组决定至少保存 candidate kind、参与的相对目录/来源标识、使用的结构/tag 证据和 issue reason code。普通用户投影不得暴露绝对路径。

### 最小只读可见性

- 复用现有 scan diagnostics API 表达 parse、format、permission、catalog write 等硬失败。
- 从当前字段/归组决定中派生 `uncertain_apply` 列表，不新建跨扫描 issue lifecycle 表。
- 不要求独立“问题工作台”页面；可在现有 Release 列表/详情和管理员扫描区增加问题计数、一个只读筛选入口及 evidence 摘要。

## 可以继续砍掉

- `confirmation_status` 状态机；本任务没有 user/AI confirmation writer，单一 `unconfirmed` 没有业务状态变化。
- 持久化 `correction_path`；界面统一提示“修正外部源文件/标签后重扫”即可，具体 reason 来自 rule/diagnostic。
- metadata overlay、pin、manual review、revert、operation history 与 resolved projection。
- 独立问题表、open/resolved/ignored 生命周期、跨扫描人工状态继承。
- 完整 V0 evidence packet、AI-safe projection、memory/ledger 或为了未来 AI 预留的 payload。
- 单独的大型问题工作台、复杂 faceted filter、批量确认或批量修正交互。

## 为什么不是只保留图谱

- 当前永久合同要求 source evidence 可检查（`.trellis/spec/guides/product-goals.md:104-105`）。
- 已完成 Core 0 PRD 至少要求关键字段保存当前值、来源、是否 inferred、关联 scan run 与观察时间（`.trellis/tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md:81-85`）。
- 当前已有 `track_observations` 与管理员 scan diagnostics API，可演进而无需新建完整治理系统（`backend/migrations/0003_catalog_observations.sql:25-32`、`0004_field_provenance.sql:1-6`、`backend/cmd/roomusic/scans.go:236-259`）。

因此，最小 evidence 与派生只读问题视图是自动整理本身的正确性边界，不是附加功能。
