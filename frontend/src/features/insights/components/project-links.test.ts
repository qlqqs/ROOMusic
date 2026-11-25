import { describe, expect, it } from "vitest";
import { projectLinks } from "./project-links";

const repo = "https://github.com/example/roomusic-fork";
const commit = "b6654580039af4cd484397f8d5d42de14eca3641";
const license = "https://www.gnu.org/licenses/agpl-3.0.html";

describe("关于页的项目链接", () => {
  it.each([repo, `${repo}/`, `${repo}.git`, `${repo}.git/`])("从 %s 推出版本链接和反馈预填项", (source) => {
    const links = projectLinks(source, { version: "0.8.1", commit });
    expect(links).toMatchObject({
      source,
      license,
      releaseNotes: `${repo}/releases/tag/v0.8.1`,
      thirdParty: `${repo}/blob/v0.8.1/THIRD-PARTY-NOTICES.md`,
      commit: `${repo}/commit/${commit}`,
      feedback: `${repo}/issues/new?template=bug_report.yml&version=0.8.1+%28b665458%29`,
    });
  });

  it("开发版本用 commit 定位第三方许可并预填反馈版本", () => {
    expect(projectLinks(repo, { version: null, commit })).toMatchObject({
      releaseNotes: `${repo}/releases`,
      thirdParty: `${repo}/blob/${commit}/THIRD-PARTY-NOTICES.md`,
      commit: `${repo}/commit/${commit}`,
      feedback: `${repo}/issues/new?template=bug_report.yml&version=b665458`,
    });
  });

  it("成员使用发布列表和主分支，反馈链接省略版本", () => {
    expect(projectLinks(repo, null)).toMatchObject({
      releaseNotes: `${repo}/releases`,
      thirdParty: `${repo}/blob/main/THIRD-PARTY-NOTICES.md`,
      commit: null,
      feedback: `${repo}/issues/new?template=bug_report.yml`,
    });
  });

  it("非 GitHub 仓库只提供源代码与许可", () => {
    const source = "https://github.com.example.org/qlqqs/ROOMusic";
    expect(projectLinks(source, { version: "0.8.1", commit })).toEqual({
      source,
      sourceLabel: "github.com.example.org/qlqqs/ROOMusic",
      license,
      releaseNotes: null,
      thirdParty: null,
      commit: null,
      feedback: null,
    });
  });
});
