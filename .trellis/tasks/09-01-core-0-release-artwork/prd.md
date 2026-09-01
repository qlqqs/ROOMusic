# Core 0 Release 封面体验

## Goal

增加 release-level 目录图与内嵌图发现、受控存储和鉴权展示闭环

## Requirements

- 前置依赖：首个浏览切片和稳定 Release identity 已完成。
- 发现明确命名的同目录图片和受支持音频内嵌图片，使用确定且可测试的默认优先级。
- 图片副本或单一展示衍生只写 ROOMusic data 目录；音乐目录保持只读，PostgreSQL 不保存大图片二进制。
- 以内容 hash 幂等保存，并在 catalog 中关联 release-level artwork 元数据。
- 通过受鉴权的资源 ID 返回正确 MIME 与缓存头，不向客户端暴露原始路径。
- 图片损坏或处理失败只产生诊断，不阻塞音频扫描与图谱更新。

## Acceptance Criteria

- [ ] folder artwork 与内嵌 artwork 的优先级在相同输入重扫时保持确定。
- [ ] 重复内容不会产生重复存储，损坏图片不会阻塞 Release 浏览。
- [ ] 鉴权资源 API 返回正确 MIME/缓存语义且不泄露原始路径。
- [ ] 测试证明音乐目录没有任何写入。

## Out of Scope

- Track-level artwork、外部下载、人工选图、冲突编辑和多尺寸衍生平台。
