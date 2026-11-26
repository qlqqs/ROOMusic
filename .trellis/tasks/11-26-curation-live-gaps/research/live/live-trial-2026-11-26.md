# 28 张公开专辑整理试用 · 2026-11-26（真实串行轮已停止）

已在独立库完成程序轮和限信息 oracle 轮。真实串行轮依指令提前停止。已落库 118 次有实报用量的模型调用，输入 472,896、输出 15,358 token。MB 最近观测为 221 次，其中 29 次 HTTP 503。最终真实模型验收留待并发版本重跑。[串行部分快照](real-serial-partial.json) · [五张 oracle 归因](oracle-gaps-2026-11-26.md)

| 轮次 | 符合／完整 | 错连／并／改 | MB 请求／503 | 耗时 |
|---|---|---|---|---|
| program | 15/28、15/28 | 0/0/0 | 67/4 | 184.6 秒 |
| oracle | 23/28、23/28 | 0/0/0 | 211/24 | 1320.4 秒 |

oracle 在 22 分钟时限结束，留有两个排定重试的发行项。[重试快照](oracle-retry.json)

| 专辑 | 程序终态／完整 | 限信息 oracle 终态／完整 |
|---|---|---|
| 唱遊大世界王菲香港演唱會98–99 | release_group／否 | release_group／是 |
| 菲主打 | unlisted／否 | unlisted／否 |
| 菲賣品 | release／是 | release／是 |
| Il canto delle pellicole | release_group／是 | release_group／是 |
| Goodbye | release／是 | release／是 |
| Boys for Pele | unlisted／否 | release_group／是 |
| The Division Bell | release／是 | release／是 |
| Gilgamesh | release／是 | release／是 |
| The Belly of an Architect | release_group／是 | release_group／是 |
| Amadeus: Special Edition: The Director’s Cut | release／是 | release／是 |
| Ambient | unlisted／否 | uncertain／否 |
| M.elle le "Gladiator" | release_group／是 | release_group／是 |
| …I Care Because You Do | unlisted／否 | release_group／是 |
| soundtrack from thirtysomething | release／是 | release／是 |
| Arise | release／否 | release／是 |
| Dive Into Disney | release／是 | release／是 |
| The Bower of Despair | release_group／是 | release_group／是 |
| Powerslave | release／是 | release／是 |
| Gettin’ In Over My Head | release／否 | release／是 |
| Elysian Fields | release／是 | release／是 |
| Alive 2007 | release_group／是 | release_group／是 |
| Music Has the Right to Children | uncertain／否 | release_group／是 |
| The Matrix | uncertain／否 | release_group／是 |
| My Life in the Bush of Ghosts | release_group／是 | release_group／是 |
| 千のナイフ | unlisted／否 | unlisted／否 |
| Mezzanine | uncertain／否 | unlisted／否 |
| Dummy | uncertain／否 | uncertain／否 |
| 浮躁 | unlisted／否 | release_group／是 |
