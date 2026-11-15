# 内嵌前端

`frontend` 执行 `npm run build` 时把产物写入本目录的 `dist/`，`main.go` 用 `//go:embed web` 把它编进后端二进制。

- `dist/` 被 Git 忽略，不提交；生产镜像在 Dockerfile 的 Node 阶段构建后复制进来。
- 本说明文件保证没有前端产物时 `go build`、`go vet` 仍能编译。此时后端照常启动，前端路径返回“前端未构建”提示，开发时使用 Vite 开发服务器。
