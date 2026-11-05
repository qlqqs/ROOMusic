import { Fragment } from "react";
import { Panel } from "../../../shared/ui";
import type { ReleaseDetailDTO } from "../api/decoders";
import type { ReleaseCreditDTO } from "../api/decoders";
import { isFieldCorrected, releaseFacts } from "../format";
import { ArtistNames } from "./artist-names";
import { creditRoleLabel } from "../labels";
import { ManualCorrectionTag } from "./manual-correction-tag";
import styles from "./release-facts.module.css";

type ReleaseFactsProps = {
  release: ReleaseDetailDTO;
  /** 组里有多个版本时区块叫“此版本”（区别于别的版本）；单版本的组没有“别的版本”，叫“发行信息”，页面上不出现版本字样。 */
  versioned: boolean;
};

/**
 * 侧栏：发行信息（没有值的条目不列出，都没有时说明暂无）与“制作人员”（专辑级署名，没有就不显示这个区块）。
 * 曲目自己的署名在曲目行里。
 */
export function ReleaseFacts({ release, versioned }: ReleaseFactsProps) {
  const facts = releaseFacts(release);
  // 按角色归并，角色与姓名都保持接口给出的顺序；艺人就绪时每个名字是链接。
  const groups = new Map<string, ReleaseCreditDTO[]>();
  for (const credit of release.credits) groups.set(credit.role, [...(groups.get(credit.role) ?? []), credit]);
  const credits = Array.from(groups, ([role, entries]) => ({ role, entries }));
  return (
    <>
      <Panel title={versioned ? "此版本" : "发行信息"}>
        {facts.length === 0 ? (
          <p>暂无更多发行信息。</p>
        ) : (
          <dl className={styles.facts}>
            {facts.map((fact) => (
              <Fragment key={fact.label}>
                <dt>{fact.label}</dt>
                <dd>
                  {fact.value}
                  {isFieldCorrected(release.corrected_fields, fact.field) ? (
                    <>
                      {" "}
                      <ManualCorrectionTag />
                    </>
                  ) : null}
                </dd>
              </Fragment>
            ))}
          </dl>
        )}
      </Panel>
      {credits.length === 0 ? null : (
        <Panel title="制作人员">
          <dl className={styles.facts}>
            {credits.map((group) => (
              <Fragment key={group.role}>
                <dt>{creditRoleLabel(group.role)}</dt>
                <dd>
                  {group.entries.map((credit, index) => (
                    <Fragment key={`${credit.name}:${index}`}>
                      {index === 0 ? null : "、"}
                      <ArtistNames text={credit.name} refs={credit.artist_refs} />
                    </Fragment>
                  ))}
                </dd>
              </Fragment>
            ))}
          </dl>
        </Panel>
      )}
    </>
  );
}
