# 最终真实服务验收（2026-11-15）

## 范围与隔离

验收本任务变动涉及的 `account.real.ts`、`artists.real.ts`、`personal.real.ts` 与 `playback-zones.real.ts`，共 15 条用例。使用仓库现有 `scripts/playback-e2e.sh`、固定 mise 工具链、合成媒体、独立 PostgreSQL 和临时编译树；没有读取现有实例数据、配置或凭据，没有修改测试脚本或系统配置。

运行通过 `sg docker -c` 使用既有 docker 用户组权限；浏览器动态库与字体配置在其内部设置，避免组切换清除 `LD_LIBRARY_PATH`。临时目录使用项目内 `temp/r`，隔离服务 UNIX socket 完整路径 101 字节，避免深层目录超过系统限制。

## 实际结果

- 首轮环境验证：15 条均未进入浏览器行为，`sg` 清除了动态库环境变量导致 Chromium 缺库。隔离资源已由脚本清理。
- 第二轮环境验证：深层 TMPDIR 使 UNIX socket 路径超长，服务启动失败，未执行用例；隔离资源已由脚本清理。
- 第一轮行为验证：账户 2 条、艺人 3 条通过；收藏用例失败于隐藏心形未先悬停。随后执行进程收到 SIGTERM（退出 143，来源未确定），未完成剩余用例；只保留已通过结果。精确清理本轮容器、数据卷与临时服务目录，保留服务日志。
- 第二轮行为验证：收藏修复后通过；播放区双标签接手、重启恢复、成员权限隔离、旧队列导入、ALAC/歌词/计次、跨专辑交叉渐变通过。合计 7 通过、3 失败：歌单菜单未悬停、手机空队列无法打开设备列表、信号路径步骤文本拆行后的旧定位器。
- 第三轮定向复验：歌单、真实非零电平化与同流无缝衔接 2 条通过；手机入口已修复并成功远程起播，但流程停在遗漏的“已暂停”旧文案断言，已改为播放按钮。
- 手机完整流程首次复验：暂停、切歌、定位与转到手机均通过；转回电脑时源队列清空触发全屏自动收起，测试仍点击退出动画中的收起按钮而失败。
- 最终手机用例复验：修正上述生命周期断言后通过（1 条，11.2 秒；含隔离构建与清理总耗时 30 秒），覆盖远程起播、进度同步、暂停停声、切歌、定位、双向转移清空源队列、设备离线后接手。日志为 `temp/final-acceptance/real-mobile-lifecycle.log`。
- 最终结果：本轮范围内 15 条真实服务用例全部通过；复用前面 14 条通过记录，最后只定向复验手机用例。测试项目的容器、数据卷和临时服务目录已清理，标签查询确认本轮没有残留容器或数据卷。

## 修复

- `personal.real.ts`：收藏心形和歌单行菜单点击前悬停对应行，与实际交互一致。
- `playback-zones.real.ts`：正常播放以“暂停”按钮、暂停以“播放”按钮显示验证，继续保留原生媒体推进与服务端状态断言；循环按钮使用“列表循环”；播放设置使用 `radiogroup` 与“串流音质”；电平化步骤名称与数值拆行后精确定位名称。
- 手机空队列问题已由真实用例复现并交主检查代理修复；测试坚持通过用户可见入口选择设备，未直接调用内部控制器绕过。
- `playback-zones.real.ts` 的 `transferTo`：队列转移会先清空源队列，全屏因此自动收起；改为等待收起按钮隐藏，避免重复点击正在退出的按钮。实际转移与停声断言保留。

## 命令

环境通过 `sg docker -c 'env ... mise exec -- bash scripts/playback-e2e.sh ...'` 设置：`CI=1`、`NO_PROXY=127.0.0.1,localhost` 与小写同值、`LD_LIBRARY_PATH` 和 `FONTCONFIG_FILE` 指向主工作目录 `temp/ui-review/libs` 中既有浏览器库与字体，`TMPDIR` 指向当前项目 `temp/r`。每轮独立 Compose 名使用 `roomusic-test-uip-final-20261115` 加不同后缀，不复用已有资源。

```bash
mise exec -- bash scripts/playback-e2e.sh \
  e2e/real/artists.real.ts e2e/real/account.real.ts \
  e2e/real/personal.real.ts e2e/real/playback-zones.real.ts

mise exec -- bash scripts/playback-e2e.sh \
  e2e/real/playback-zones.real.ts e2e/real/personal.real.ts

mise exec -- bash scripts/playback-e2e.sh \
  e2e/real/playback-zones.real.ts e2e/real/personal.real.ts \
  --grep '新建歌单|手机远程控制电脑|本机电平化'

mise exec -- bash scripts/playback-e2e.sh \
  e2e/real/playback-zones.real.ts --grep '手机远程控制电脑'

cd frontend
mise exec -- npx eslint e2e/real/personal.real.ts e2e/real/playback-zones.real.ts
mise exec -- npm run typecheck
```

全部真实测试定位器与最终生命周期断言修改后的定向 ESLint 和类型检查通过。完整日志在 `temp/final-acceptance/real-*.log`，失败追踪保存在 `temp/final-acceptance/real-failures/`，不提交临时产物。

## 未覆盖

本轮未扩大到无关真实服务文件；真实测试使用 Chromium 自动化，不代表人工读屏或所有浏览器手工实播。
