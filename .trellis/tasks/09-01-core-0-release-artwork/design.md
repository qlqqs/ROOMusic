# Core 0 Release 封面体验技术设计

## 模块边界

artwork discovery 属于 scanner 读取边界，release-level 关联和 DTO 属于 catalog；
受控 data 目录存储和鉴权资源响应属于 backend storage/http 边界。音乐目录只读，
不把图片字节写回源目录或 PostgreSQL。

## 发现与优先级

扫描包含音频内嵌图片和 Release 目录中明确命名的图片文件（例如 `cover`、`folder`、
`front` 的常见扩展名）。候选先验证大小、MIME 和可解码性；同一 Release 内嵌候选
优先于目录候选，候选内部按固定名称、路径排序。失败候选记录 bounded diagnostic，
继续处理音频和其他候选。

## 存储与 API

对图片内容计算 hash，以 `hash + MIME` 幂等写入 ROOMusic data 目录；数据库保存
release、hash、MIME、尺寸、storage key 和来源类型，不保存大图片二进制或原始路径。
新增受鉴权的 resource ID API，返回正确 `Content-Type`、固定缓存语义和 request ID；
不存在、未授权或损坏资源使用稳定安全错误。

## 兼容与回退

现有 Release/Medium/Track DTO 在无 artwork 时保持兼容，前端展示可选 artwork URL。
封面发现、解码或存储失败不改变音频观察、Track 身份或 missing 对账。若图片处理库
不足，保留候选诊断并暂不生成封面，不引入外部下载或索引服务。
