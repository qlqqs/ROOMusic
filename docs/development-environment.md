# 项目开发与测试环境

本文记录 ROOMusic 的开发与测试约定。本机环境说明见 `~/.codex/AGENT.md`；通用工作约定见项目根目录的 `AGENTS.md`。

## 工具链

Go 与 Node 版本由仓库根目录的 `.mise.toml` 固定。进入项目后执行：

```bash
mise trust
mise install
mise exec -- go version
mise exec -- node --version
```

## 工作目录

以下路径均相对于当前项目工作目录：

- `temp/dev/`：本项目开发数据。
- `temp/`：临时文件、合成测试素材与每次测试的部署目录。
- `temp/` 整体加入 Git 忽略规则；临时文件按日期归档到 `temp/archive/YYYY-MM-DD/`。
- `.trellis/tasks/archive/`：Trellis 归档任务只留本机，加入 Git 忽略规则。`task.py archive` 移动任务后，因为归档目录被忽略，会提示并跳过自动提交；
  这时手动提交源任务目录的删除（以及被改写的父子任务 `task.json`）：`git add -A .trellis/tasks/<任务目录>` 后提交。
  spec 与文档引用归档文档时写成不带链接的路径文字，并注明“本机归档”。

## 浏览器测试与截图

Playwright 只用 Chromium。本机缺 Chromium 的系统库（`libatk` 等）时，`apt-get download` 后 `dpkg -x` 解到 `temp/` 下，
运行时用 `LD_LIBRARY_PATH` 指向解出的库目录；对回环地址不走代理（`NO_PROXY=127.0.0.1,localhost`）。

截图与文字显示核对需要中文字体，否则中文显示为方块。不用 sudo 的装法：

```bash
apt-get download fonts-noto-cjk
dpkg -x fonts-noto-cjk_*.deb fonts-noto-cjk
mkdir -p ~/.local/share/fonts/noto-cjk ~/.config/fontconfig/conf.d
cp fonts-noto-cjk/usr/share/fonts/opentype/noto/NotoSansCJK-{Regular,Bold}.ttc ~/.local/share/fonts/noto-cjk/
cp fonts-noto-cjk/usr/share/fontconfig/conf.avail/70-fonts-noto-cjk.conf ~/.config/fontconfig/conf.d/
```

第二个文件按页面语言选字形：页面是 `lang="zh-CN"`，所以用简体字形，不会落到日文字形。前端字体栈里有 `"Noto Sans CJK SC"`，
截图看到的就是它；真实设备上用的是苹方、微软雅黑等系统字体，截图核对不能代替真实设备上的查看。

## Docker 隔离约定

开发项目名使用 `roomusic-compose-dev`；每次集成测试使用 `roomusic-compose-test-` 加随机后缀；生产使用独立项目名。不同项目各自使用网络及目录，开发和测试由 Compose 生成容器名；生产使用可配置的固定容器名。

应用端口（Vite、Go 后端）绑定 `0.0.0.0`，便于局域网设备访问；开发和测试的 PostgreSQL 只绑定 `127.0.0.1`。开发使用独立端口，测试由脚本选取空闲端口。开发、测试与生产的 PostgreSQL 使用同一个钉死镜像。测试容器限制 CPU 与内存，数据库集成和端到端测试串行运行。

仅清理当前测试项目及其临时目录，不执行全局 prune。测试仅使用合成数据，不访问现有实例的配置、凭据或数据。
