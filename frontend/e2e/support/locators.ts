import type { Page } from "@playwright/test";

/** 页面唯一的 h1：每个页面都以它确认已经到达。 */
export const heading = (page: Page, name: string) => page.getByRole("heading", { level: 1, name });

export const mainNav = (page: Page) => page.getByRole("navigation", { name: "主导航" });

export const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
