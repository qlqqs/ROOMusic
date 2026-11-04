import { act, cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { installFakeApi, installFakeSession, installOverviewApi } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

async function setup(path = "/") {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  installOverviewApi(api);
  renderApp({ path });
  await screen.findByRole("heading", { level: 1, name: path === "/" ? "概览" : "搜索" });
  return screen.getByRole("main");
}

/** jsdom 不做排版：只模拟哪些搜索框可见，不用它证明实际 CSS 断点或布局。 */
function visible(input: HTMLElement) {
  vi.spyOn(input, "getClientRects").mockReturnValue({
    0: new DOMRect(10, 10, 200, 32), length: 1,
    item: (index: number) => index === 0 ? new DOMRect(10, 10, 200, 32) : null,
    [Symbol.iterator]: () => [new DOMRect(10, 10, 200, 32)].values(),
  });
}

function slash(target: HTMLElement, options: KeyboardEventInit = {}) {
  const event = new KeyboardEvent("keydown", { key: "/", bubbles: true, cancelable: true, ...options });
  fireEvent(target, event);
  return event;
}

beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => undefined);
  vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => undefined);
});

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("外壳全局搜索快捷键", () => {
  it("宽屏聚焦可见侧栏搜索框，阻止斜杠输入但不打开菜单", async () => {
    const main = await setup();
    const input = screen.getByRole("combobox", { name: "搜索" });
    visible(input);
    act(() => main.focus());
    expect(slash(main).defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(input);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("窄屏结果页优先聚焦可见的结果页搜索框，不打开菜单", async () => {
    const main = await setup("/search");
    const input = within(main).getByRole("combobox", { name: "搜索" });
    visible(input);
    act(() => main.focus());
    expect(slash(main).defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(input);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("窄屏其他页面无可见搜索框时打开菜单并聚焦菜单里的框", async () => {
    const main = await setup();
    act(() => main.focus());
    expect(slash(main).defaultPrevented).toBe(true);
    const menu = screen.getByRole("dialog", { name: "菜单" });
    expect(document.activeElement).toBe(within(menu).getByRole("combobox", { name: "搜索" }));
  });

  it.each(["input", "textarea", "contenteditable", "contenteditable 子元素", "select"])(
    "焦点在 %s 时不拦截 / 或抢走焦点", async (kind) => {
      const main = await setup();
      visible(screen.getByRole("combobox", { name: "搜索" }));
      const host = document.createElement(kind === "contenteditable" || kind === "contenteditable 子元素" ? "div" : kind);
      let target = host;
      if (kind.startsWith("contenteditable")) {
        host.setAttribute("contenteditable", "true");
        if (kind === "contenteditable 子元素") {
          target = document.createElement("span");
          host.append(target);
        }
        // jsdom 未实现 isContentEditable；浏览器里编辑宿主的后代也继承这一属性。
        Object.defineProperty(target, "isContentEditable", { configurable: true, value: true });
      }
      target.tabIndex = 0;
      main.append(host);
      try {
        act(() => target.focus());
        expect(slash(target).defaultPrevented).toBe(false);
        expect(document.activeElement).toBe(target);
        expect(screen.queryByRole("dialog")).toBeNull();
      } finally {
        host.remove();
      }
    },
  );

  it.each<KeyboardEventInit>([{ ctrlKey: true }, { metaKey: true }, { altKey: true }, { isComposing: true }])(
    "修饰键或 IME 状态 %j 不触发快捷键", async (options) => {
      const main = await setup();
      visible(screen.getByRole("combobox", { name: "搜索" }));
      act(() => main.focus());
      expect(slash(main, options).defaultPrevented).toBe(false);
      expect(document.activeElement).toBe(main);
      expect(screen.queryByRole("dialog")).toBeNull();
    },
  );

  it("其他对话框打开时不跳到其外的搜索框，也不再打开菜单", async () => {
    const main = await setup();
    visible(screen.getByRole("combobox", { name: "搜索" }));
    const dialog = document.createElement("dialog");
    dialog.setAttribute("aria-label", "编辑信息");
    const button = document.createElement("button");
    button.textContent = "保存";
    dialog.append(button);
    main.append(dialog);
    dialog.showModal();
    try {
      act(() => button.focus());
      expect(slash(button).defaultPrevented).toBe(false);
      expect(document.activeElement).toBe(button);
      expect(screen.getAllByRole("dialog")).toHaveLength(1);
      expect(screen.queryByRole("dialog", { name: "菜单" })).toBeNull();
    } finally {
      dialog.close();
      dialog.remove();
    }
  });

  it("已被子控件消费的斜杠事件不会再次处理", async () => {
    const main = await setup();
    visible(screen.getByRole("combobox", { name: "搜索" }));
    act(() => main.focus());
    const event = new KeyboardEvent("keydown", { key: "/", bubbles: true, cancelable: true });
    event.preventDefault();
    fireEvent(main, event);
    expect(document.activeElement).toBe(main);
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
