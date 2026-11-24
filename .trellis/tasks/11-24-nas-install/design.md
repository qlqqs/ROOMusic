# 设计：NAS 一键安装

## Compose

- `app`：加 `user: "${PUID:-1000}:${PGID:-1000}"`；`image: "${ROOMUSIC_IMAGE:-ghcr.io/qlqqs/roomusic}:${ROOMUSIC_VERSION:-latest}"`；其余加固不变。
- `postgres`：`entrypoint` 改为内联 sh（Compose 里 `$` 写 `$$`），执行完准备步骤后 `exec docker-entrypoint.sh postgres`（保持官方入口的降权与初始化）：
  1. `umask 077`；`secrets/postgres_password` 不存在或为空时：若数据库目录（`/var/lib/postgresql/18/docker`）非空，输出“数据库已存在但缺少密码文件 secrets/postgres_password，请从备份恢复该文件”并退出 1；否则生成 32 字节随机十六进制写入。
  2. 对 `secrets/`、`data/`、`backups/` 顶层：属主是 0 时 `chown "$PUID:$PGID"`；密码文件 `chown "$PUID:$PGID"`、`0600`；目录 `0700`。`chown` 失败（网络存储 root_squash）只打印提示，不退出。
  3. 官方入口在降权后还要读密码文件：沿用现有已验证的读取方式（实现时核对官方入口 `file_env` 的执行身份，必要时把文件同时授予 postgres 组读或在入口前导出 `POSTGRES_PASSWORD`；不在日志里输出密码）。
- postgres 额外挂载 `./data:/prep/data`、`${ROOMUSIC_BACKUP_DIR:-./backups}:/prep/backups`，`./secrets` 对 postgres 可写、对 app 只读。
- `PUID`/`PGID` 传给 postgres 的环境变量。

## 单文件版本

`deploy/nas/compose.yaml`：从主文件派生——`ROOMUSIC_MUSIC_DIR` 等写成 `/path/to/music`、`/path/to/roomusic/...` 的绝对路径占位，其余变量写字面默认值；文件头注释写“要改的只有这几行”。
检查脚本：把占位替换成临时目录后，与主文件在相同变量下 `docker compose config` 的结果比较（忽略路径差异）。

## 删除脚本

删 `scripts/production-init.sh`、`scripts/tests/test_production_init.py`；`.env.prod.example`、`compose.prod.yaml` 头部注释、README、部署说明、`release-check.sh` 与规范里对脚本的引用同步去掉。

## 发布

`.github/workflows/release.yml`：`vars.DOCKERHUB_IMAGE` 与 `secrets.DOCKERHUB_USERNAME`/`DOCKERHUB_TOKEN` 都存在时登录 Docker Hub，按摘要推送并在合并清单时加上 Docker Hub 的标签；否则跳过并打提示。

## 测试

扩展 `scripts/tests/test_production_deployment.py`（真实 Docker，临时目录与唯一项目名）：空目录首次安装、模拟各 NAS 属主、密码缺失拒绝、重建复用、0.8.0 升级、单文件版本；Compose v2.20.1 二进制（按版本与 sha256 下载到 `temp/`）跑 `config`。
