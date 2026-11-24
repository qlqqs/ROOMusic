# 第三方组件与许可

ROOMusic 本身以 [GNU Affero 通用公共许可证第 3 版](LICENSE)（AGPL-3.0-only）发布。下面列出随源码或生产镜像分发的第三方程序与数据。
Go 与 npm 依赖使用 MIT、BSD、Apache-2.0、ISC、Unlicense 等许可，各自的许可文本随依赖包分发，清单见 `backend/go.mod` 与 `frontend/package-lock.json`。

## 生产镜像里的独立程序

两者都是独立的可执行文件，由 ROOMusic 以子进程调用，不与 ROOMusic 链接。

| 程序 | 版本 | 许可 | 来源 |
| --- | --- | --- | --- |
| FFmpeg | 7.1.1，静态构建，`--enable-gpl --enable-version3`，不含 nonfree 组件 | GPL-3.0-or-later | 构建来自 [mwader/static-ffmpeg](https://github.com/wader/static-ffmpeg)，镜像按摘要固定（见 `deploy/production/Dockerfile`）；源码见 [ffmpeg.org](https://ffmpeg.org/download.html) 与该项目的构建脚本 |
| fpcalc（Chromaprint） | 1.6.2，官方静态构建 | LGPL-2.1-or-later（自身代码为 MIT，内含 FFmpeg 的部分代码） | [acoustid/chromaprint](https://github.com/acoustid/chromaprint/releases/tag/v1.6.2) |

arm64 镜像为 fpcalc 附带 Debian 的 glibc 运行库（LGPL-2.1-or-later），放在 `/opt/fpcalc/lib`。

镜像里的 `/usr/share/doc/roomusic/` 放有 ROOMusic 的许可、本文件，以及 GPL-3.0 与 LGPL-2.1 的全文。

## 源码里的数据

| 数据 | 许可 | 位置 |
| --- | --- | --- |
| OPRA 耳机校正数据（裁剪后） | CC BY-SA 4.0 | `backend/internal/dspsettings/opra/data/`（含 `LICENSE` 与 `SOURCE.md`） |
| OpenCC 繁简对照表 | Apache-2.0 | `backend/internal/catalog/textnorm/data/opencc/` |
| Unicode 汉字拼音数据 | Unicode License V3 | `backend/internal/catalog/internal/artistgraph/data/pinyin/` |

## 外部数据源

自动整理从 MusicBrainz、Cover Art Archive、LRCLIB、AcoustID、Wikidata、Wikimedia Commons、维基百科、Fanart.tv、TheAudioDB 读取资料，
各服务有自己的使用条款；写入曲库的图片与简介保留来源、作者和许可信息。
