// opra-update 由维护者从 backend 手动运行；仅下载并更新固定的后端内置数据目录。
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dspsettings/opra"
)

const (
	databaseURL = "https://opra.roonlabs.net/database_v1.jsonl"
	licenseURL  = "https://raw.githubusercontent.com/opra-project/OPRA/main/LICENSE.md"
	dataDir     = "internal/dspsettings/opra/data"
)

type download struct {
	data         []byte
	etag         string
	lastModified string
	date         string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if len(os.Args) != 1 {
		return fmt.Errorf("用法：在 backend 目录运行 go run ./cmd/opra-update；不接受输出路径")
	}
	mod, err := os.ReadFile("go.mod")
	if err != nil || !strings.HasPrefix(string(mod), "module github.com/qlqq/roomusic/backend\n") {
		return fmt.Errorf("请从 ROOMusic backend 目录运行")
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	upstream, err := fetch(ctx, client, databaseURL, 128<<20)
	if err != nil {
		return err
	}
	fetchedAt := time.Now().UTC().Format(time.RFC3339)
	catalog, err := opra.ParseUpstream(bytes.NewReader(upstream.data))
	if err != nil {
		return fmt.Errorf("上游数据校验失败，未写入文件：%w", err)
	}
	counts := catalog.Counts()
	if counts.Vendors == 0 || counts.Products == 0 || counts.EQs == 0 {
		return fmt.Errorf("上游数据缺少厂商、产品或 EQ，未写入文件")
	}
	license, err := fetch(ctx, client, licenseURL, 4<<20)
	if err != nil {
		return err
	}
	if !bytes.Contains(license.data, []byte("Creative Commons Attribution-ShareAlike 4.0 International Public")) ||
		!bytes.Contains(license.data, []byte("Section 8 -- Interpretation.")) {
		return fmt.Errorf("上游 LICENSE 未包含预期的 CC BY-SA 4.0 全文，未写入文件")
	}
	var trimmed, compressed bytes.Buffer
	if err := catalog.WriteJSONL(&trimmed); err != nil {
		return err
	}
	if err := catalog.WriteGzip(&compressed); err != nil {
		return err
	}
	clampedBands, clampedPreamps := mappingCounts(catalog)
	upstreamRows := bytes.Count(upstream.data, []byte{'\n'})
	if len(upstream.data) > 0 && upstream.data[len(upstream.data)-1] != '\n' {
		upstreamRows++
	}
	source := fmt.Sprintf(`# OPRA 内置耳机校正数据

## 来源与版本

- 上游仓库：https://github.com/opra-project/OPRA
- 消费说明：https://github.com/opra-project/OPRA/blob/main/docs/CONSUMING.md
- 下载地址：%s
- 抓取时间（UTC）：%s
- 格式版本：database_v1；镜像未提供独立的提交版本，本次内容由 SHA-256 标识。
- 响应 Date：%s
- 响应 ETag：%s
- 响应 Last-Modified：%s
- 原始文件大小：%d 字节
- 原始文件 SHA-256：%x
- 原始记录数：%d

## 裁剪与生成

由 ROOMusic 的 backend/cmd/opra-update 自主解析并裁剪：仅保留厂商名称、产品关联/名称/子类型，以及 parametric_eq 的产品关联、作者、说明、来源链接和参数。移除介绍文字与图片路径，丢弃其他 EQ 类型；保留原始参数值和优先级顺序。记录按 ID 排序，使用无时间戳和文件名的 gzip 压缩。

- 保留：厂商 %d，产品 %d，EQ %d
- 丢弃的非 parametric_eq 记录：%d
- 裁剪 JSONL 大小：%d 字节
- 裁剪 JSONL SHA-256：%x
- gzip 大小：%d 字节
- gzip SHA-256：%x
- 映射时被钳位的段数：%d（每条 EQ 优先级前 32 段中，每段至多计一次）
- 映射时被钳位的前级增益数：%d

裁剪文件保留 8.5 Hz 等原始参数。映射供播放区使用时，频率钳到 10–40000 Hz，有效增益和前级增益钳到 −30～+30 dB，Q 钳到 0.1–30；最多取前 32 段。映射统计与运行时共用 MapEQ，不改写来源事实。

从 backend 运行 go run ./cmd/opra-update 可重新生成。工具只写本目录；运行时只读取内置数据，不联网。SOURCE.md 和 LICENSE 全文与 gzip 一同通过 go:embed 分发。

## 许可与署名

原始数据及本裁剪数据按 CC BY-SA 4.0 分发：https://creativecommons.org/licenses/by-sa/4.0/
数据来自 OPRA 社区与各条 EQ 中标注的作者，Roon Labs 维护镜像。这里明确标注了 ROOMusic 的裁剪改动；再分发或修改本数据须保留来源、署名和相同许可。

浏览界面须显示 OPRA 标识、项目简述和仓库链接；每条配置保留作者、说明与来源链接，作者署名应比 OPRA 标识更醒目。图片由前端单独维护，本工具不下载或写入图片。

- 完整上游 LICENSE 来源：%s
- LICENSE 大小：%d 字节
- LICENSE SHA-256：%x

LICENSE 按上游原文完整保留，其中 Dataset 部分为 CC BY-SA 4.0，Code 部分为上游代码的 MIT 许可；MIT 不替代数据许可。本生成器未复制上游实现代码。
`, databaseURL, fetchedAt, header(upstream.date), header(upstream.etag), header(upstream.lastModified),
		len(upstream.data), sha256.Sum256(upstream.data), upstreamRows,
		counts.Vendors, counts.Products, counts.EQs, upstreamRows-counts.Vendors-counts.Products-counts.EQs,
		trimmed.Len(), sha256.Sum256(trimmed.Bytes()), compressed.Len(), sha256.Sum256(compressed.Bytes()),
		clampedBands, clampedPreamps, licenseURL, len(license.data), sha256.Sum256(license.data))
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}
	for _, file := range []struct {
		name string
		data []byte
	}{
		{"opra-v1.jsonl.gz", compressed.Bytes()},
		{"LICENSE", license.data},
		{"SOURCE.md", []byte(source)},
	} {
		if err := os.WriteFile(filepath.Join(dataDir, file.name), file.data, 0644); err != nil {
			return fmt.Errorf("写入 %s: %w", file.name, err)
		}
	}
	fmt.Print(source)
	return nil
}

func fetch(ctx context.Context, client *http.Client, url string, limit int64) (download, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return download{}, err
	}
	req.Header.Set("User-Agent", "ROOMusic-opra-update/1")
	resp, err := client.Do(req)
	if err != nil {
		return download{}, fmt.Errorf("下载 %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return download{}, fmt.Errorf("下载 %s: HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return download{}, fmt.Errorf("读取 %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		return download{}, fmt.Errorf("%s 超过 %d 字节限制", url, limit)
	}
	return download{data: data, etag: resp.Header.Get("ETag"), lastModified: resp.Header.Get("Last-Modified"), date: resp.Header.Get("Date")}, nil
}

func mappingCounts(c *opra.Catalog) (bands, preamps int) {
	for _, vendor := range c.Vendors() {
		for _, product := range c.Products(vendor.ID) {
			for _, eq := range c.EQs(product.ID) {
				mapped, _ := c.MapEQ(eq.ID)
				bands += mapped.ClampedBands
				if mapped.PreampClamped {
					preamps++
				}
			}
		}
	}
	return bands, preamps
}

func header(value string) string {
	if value == "" {
		return "未提供"
	}
	return value
}
