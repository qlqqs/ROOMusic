# 技术设计

## 初始化与挂载

新增 deploy/production/init.sh，随应用镜像分发。Compose init 服务使用同一镜像，以 root 运行脚本且关闭应用健康检查，完成后退出。postgres 依赖 init 成功，app 依赖 postgres healthy。

init 挂载 secrets、data、backups 和只读 postgres 目录。仅设置 secrets/data/backups 顶层属主权限，不递归修改已有数据。PostgreSQL 官方入口负责自己的数据目录权限。

密码使用 /dev/urandom 的 32 字节转十六进制，临时文件原子重命名写入 secrets/postgres_password。文件属主 1000、权限 0600，目录 0700。首次可由 ROOMUSIC_DB_PASSWORD 指定。既有文件优先复用；显式新密码不同、空文件或丢失文件但数据库非空均报错。PostgreSQL 官方 root 入口通过 POSTGRES_PASSWORD_FILE 读取后传给降权进程。

Compose 默认挂载 ./postgres:/var/lib/postgresql、./data:/data、./backups:/backups、./secrets:/run/secrets。保留 ROOMUSIC_BACKUP_DIR 自定义目录。删除固定容器名和顶层 volumes。

## 应用配置

ROOMUSIC_DATABASE_URL 保留连接地址和用户名；设置 ROOMUSIC_DB_PASSWORD_FILE 时统一由 config.DatabaseURL 读取密码，通过 net/url.UserPassword 正确编码。保留未设置文件时的既有行为；拒绝 URL 自带密码与密码文件同时设置。读取错误不输出密码。服务、备份、恢复、重置密码均已调用该配置入口。

容器入口 pg_isready 仅探测数据库是否接受连接，不需要密码，继续使用无密码地址；真实认证由应用承担。

## 验证与发布边界

固定当前生产 PostgreSQL 镜像。构建本任务专用应用镜像，用覆盖文件限制 CPU/内存、动态端口及独立数据目录，覆盖首次启动、重建、备份恢复和复制目录启动。只清理本任务资源。

新 Compose 需要包含本次改造的新应用镜像，旧版本镜像不包含初始化脚本或文件配置；文档明确配套版本关系，不自动执行降级迁移。
