import { useState, type FormEvent, type ReactNode } from "react";
import { Button, SettingRow } from "../../../shared/ui";
import type { CurationSourceDTO } from "../api/decoders";
import styles from "./curation-sections.module.css";

type SourceKey = NonNullable<CurationSourceDTO["keys"]>[number];

/** 各源各把密钥的名称与获取方式（键是“源.密钥名”）；没登记的密钥按标识原样显示，没有链接。 */
// alwaysOptional：这把密钥本来就可以不填（标签里已写明），不再另加“不填也能用”。
const keyGuides: Record<string, { label: string; url: string; linkText: string; alwaysOptional?: true; how: (link: ReactNode) => ReactNode }> = {
  "acoustid.application": {
    label: "应用密钥",
    url: "https://acoustid.org/new-application",
    linkText: "注册一个应用",
    how: (link) => <>在 AcoustID 网站上{link}即可免费获得，仅限非商业使用。</>,
  },
  "acoustid.user": {
    label: "用户密钥",
    url: "https://acoustid.org/api-key",
    linkText: "这个页面",
    how: (link) => <>登录 AcoustID 后在{link}生成；2027 年起查询必须带上它。</>,
  },
  "fanarttv.project": {
    label: "项目密钥",
    url: "https://fanart.tv/get-an-api-key/",
    linkText: "申请页面",
    how: (link) => <>在 Fanart.tv 的{link}免费申请。</>,
  },
  "fanarttv.personal": {
    label: "个人密钥（可选）",
    url: "https://fanart.tv/get-an-api-key/",
    linkText: "这个页面",
    alwaysOptional: true,
    how: (link) => <>登录 Fanart.tv 后在{link}取得；填了以后新上传的图片能更早用上。</>,
  },
  "theaudiodb.api_key": {
    label: "API 密钥",
    url: "https://www.theaudiodb.com/free_music_api",
    linkText: "这个页面",
    how: (link) => <>在{link}查看：免费的测试密钥是 123，订阅后可以换成自己的密钥。</>,
  },
};

function KeyDescription({ source, sourceKey }: { source: CurationSourceDTO; sourceKey: SourceKey }) {
  const guide = keyGuides[`${source.id}.${sourceKey.name}`];
  const invalid = sourceKey.status === "invalid" ? <>{source.name} 说这把密钥无效，请换一个。</> : null;
  // 可选的密钥说明不填也能用；Fanart.tv 的项目密钥可选是因为发布版内置了一把。标签里已写“可选”的不再重复。
  const optional =
    sourceKey.optional === true && !sourceKey.configured && guide?.alwaysOptional !== true ? (
      <>{source.id === "fanarttv" && sourceKey.name === "project" ? "ROOMusic 已内置，不填也能用。" : "可选，不填也能用。"}</>
    ) : null;
  if (guide === undefined)
    return (
      <>
        {invalid}
        {optional}
      </>
    );
  const link = (
    <a href={guide.url} target="_blank" rel="noreferrer">
      {guide.linkText}
    </a>
  );
  return (
    <>
      {invalid}
      {optional}
      {guide.how(link)}
    </>
  );
}

/** 一把密钥的名称（如 AcoustID 的用户密钥、Fanart.tv 的项目密钥）。 */
export function keyLabel(sourceId: string, name: string): string {
  return keyGuides[`${sourceId}.${name}`]?.label ?? name;
}

/**
 * 需要密钥的源（AcoustID、Fanart.tv、TheAudioDB）在开关之下、每把密钥一行从属行（照管家的模型密钥）：没有时是密码框加“保存”；
 * 有时只显示末四位，可以更换或清除（清除不另确认）。保存与清除由调用方的 mutation 完成，结果写回源的列表。
 */
export function SourceKeyRow({
  source,
  sourceKey,
  busy,
  onSave,
}: {
  source: CurationSourceDTO;
  sourceKey: SourceKey;
  busy: boolean;
  onSave: (key: string | null, done: () => void) => void;
}) {
  const [draft, setDraft] = useState("");
  const [replacing, setReplacing] = useState(false);
  const editing = !sourceKey.configured || replacing;
  const label = keyLabel(source.id, sourceKey.name);
  const finish = () => {
    setDraft("");
    setReplacing(false);
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const key = draft.trim();
    if (key !== "") onSave(key, finish);
  };

  return (
    <SettingRow label={label} description={<KeyDescription source={source} sourceKey={sourceKey} />} nested plainLabel={!editing}>
      {({ controlId, descriptionId }) =>
        editing ? (
          <form className={styles.inline} onSubmit={submit} aria-label={`${source.name} 的${label}`} noValidate>
            <input
              id={controlId}
              className={styles.input}
              type="password"
              autoComplete="off"
              spellCheck={false}
              placeholder={`${source.name} 的${label}`}
              value={draft}
              aria-describedby={descriptionId}
              onChange={(event) => setDraft(event.target.value)}
            />
            <Button type="submit" variant="primary" busy={busy} disabled={draft.trim() === ""}>
              保存
            </Button>
            {replacing ? (
              <Button disabled={busy} onClick={finish}>
                取消
              </Button>
            ) : null}
          </form>
        ) : (
          <span className={styles.inline}>
            <span className={styles.value}>已保存，末四位 {sourceKey.last_four}</span>
            <Button disabled={busy} onClick={() => setReplacing(true)}>
              更换
            </Button>
            <Button busy={busy} onClick={() => onSave(null, finish)}>
              清除
            </Button>
          </span>
        )
      }
    </SettingRow>
  );
}
