import { useState, type FormEvent, type ReactNode } from "react";
import { Button, SettingRow } from "../../../shared/ui";
import type { CurationSourceDTO } from "../api/decoders";
import styles from "./curation-sections.module.css";

type SourceKey = NonNullable<CurationSourceDTO["keys"]>[number];

/** 各把密钥的名称与获取方式；没登记的密钥名按标识原样显示，没有链接。 */
const keyGuides: Record<string, { label: string; url: string; how: (link: ReactNode) => ReactNode }> = {
  application: {
    label: "应用密钥",
    url: "https://acoustid.org/new-application",
    how: (link) => <>在 AcoustID 网站上{link}即可免费获得，仅限非商业使用。</>,
  },
  user: {
    label: "用户密钥",
    url: "https://acoustid.org/api-key",
    how: (link) => <>登录 AcoustID 后在{link}生成；2027 年起查询必须带上它。</>,
  },
};

const linkText: Record<string, string> = { application: "注册一个应用", user: "这个页面" };

function KeyDescription({ sourceKey }: { sourceKey: SourceKey }) {
  const guide = keyGuides[sourceKey.name];
  if (guide === undefined) return sourceKey.status === "invalid" ? <>AcoustID 说这把密钥无效，请换一个。</> : null;
  const link = (
    <a href={guide.url} target="_blank" rel="noreferrer">
      {linkText[sourceKey.name]}
    </a>
  );
  return sourceKey.status === "invalid" ? (
    <>AcoustID 说这把密钥无效，请换一个。{guide.how(link)}</>
  ) : (
    guide.how(link)
  );
}

/** 一把密钥的名称（用户密钥；没有内置应用密钥的构建还有应用密钥）。 */
export function keyLabel(name: string): string {
  return keyGuides[name]?.label ?? name;
}

/**
 * 需要密钥的源（AcoustID）在开关之下、每把密钥一行从属行（照管家的模型密钥）：没有时是密码框加“保存”；
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
  const label = keyLabel(sourceKey.name);
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
    <SettingRow label={label} description={<KeyDescription sourceKey={sourceKey} />} nested plainLabel={!editing}>
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
