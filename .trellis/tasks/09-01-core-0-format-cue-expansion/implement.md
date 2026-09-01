# Core 0 扫描格式与 CUE 扩展执行计划

1. 盘点现有 parser、scanner、迁移和 fixture，固定 FLAC/MP3 身份回归基线。
2. 增加 OGG、Opus、WAV parser adapter 与最小合法/损坏 fixture，统一标签字段、
   filename fallback、来源类型和错误分类。
3. 实现受限 CUE tokenizer/parser：编码处理、单 `FILE`、`TRACK AUDIO`、
   `INDEX 01`，并执行 realpath containment 与虚拟来源身份归一化。
4. 扩展 scanner 分类和 catalog 窄合同；保证 unsupported/parse failure 不阻塞
   其他文件，且不触发不完整扫描的 missing 对账。
5. 增加多碟边界 fixture；仅实现有明确证据的 Medium 规则，不改变既有 Track ID。
6. 更新 REST DTO/前端来源展示（如字段变化确有必要），补充回归测试与中文规范。

验证命令：`GOCACHE=/tmp/roomusic-go-cache go test ./...`、`go vet ./...`、
`go build ./...`、前端 `npm run lint`、`npm run typecheck`、`npm run test`、
`npm run build`，以及 `docker compose config --quiet`。

回退点：parser 可独立回退；迁移与 catalog 写入保持向前兼容，不删除既有来源。
