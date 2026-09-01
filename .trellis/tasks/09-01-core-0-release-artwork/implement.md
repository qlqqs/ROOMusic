# Core 0 Release 封面体验执行计划

1. 盘点现有 Release schema、扫描事务、配置 data 目录和静态资源边界，建立无封面回归基线。
2. 增加 artwork 元数据迁移与受控 storage adapter：hash 幂等、MIME/尺寸字段、目录权限和容量上限。
3. 实现目录图片发现与音频内嵌图片读取，固定候选命名、大小/MIME/解码校验和内嵌优先级。
4. 将 release-level artwork 关联纳入扫描/catalog 窄合同；失败只记诊断，不影响音频图谱提交。
5. 增加鉴权 resource ID API、缓存头和安全错误；禁止返回原始音乐路径。
6. 更新 Release DTO 与前端详情展示，覆盖无图、加载、损坏、401/404 和恢复状态。
7. 增加重复 hash、重复扫描、目录只读、损坏图片和内嵌优先级测试，完成跨层审查。

验证命令：`GOCACHE=/tmp/roomusic-go-cache go test ./...`、`go vet ./...`、`go build ./...`、
前端 `npm run lint`、`npm run typecheck`、`npm run test`、`npm run build`，以及
`docker compose config --quiet`。

回退点：先回退前端展示，再回退 API/DTO，最后回退扫描关联；保留已写入的 hash 内容和元数据以便前向修复。
