---
name: album-curation
description: 调查并整理一张专辑的本地归组与 MusicBrainz 对应。
min_role: admin
---

# 整理一张专辑

1. 调用 `get_release` 和 `get_release_evidence` 找到发行和来源相对路径，用 `list_library_roots` 取得根 ID，然后用 `list_library_directory` 列出相关目录。用 `read_library_file` 读 CUE、抓轨日志、NFO 和音频标签。核实实际有哪些碟和曲目，检查是否混进其他专辑或遗漏另一个碟目录。查看标签的 `inferred_fields`，区分文件原有标签和从文件名推断的值。
2. 先从抓轨日志找光盘目录（TOC）或 Disc ID，从 NFO 和标签找条码、目录号及已有的外部编号。有 MusicBrainz 发行编号时用 `get_music_metadata` 查详情。有条码或目录号时先用 `search_music_metadata` 按 `barcode` 或 `catno` 字段找发行。拿到候选后可用 `get_music_metadata` 的 `media`、`discids` 核对日志里的光盘信息。缺少可查询的编号时按专辑名和艺人搜。网络专辑也可以先查艺人的 MusicBrainz ID，再用 `get_music_metadata` 的 `include: ["releases"]` 查该艺人名下的发行。
3. 用 `get_music_metadata` 读取候选发行的媒体和录音。逐碟核对曲数与顺序，逐首核对时长；CUE 以分轨时间为准。标题相似只能帮助找候选。
4. 文件混放或扫描归错时，先用 `regroup_local_files` 修正归组。受保护的发行先用 `list_operations` 找到原外部对应或人工合并，再用 `revert_operation` 撤销对应的操作。等确认扫描生效后重读发行和目录，再认版本。
5. 证据支持某发行时，调用 `match_release_external` 建立对应。整理链会补能从外部对应得到的字段，识别出的字段由这条流程写入。
6. 工具拒绝写入时读冲突码和具体原因。换一个候选或补查一次相应证据；连续两次遇到同一原因就停下，向用户说明核对结果和卡住的原因。
7. 核对后确认 MusicBrainz 未收录时，说明未收录。已配置联网搜索时，可用 `web_search` 和 `fetch_page` 查艺人、厂牌或发行方的官方页面，先把找到的字段与链接给用户看，获得同意后才用 `correct_release` 或 `correct_track`。年份和厂牌需要来源支持。需要补全艺人资料时，提示用户将艺人交给艺人联网。
8. 无专辑标签的单轨是散曲，只认 MusicBrainz 录音。有专辑标签的单曲仍按专辑流程核对，缺曲时最多认到发行组。
9. 复用同一查询的结果。接近单次运行上限时，说明已查证的文件、候选、已完成的操作和待核实的项目，等用户说“继续”。
