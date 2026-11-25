// “关于”页的项目链接，都从实例公布的源代码地址推出：改过的版本（ROOMUSIC_SOURCE_URL 指向别的仓库）链接到它自己的仓库。
// 更新说明、反馈、commit 与第三方许可的路径是 GitHub 的写法，源代码地址不在 github.com 上时只给源代码与许可。

export type BuildLine = { version: string | null; commit: string | null };

export type ProjectLinks = {
  source: string;
  /** 源代码地址去掉协议，用作提示文字。 */
  sourceLabel: string;
  license: string;
  releaseNotes: string | null;
  feedback: string | null;
  thirdParty: string | null;
  commit: string | null;
};

const LICENSE_URL = "https://www.gnu.org/licenses/agpl-3.0.html";
const ISSUE_TEMPLATE = "bug_report.yml";

function githubRepository(sourceUrl: string): string | null {
  let url: URL;
  try {
    url = new URL(sourceUrl);
  } catch {
    return null;
  }
  if (url.hostname !== "github.com") return null;
  const [owner, repo] = url.pathname.split("/").filter((part) => part !== "");
  if (owner === undefined || repo === undefined) return null;
  return `https://github.com/${owner}/${repo.replace(/\.git$/, "")}`;
}

/** 版本号与 commit 前 7 位，用在反馈表单的“版本”栏。 */
export function buildText(build: BuildLine): string | null {
  if (build.version === null) return build.commit === null ? null : build.commit.slice(0, 7);
  return build.commit === null ? build.version : `${build.version} (${build.commit.slice(0, 7)})`;
}

/** build 为 null 表示看不到版本（成员）：链接指向最新的更新说明与主分支。 */
export function projectLinks(sourceUrl: string, build: BuildLine | null): ProjectLinks {
  const base = { source: sourceUrl, sourceLabel: sourceUrl.replace(/^https?:\/\//, "").replace(/\/$/, ""), license: LICENSE_URL };
  const repo = githubRepository(sourceUrl);
  if (repo === null) return { ...base, releaseNotes: null, feedback: null, thirdParty: null, commit: null };
  const version = build?.version ?? null;
  const commit = build?.commit ?? null;
  const ref = version === null ? (commit ?? "main") : `v${version}`;
  const feedback = new URL(`${repo}/issues/new`);
  feedback.searchParams.set("template", ISSUE_TEMPLATE);
  const text = build === null ? null : buildText(build);
  if (text !== null) feedback.searchParams.set("version", text);
  return {
    ...base,
    releaseNotes: version === null ? `${repo}/releases` : `${repo}/releases/tag/v${version}`,
    feedback: feedback.toString(),
    thirdParty: `${repo}/blob/${ref}/THIRD-PARTY-NOTICES.md`,
    commit: commit === null ? null : `${repo}/commit/${commit}`,
  };
}
