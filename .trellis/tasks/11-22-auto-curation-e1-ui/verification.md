# 自动整理 E1：验证记录

## 已运行

| 层 | 内容 | 结果 |
| --- | --- | --- |
| 后端集成（BT） | `TestPostgreSQLCurationE1*`（`auto_fields` 的详情与对比、成对标记一起清除、清除后可再写入、404）、`TestPostgreSQLAPIResponseSamples`、`TestPostgreSQLCurationCSuppressions`、`TestPostgreSQLExternal*` | 20 个顶层用例通过；797 个已有样本逐个校验未变，新增 `releases/get-auto` |
| 前端类型与规范 | `npm run typecheck`、`eslint src e2e` | 通过 |
| Vitest 全量 | 188 个文件 | 4557 通过，1 个跳过（已有的大样本用例，需要外部目录） |
| 页面级 | `src/app/curation-settings.test.tsx`（入口与权限、打开要确认、关闭与暂停、失败回滚、进度两种形态、外部源与分组说明、按来源分段撤销、按批撤销与权威总数、标记清除、操作记录的批次筛选与依据、概览的一次性提示）；`release-corrections.test.tsx` 的自动补全标签与编辑对话框 | 通过 |
| 单元 | `src/features/curation/curation.test.ts`（进度轮询、一次性提示的条件与存储不可用、标记与跳过原因的文案） | 通过 |
| 契约 | 十个 `curation/*` 样本与 `releases/get-auto` 往返无损 | 通过 |
| Playwright 冒烟 | `e2e/curation.spec.ts`（1440、375、390 三宽：五组、打开确认框、撤销这一批的对话框，无横向溢出、axe、最小字号；按批次筛选的操作记录 375；成员不请求）；受影响的 `auth`、`governance`、概览与设置用例 | 5 个与 43 个通过 |
| 真实服务 | `e2e/real/curation.real.ts`（经 `scripts/playback-e2e.sh` 的隔离服务与数据库；总开关保持关闭） | 通过 |

Playwright 在本机需要 `temp/ci-browser-libs/root` 里解出的浏览器依赖库（`LD_LIBRARY_PATH`）与对回环地址不走代理（`NO_PROXY=127.0.0.1,localhost`）。

## 没有验证的

- 截图里的中文：本机无头浏览器没有中文字体，1440 与 390 的截图只核对了版式（行、开关、菜单、表格堆叠与换行），文字是方块。文案以页面级测试的断言为准。
- 真实外部服务与打开总开关之后的真实运行：自动化测试不打开总开关（父任务收尾 V12 试用）。
