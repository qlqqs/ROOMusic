# 测试环境中文字体

状态：已完成（2026-11-24）。父任务 `11-24-m8-devices-artists-plugins`，决定 W3。

## 背景

本机没有任何中文字体（没有 `fontconfig` 命令行工具，只有 `libfontconfig1` 与 `fontconfig-config`）。M6 核对文字显示时用的文泉驿微米黑是临时解压进
`temp/` 的，之后已经清掉；无头 Chromium 截图里的中文会变成方块，浏览器测试看不出缺字与截断。

## 需求

- 在用户目录装一个中文字体，供本机 Playwright 的 Chromium 使用；不用 sudo，不改系统目录与系统级默认配置。
- 简体中文页面（`lang="zh-CN"`）用简体字形，不能落到日文字形。
- 前端字体栈已经列出 `"Noto Sans CJK SC"`（`frontend/src/styles/tokens.css`），选这个字体能直接命中。
- 项目文档写明做法，本机环境说明记下安装事实。

## 验收

- 无头 Chromium 渲染 `lang="zh-CN"` 的中文，`CSS.getPlatformFontsForNode` 报告 `Noto Sans CJK SC`；常规与粗体都正常显示，没有方块。
- 文档：`docs/development-environment.md` 有“浏览器测试与截图”一节；`~/workspace/work/environment.md` 记下安装位置。

## 实施记录

- `apt-get download fonts-noto-cjk`（Ubuntu noble，`1:20230817+repack1-3`）后 `dpkg -x` 解到会话临时目录。
- 只装 Sans 的常规与粗体：`~/.local/share/fonts/noto-cjk/NotoSansCJK-{Regular,Bold}.ttc`（另放许可文件）。
- 按语言选字形的规则 `70-fonts-noto-cjk.conf` 复制到 `~/.config/fontconfig/conf.d/`（系统的 `50-user.conf` 会读这里）。
- 验证：Playwright 1.63 的 Chromium，`lang="zh-CN"` 页面，报告 `Noto Sans CJK SC`（`NotoSansCJKsc-Regular`），截图里简体字形、粗体正常。
