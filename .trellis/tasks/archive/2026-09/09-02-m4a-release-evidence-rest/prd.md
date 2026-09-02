# M4A 与 Release 证据 REST 投影

## Goal

补齐 M4A AAC/ALAC 元数据解析及管理员安全 evidence 详情接口

## Requirements

- 解析 M4A AAC/ALAC 标签与可用音频事实，保持缺失字段为空并限制读取范围。
- 扩展 Release 详情与管理员 evidence REST 投影，普通用户仅可见脱敏来源类别和不确定标记。
- 前端 API decoder、列表/详情展示和权限错误状态与后端字段保持一致。

## Acceptance Criteria

- [ ] 合成 AAC/ALAC fixture 可解析 title、artist、duration、codec、采样率和声道等可用事实。
- [ ] 管理员可读取 bounded candidates/reasons，普通用户请求返回 403 且不泄露绝对路径。
- [ ] 前端严格解码新增 DTO，并覆盖 malformed、权限和空 evidence 状态测试。

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.
