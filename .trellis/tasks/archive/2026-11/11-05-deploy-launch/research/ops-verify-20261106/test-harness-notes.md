## 测试执行器修正

B2 首次已成功创建管理员（201），但脚本误认为 setup/admin 同时建立会话，随后 auth/me 返回 401。只读查看 handler 确认初始化接口只创建账号，前端另行登录。这是测试脚本假设错误，不是应用缺陷。随后用原凭据登录返回 200，Cookie 属性为 HttpOnly、SameSite=Lax、非 Secure（只检查属性，不记录值）；auth/me 返回 200。修正临时脚本为显式登录后继续，无仓库改动。

B4 原密码新会话登录、歌单恢复均成功；临时校验脚本把曲目摘要字段误写成 track.id（实际为 track.track_id），导致顺序断言失败。白名单结构核对确认原顺序一致，只修正 /tmp 校验脚本后重跑。不是应用或恢复缺陷。

D 首轮挂载比较直接按 Docker inspect 的数组顺序比较，因正式 app 的两个挂载顺序调换而误报。以 Destination、Source 排序后，各挂载字段完全一致。保留 d-result.json 初次输出与 d-final-result.json 归一化结论。

D 结束时 git status 发现 .gitignore、README.md 被改动；本执行器所有文件写入只在 /tmp 下，未修改、未还原仓库。变更来源未独立核实，按外部/并发变更记录，不宣称仓库最终干净；HEAD 仍为 ca21446。
