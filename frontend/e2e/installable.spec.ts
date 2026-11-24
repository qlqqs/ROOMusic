import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";

// 装到主屏幕：生产构建里的 Manifest 与图标能取到，Chromium 自己的安装条件检查没有错误。
test.describe("手机网页可安装", () => {
  test("Manifest 与图标可取到，Chromium 判定可安装", async ({ page, backend }) => {
    await backend.start();
    await page.goto("/");
    await expect(heading(page, "登录")).toBeVisible();

    const manifestHref = await page.locator('link[rel="manifest"]').getAttribute("href");
    expect(manifestHref).toBe("/manifest.json");
    const manifestResponse = await page.request.get(manifestHref ?? "");
    expect(manifestResponse.status()).toBe(200);
    const manifest = (await manifestResponse.json()) as { display: string; start_url: string; icons: { src: string; sizes: string; purpose: string }[] };
    expect(manifest.display).toBe("standalone");
    expect(manifest.start_url).toBe("/");
    expect(manifest.icons.map((icon) => `${icon.sizes} ${icon.purpose}`)).toEqual(["192x192 any", "512x512 any", "512x512 maskable"]);

    const appleIcon = await page.locator('link[rel="apple-touch-icon"]').getAttribute("href");
    for (const src of [...manifest.icons.map((icon) => icon.src), appleIcon ?? ""]) {
      const response = await page.request.get(src);
      expect(response.status(), src).toBe(200);
      expect(response.headers()["content-type"], src).toBe("image/png");
    }

    const cdp = await page.context().newCDPSession(page);
    const { installabilityErrors } = (await cdp.send("Page.getInstallabilityErrors")) as { installabilityErrors: { errorId: string }[] };
    expect(installabilityErrors.map((error) => error.errorId)).toEqual([]);
  });
});
