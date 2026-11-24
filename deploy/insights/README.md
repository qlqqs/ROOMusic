# 统计端部署

统计端 `roomusic-insights` 接收各实例的匿名心跳，并公开汇总页。收集的字段与规则见[匿名统计说明](../../docs/insights.md)与 [ADR 0008](../../docs/architecture/adr/0008-instance-insights.md)。

## 启动

在本目录：

```bash
mkdir -p data && sudo chown 1000:1000 data
docker compose up -d --build
```

- 只监听宿主机 `127.0.0.1:18110`，由反向代理对外提供 HTTPS。
- 数据在 `data/instances.json`，每次写入先写临时文件再改名；备份这个文件即可。
- 项目网络固定为 `172.28.181.0/28`，网关 `172.28.181.1`。和宿主机已有网络冲突时，设置 `INSIGHTS_BRIDGE_SUBNET` 与 `INSIGHTS_BRIDGE_GATEWAY`。
  统计端只信任来自网关地址的 `X-Real-IP`（宿主机回环转发进容器的连接以网关为来源），其他来源用 TCP 对端地址限速。

## 反向代理

站点 `insights.roomusic.org`，HTTPS 证书由反向代理申请。把请求转发到 `http://127.0.0.1:18110`，并设置：

```nginx
proxy_set_header Host $host;
proxy_set_header X-Real-IP $remote_addr;
client_max_body_size 8k;
```

DNS 给 `insights.roomusic.org` 加 A 记录指向服务器。使用 Cloudflare 代理时，反向代理收到的 `$remote_addr` 是 Cloudflare 的地址，限速会按 Cloudflare 节点计数；只解析不代理（灰色云朵）时是客户端地址。

## 更新

```bash
git pull && docker compose up -d --build
```
