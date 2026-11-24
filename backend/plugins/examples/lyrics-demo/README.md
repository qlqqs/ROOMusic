# 示例歌词源

从仓库根目录运行以下命令。Go 使用仓库固定版本，`c-shared` 为 WASI 生成带 `_initialize` 的 reactor。产物在 `temp/lyrics-demo/`。

```bash
mkdir -p temp/lyrics-demo
(cd backend/plugins/examples/lyrics-demo && GOOS=wasip1 GOARCH=wasm mise exec -- go build -buildmode=c-shared -o ../../../../temp/lyrics-demo/plugin.wasm .)
cp backend/plugins/examples/lyrics-demo/manifest.json temp/lyrics-demo/
(cd temp/lyrics-demo && python3 -m zipfile -c lyrics-demo.zip manifest.json plugin.wasm)
```

启动上游 mock，给开发后端设置 `ROOMUSIC_UPSTREAM_MOCK_URL`，在自动整理设置中上传并确认 `lyrics-demo.zip`，打开示例源和自动整理。示例清单使用保留示例域名，合成数据由 mock 提供。

- 歌词路由 `/plugins/lyrics-demo/main/lyrics` 按标题“潮汐信箱”和艺人“纸屿”匹配。把 LRCLIB 情景设为 `not_found`，扫描同名、有时长的合成曲目，即可观察插件补词。
- 简介路由 `/plugins/lyrics-demo/main/artist_bio` 返回独奏艺人“纸屿”的中文简介，要求已对应的 MBID 为 `10000000-0000-4000-8000-000000000003`。
- 通过 `/_mock/requests?service=plugins` 查看请求路径与响应码。

完整 ABI 和安装接口见[插件开发说明](../../../../docs/plugins.md)。
