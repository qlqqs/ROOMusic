import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
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

/** 密钥的说明：无效时先说无效；去哪里申请只在没填或无效时写（填好了就不用再看）。什么都不用写时是 null。 */
function keyDescription(source: CurationSourceDTO, sourceKey: SourceKey): ReactNode {
  const guide = keyGuides[`${source.id}.${sourceKey.name}`];
  const invalid = sourceKey.status === "invalid";
  // 可选的密钥说明不填也能用；Fanart.tv 的项目密钥可选是因为发布版内置了一把。标签里已写“可选”的不再重复。
  const optional =
    sourceKey.optional === true && !sourceKey.configured && guide?.alwaysOptional !== true
      ? source.id === "fanarttv" && sourceKey.name === "project"
        ? "ROOMusic 已内置，不填也能用。"
        : "可选，不填也能用。"
      : null;
  const how =
    guide === undefined || (sourceKey.configured && !invalid) ? null : (
      guide.how(
        <a href={guide.url} target="_blank" rel="noreferrer">
          {guide.linkText}
        </a>,
      )
    );
  if (!invalid && optional === null && how === null) return null;
  return (
    <>
      {invalid ? <span className={styles.reason}>{source.name} 说这把密钥无效，请换一个。</span> : null}
      {optional}
      {how}
    </>
  );
}

/** 一把密钥的名称（如 AcoustID 的用户密钥、Fanart.tv 的项目密钥）。 */
export function keyLabel(sourceId: string, name: string): string {
  return keyGuides[`${sourceId}.${name}`]?.label ?? name;
}

/**
 * 需要密钥的源（AcoustID、Fanart.tv、TheAudioDB）在开关之下、每把密钥一行从属行：没填时是“未填写”与“填写”，
 * 点了才展开密码框、“保存”与“取消”（与已填时的“更换”同一套）；已填只显示末四位，可以更换或清除（清除不另确认）。
 * 展开时焦点进输入框，取消或保存后回到这一行的按钮。保存与清除由调用方的 mutation 完成，结果写回源的列表。
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
  const [editing, setEditing] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const returnFocus = useRef(false);
  const label = keyLabel(source.id, sourceKey.name);
  useEffect(() => {
    if (editing || !returnFocus.current) return;
    returnFocus.current = false;
    trigger.current?.focus();
  }, [editing]);
  const finish = () => {
    setDraft("");
    returnFocus.current = true;
    setEditing(false);
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const key = draft.trim();
    if (key !== "") onSave(key, finish);
  };
  const description = keyDescription(source, sourceKey);

  return (
    <SettingRow label={label} description={description ?? undefined} nested plainLabel={!editing}>
      {({ controlId, descriptionId }) =>
        editing ? (
          <form className={styles.inline} onSubmit={submit} aria-label={`${source.name} 的${label}`} noValidate>
            <input
              id={controlId}
              className={styles.input}
              type="password"
              autoComplete="off"
              spellCheck={false}
              // 点“填写”或“更换”后直接输入。
              autoFocus
              placeholder={`${source.name} 的${label}`}
              value={draft}
              aria-describedby={descriptionId}
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Escape") finish();
              }}
            />
            <Button type="submit" variant="primary" busy={busy} disabled={draft.trim() === ""}>
              保存
            </Button>
            <Button disabled={busy} onClick={finish}>
              取消
            </Button>
          </form>
        ) : sourceKey.configured ? (
          <span className={styles.inline}>
            <span className={styles.value}>已保存，末四位 {sourceKey.last_four}</span>
            <Button ref={trigger} disabled={busy} onClick={() => setEditing(true)}>
              更换
            </Button>
            <Button busy={busy} onClick={() => onSave(null, finish)}>
              清除
            </Button>
          </span>
        ) : (
          <span className={styles.inline}>
            <span className={styles.value} data-empty="">
              未填写
            </span>
            <Button ref={trigger} aria-label={`填写 ${source.name} 的${label}`} aria-describedby={descriptionId} disabled={busy} onClick={() => setEditing(true)}>
              填写
            </Button>
          </span>
        )
      }
    </SettingRow>
  );
}
