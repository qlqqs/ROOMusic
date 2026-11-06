import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

describe("B5：登录限速", () => {
  it("429 显示登录提示，不跳转、不结束会话、不自动重试，表单仍可提交", async () => {
    const api = installFakeApi();
    installFakeSession(api);
    api.on("POST", "/api/v1/auth/login", () => sampleResponse("errors/too-many-attempts", { "Retry-After": "60" }));
    const { runtime, history } = renderApp({ path: "/members" });
    await screen.findByRole("heading", { level: 1, name: "登录" });
    const state = runtime.session.getSnapshot();
    const generation = runtime.session.currentGeneration();
    await userEvent.type(screen.getByLabelText("用户名"), "curator");
    await userEvent.type(screen.getByLabelText("密码"), "curator-password");
    await userEvent.click(screen.getByRole("button", { name: "登录" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("登录失败次数太多，请过几分钟再试。");
    expect(history.location.pathname).toBe("/login");
    expect(new URLSearchParams(history.location.search).get("redirect")).toBe("/settings/members");
    expect(runtime.session.getSnapshot()).toEqual(state);
    expect(runtime.session.currentGeneration()).toBe(generation);
    expect(screen.queryByText("登录已失效，请重新登录。")).toBeNull();
    expect(screen.getByRole<HTMLInputElement>("textbox", { name: "用户名" }).value).toBe("curator");
    expect(screen.getByLabelText<HTMLInputElement>("密码").value).toBe("curator-password");
    expect(api.requestsTo("POST", "/api/v1/auth/login")).toHaveLength(1);
    const submit = screen.getByRole<HTMLButtonElement>("button", { name: "登录" });
    expect(submit.disabled).toBe(false);
    expect(submit.getAttribute("aria-busy")).not.toBe("true");
    await userEvent.click(submit);
    await screen.findByText("登录失败次数太多，请过几分钟再试。");
    expect(api.requestsTo("POST", "/api/v1/auth/login")).toHaveLength(2);
    expect(history.location.pathname).toBe("/login");
    expect(runtime.session.currentGeneration()).toBe(generation);
  });
});
