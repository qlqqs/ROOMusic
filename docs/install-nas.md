# 在 NAS 上安装

ROOMusic 由两个容器组成：应用和数据库（PostgreSQL）。在 NAS 的 Docker 界面里新建一个“项目”（有的叫 Compose、应用），粘贴一份 Compose，填好音乐目录就能启动。
不需要命令行，也不需要运行任何脚本。

## 开始之前

1. **准备一个文件夹**放 ROOMusic 的数据，例如 `docker/roomusic`。数据库、应用数据、备份都放在它下面。
2. **确认音乐所在的共享文件夹**，ROOMusic 只读取，不会修改里面的文件。
3. **查到你的用户 ID（PUID）和组 ID（PGID）**。ROOMusic 用这个身份读写数据文件夹、读取音乐。各家 NAS 第一个用户的默认值：

   | NAS | PUID | PGID |
   | --- | --- | --- |
   | 群晖 | 1026 | 100 |
   | 绿联 | 1000 | 10 |
   | 飞牛 | 1000 | 用 `id` 查 |
   | Unraid | 99 | 100 |
   | TrueNAS | 568 | 568 |

   不是第一个用户、或者想确认时，在 NAS 的终端（SSH）里执行 `id 用户名`，`uid=` 后面是 PUID，`gid=` 后面是 PGID。
   这个用户要对数据文件夹有读写权限、对音乐文件夹有读取权限。

4. **端口**：网页默认用 8090，数据库只在本机的 55439 上。被别的应用占用时，改 `ROOMUSIC_HTTP_PORT`、`ROOMUSIC_DB_PORT`。

## 两种 Compose 文件

| 文件 | 用在 |
| --- | --- |
| [`compose.prod.yaml`](../compose.prod.yaml) 加 [`.env.prod.example`](../.env.prod.example) | 能读取同一文件夹里 `.env` 的平台：群晖、飞牛、Unraid（Compose Manager），以及命令行安装 |
| [`deploy/nas/compose.yaml`](../deploy/nas/compose.yaml) | 只有一个粘贴框、不读 `.env` 的平台：威联通、TrueNAS、绿联。把文件开头标出的几行改成你的路径与 PUID、PGID |

两者装出来的东西完全一样。

## 群晖（DSM 7.2 以上，Container Manager）

1. 在 File Station 里建文件夹，例如 `/volume1/docker/roomusic`。
2. 把 `compose.prod.yaml` 改名为 `docker-compose.yml`，`.env.prod.example` 改名为 `.env`，都上传到这个文件夹。
   群晖的文本编辑器打不开点开头的文件，可以先传成 `env.txt` 编辑好再改名为 `.env`。
3. 编辑 `.env`：`ROOMUSIC_MUSIC_DIR=/volume1/music`（换成你的音乐文件夹），`PUID=1026`、`PGID=100`（换成你的）。
4. Container Manager → 项目 → 新增：项目名 `roomusic`，路径选上面的文件夹，提示已有 `docker-compose.yml` 时选择使用它，完成后启动。
5. 打开 `http://NAS地址:8090`。

注意：群晖自带的 Compose 版本较旧，不能使用 `deploy/production/compose.bridge.yaml`；默认的 host 网络可以直接用。开启了 DSM 防火墙时，放行 8090 端口。

## 飞牛 fnOS

1. 在文件管理里建文件夹，例如 `/vol1/1000/docker/roomusic`，把 `compose.prod.yaml` 与改好的 `.env` 放进去。
2. Docker → Compose → 新增项目：名称 `roomusic`，路径选这个文件夹，选择使用已有的 Compose 文件，构建。
3. 如果启动时报“请在 .env 里填写 ROOMUSIC_MUSIC_DIR”，说明这个版本的飞牛没有读 `.env`，改用 `deploy/nas/compose.yaml`。

## 绿联 UGOS Pro

1. 在文件管理里建数据文件夹。
2. Docker → 项目 → 创建：粘贴 `deploy/nas/compose.yaml`，把开头标出的路径与 PUID、PGID 改成你的，创建并启动。

## 极空间

极空间的 Docker 能否导入 Compose 取决于系统版本。能导入时按“只有粘贴框”的方式使用 `deploy/nas/compose.yaml`；只能用图形向导时，要分别创建数据库与应用两个容器，参数照 `deploy/nas/compose.yaml` 逐项填写，网络都选 host。

## 威联通（Container Station 3）

1. 建共享文件夹或在 `Container` 共享文件夹下建 `roomusic`。用其他共享文件夹时，要开启“高级文件夹权限”并给运行容器的账户读写权限。
2. Container Station → 应用程序 → 创建：粘贴 `deploy/nas/compose.yaml`，路径写完整路径，例如 `/share/Container/roomusic/data`。
3. 威联通不读取 `.env`，所有设置都直接改在粘贴的内容里。

## TrueNAS SCALE（24.10 以上）

1. 先建好数据集，例如 `/mnt/tank/apps/roomusic`。数据集的 ACL 模式设为 Passthrough，否则数据库初始化时改权限会失败。
2. Apps → Discover Apps → 右上角菜单 → Install via YAML：粘贴 `deploy/nas/compose.yaml`，改路径与 `PUID=568`、`PGID=568`（或你的用户）。

## Unraid

Unraid 原生不支持 Compose，先在应用商店装 **Docker Compose Manager** 插件：

1. Docker 页面底部 Compose → Add New Stack，名称 `roomusic`。
2. 编辑 Compose 文件：粘贴 `compose.prod.yaml`；编辑 env 文件：粘贴 `.env.prod.example` 并填写，数据路径建议放在 `/mnt/user/appdata/roomusic`，`PUID=99`、`PGID=100`。
3. Compose Up。

## 国内拉取镜像

数据库镜像来自 Docker Hub，NAS 自带的镜像加速对它有效。应用镜像发布在 `ghcr.io/qlqqs/roomusic`，镜像加速对它无效；拉取很慢或失败时：

- 在 NAS 的 Docker 设置里配置网络代理；或
- 把 `ROOMUSIC_IMAGE`（单文件版本里是 `image:` 一行）改成可用的 ghcr 镜像站地址，标签部分不变。

## 更新

在项目里先“停止”，再“拉取”（或“更新镜像”），然后“启动”（有的平台叫“构建”）。数据都在数据文件夹里，更新不会丢失。
升级前应用会自动备份数据库。

## 常见问题

| 现象 | 处理 |
| --- | --- |
| 数据库容器反复重启，日志说缺少密码文件 | 数据文件夹里的 `postgres/` 已有数据库，但 `secrets/postgres_password` 丢了。从备份或旧文件夹找回这个文件；全新安装时删掉 `postgres/` 重新启动 |
| 网页打开了，扫描时看不到音乐 | 运行身份（PUID、PGID）对音乐文件夹没有读取权限；在 NAS 的共享文件夹权限里给这个用户“只读” |
| 应用日志说数据目录不可写 | 数据文件夹在网络存储上，或者权限被 ACL 拦住；在 NAS 界面里给 PUID 对应的用户读写权限 |
| 8090 端口被占用 | 改 `ROOMUSIC_HTTP_PORT` |
