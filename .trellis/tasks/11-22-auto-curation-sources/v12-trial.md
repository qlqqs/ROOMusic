# V12 真实环境试用：步骤与记录表

状态：未执行。V12 要请求真实的 MusicBrainz、Cover Art Archive、LRCLIB、AcoustID，并用真实模型端点，会产生外部请求与模型费用；AcoustID 还要先注册应用。按项目约定，这些都要 qlqq 确认后再做，不由子代理自行执行。

## 前提

1. AcoustID：在 https://acoustid.org/new-application 注册 ROOMusic 应用，拿到应用密钥。
   - 发布镜像用：`gh secret set ACOUSTID_CLIENT_KEY` 设为仓库密钥（发布流水线已检查它，缺了发布会失败）。
   - 本地试用用：在“设置 › 自动整理 › 资料来源”里把它填为 AcoustID 的密钥，不用重新构建。
2. 模型：开启自动整理的管理员在“设置 › Agent”里配置一个支持工具调用的模型端点。可以另选一个审查模型，避开 U11 的同模型提示。
3. 曲库：用合成曲库，覆盖 U7 的形态：
   - 整轨 CUE；
   - Picard 打过标签（带 MusicBrainz ID）；
   - 流媒体单曲（只有 ISRC，或连 ISRC 也没有，靠指纹）；
   - 标签很差或没有标签；
   - 华语与欧美；
   - 被拆成两个目录的多碟专辑（H）；
   - 同一专辑的两种格式（H 的反例）。

   音频可以用 `scripts/gen-playback-fixtures.sh` 一类的合成方法生成，标签按公开的发行信息写。合成音频对不上 AcoustID 的真实指纹，所以指纹那一项要另放几首可以公开使用的真实录音。
4. 实例：单独起一个 Compose 项目（例如 `roomusic-compose-v12`），数据目录放在 `temp/v12/`，音乐目录只读挂载。不使用 `temp/dev/` 的开发实例，也不用任何现有实例。

## 步骤

1. 首次安装，建管理员。打开自动整理总开关，四个源都打开，填 AcoustID 密钥。
2. 扫描合成曲库，等程序层跑完一轮（“设置 › 自动整理”的进度变成空闲）。
3. 配好模型后，等模型层把待定项处理完，或者达到每日上限。
4. 处理例外：采纳一部分，不采纳一部分。再按批撤销一批，确认被撤销的不再出现。
5. 重扫一次，确认自动层、外部封面、外部歌词、外部对应和发行合并都保持。
6. 停用开启者，或者清除他的密钥，确认后台停止。

## 记录表

| 项目 | 取自 | 结果 |
| --- | --- | --- |
| 发行匹配比例：matched / uncertain / no_match / try_later | `GET /api/v1/admin/curation/progress` 的 `releases` | |
| 补上的封面、歌词、字段数 | 整理记录的各批 | |
| AcoustID 的命中、`ambiguous`、`too_many` 比例（B3） | 匹配记录的方法与原因 | |
| 模型层每批的 token 用量、当天总量 | 进度的 `model.today` | |
| 例外比例；`shelved/unhandled` 的数目（D） | 例外列表、进度的 `model.items` | |
| 曲目字段补占位的命中数；文件名推出的标题被换掉是否符合预期（I） | 整理记录 `track_auto_correction` | |
| 自动合并艺人的数量；有没有把同名的不同人合并（G） | 操作记录 `artist_merge`（来源为自动整理） | |
| 自动并入发行的数量；有没有把同一专辑的两种格式当成两部分（H） | 操作记录 `release_auto_merge` | |
| MusicBrainz 的请求速率不超过每秒 1 次 | 后端日志 | |
| 发现的问题 | | |
