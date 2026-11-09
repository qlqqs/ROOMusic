# OPRA 内置耳机校正数据

## 来源与版本

- 上游仓库：https://github.com/opra-project/OPRA
- 消费说明：https://github.com/opra-project/OPRA/blob/main/docs/CONSUMING.md
- 下载地址：https://opra.roonlabs.net/database_v1.jsonl
- 抓取时间（UTC）：2026-11-09T13:28:06Z
- 格式版本：database_v1；镜像未提供独立的提交版本，本次内容由 SHA-256 标识。
- 响应 Date：Mon, 09 Nov 2026 13:28:05 GMT
- 响应 ETag：W/"83d01ac835712bd1862248e779cd4bbb2be191764af1486bb6b4cca922cdd0c8"
- 响应 Last-Modified：未提供
- 原始文件大小：12636756 字节
- 原始文件 SHA-256：5c4cf9e2f71c696bf4fc5b4d1a46c57f31c6d0041552c2146fcd1413596fd210
- 原始记录数：19538

## 裁剪与生成

由 ROOMusic 的 backend/cmd/opra-update 自主解析并裁剪：仅保留厂商名称、产品关联/名称/子类型，以及 parametric_eq 的产品关联、作者、说明、来源链接和参数。移除介绍文字与图片路径，丢弃其他 EQ 类型；保留原始参数值和优先级顺序。记录按 ID 排序，使用无时间戳和文件名的 gzip 压缩。

- 保留：厂商 714，产品 6230，EQ 12594
- 丢弃的非 parametric_eq 记录：0
- 裁剪 JSONL 大小：10828158 字节
- 裁剪 JSONL SHA-256：e5c321218007b7797122d037cd39bfa8ebeaa2251e542fccccb6a3380ff9fe7c
- gzip 大小：1028984 字节
- gzip SHA-256：1e59c500c51f4c45140b55ad535d0f80ad9b2defd8d04c319b27ce381f9f6908
- 映射时被钳位的段数：1（每条 EQ 优先级前 32 段中，每段至多计一次）
- 映射时被钳位的前级增益数：0

裁剪文件保留 8.5 Hz 等原始参数。映射供播放区使用时，频率钳到 10–40000 Hz，有效增益和前级增益钳到 −30～+30 dB，Q 钳到 0.1–30；最多取前 32 段。映射统计与运行时共用 MapEQ，不改写来源事实。

从 backend 运行 go run ./cmd/opra-update 可重新生成。工具只写本目录；运行时只读取内置数据，不联网。SOURCE.md 和 LICENSE 全文与 gzip 一同通过 go:embed 分发。

## 许可与署名

原始数据及本裁剪数据按 CC BY-SA 4.0 分发：https://creativecommons.org/licenses/by-sa/4.0/
数据来自 OPRA 社区与各条 EQ 中标注的作者，Roon Labs 维护镜像。这里明确标注了 ROOMusic 的裁剪改动；再分发或修改本数据须保留来源、署名和相同许可。

浏览界面须显示 OPRA 标识、项目简述和仓库链接；每条配置保留作者、说明与来源链接，作者署名应比 OPRA 标识更醒目。图片由前端单独维护，本工具不下载或写入图片。

- 完整上游 LICENSE 来源：https://raw.githubusercontent.com/opra-project/OPRA/main/LICENSE.md
- LICENSE 大小：21090 字节
- LICENSE SHA-256：0cf6502b115c92674a95529b30b57273a34121549205b5f2570b0c391e11b97f

LICENSE 按上游原文完整保留，其中 Dataset 部分为 CC BY-SA 4.0，Code 部分为上游代码的 MIT 许可；MIT 不替代数据许可。本生成器未复制上游实现代码。
