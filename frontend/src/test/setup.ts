import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// jsdom 没有实现 <dialog> 的 showModal/show/close：用 open 属性与 close 事件模拟。
// 焦点约束、遮罩和 Esc 关闭的真实行为由 Playwright 冒烟覆盖。
if (typeof HTMLDialogElement.prototype.showModal !== "function") {
  HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.show = HTMLDialogElement.prototype.showModal;
  HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement, returnValue?: string) {
    if (!this.hasAttribute("open")) return;
    this.removeAttribute("open");
    if (returnValue !== undefined) this.returnValue = returnValue;
    this.dispatchEvent(new Event("close"));
  };
}

// jsdom 没有实现 window.scrollTo：路由切换时的滚动恢复会调用它，未桩住会在每次导航后刷出“Not implemented”。
window.scrollTo = () => undefined;

// jsdom 没有实现 Element.scrollIntoView：翻页后滚到结果顶部、跳到某一张介质都会调用它。需要断言调用的测试自己换成 vi.fn()。
Element.prototype.scrollIntoView = () => undefined;

afterEach(() => {
  cleanup();
});
