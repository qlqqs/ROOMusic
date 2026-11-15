import type { ReleaseCreditDTO, ReleaseMediumDTO } from "./api/decoders";
import { creditDetailLabel, creditRoleLabel } from "./labels";

/** 曲目署名的一组：有细分（乐器或工种）的按细分成组，否则按角色；key 区分组，label 是组名。 */
export type TrackCreditGroup = { key: string; label: string; entries: ReleaseCreditDTO[] };

function groupKeyOf(credit: Pick<ReleaseCreditDTO, "role" | "detail">): string {
  return credit.detail === null ? `role:${credit.role}` : `detail:${credit.detail}`;
}

/** 一首曲目的署名按组归并：组与姓名都保持接口给出的顺序（后端按作词→作曲→编曲→指挥→演奏→制作人排好）。 */
export function groupTrackCredits(credits: readonly ReleaseCreditDTO[]): TrackCreditGroup[] {
  const groups = new Map<string, TrackCreditGroup>();
  for (const credit of credits) {
    const key = groupKeyOf(credit);
    const group = groups.get(key);
    if (group === undefined) groups.set(key, { key, label: credit.detail === null ? creditRoleLabel(credit.role) : creditDetailLabel(credit.detail), entries: [credit] });
    else group.entries.push(credit);
  }
  return [...groups.values()];
}

/** 一组里的人（不计顺序）：两首曲目的同一组人相同，签名就相同。 */
function signatureOf(group: TrackCreditGroup): string {
  return group.entries
    .map((credit) => credit.name)
    .sort()
    .join("\u0000");
}

/** 全部曲目共有的署名：组的 key 集合（曲目行据此不再重复），以及按第一首曲目的顺序排好的组（页头列一次）。 */
export type SharedTrackCredits = { keys: ReadonlySet<string>; groups: readonly TrackCreditGroup[] };

const none: SharedTrackCredits = { keys: new Set(), groups: [] };

/**
 * 一个版本里每一首曲目都有、而且是同一组人的署名（例如整张都是同一位作曲、同一位钢琴）：页头列一次，曲目行只写与它不同的部分。
 * 少于两首曲目时没有“共同”可言，照旧写在曲目行里。
 */
export function sharedTrackCredits(media: readonly Pick<ReleaseMediumDTO, "tracks">[]): SharedTrackCredits {
  const tracks = media.flatMap((medium) => medium.tracks);
  const [first, ...rest] = tracks;
  if (first === undefined || rest.length === 0) return none;
  const candidates = new Map(groupTrackCredits(first.credits).map((group) => [group.key, { group, signature: signatureOf(group) }]));
  for (const track of rest) {
    if (candidates.size === 0) return none;
    const groups = new Map(groupTrackCredits(track.credits).map((group) => [group.key, signatureOf(group)]));
    for (const [key, candidate] of candidates) {
      if (groups.get(key) !== candidate.signature) candidates.delete(key);
    }
  }
  if (candidates.size === 0) return none;
  return { keys: new Set(candidates.keys()), groups: [...candidates.values()].map((candidate) => candidate.group) };
}

/** 曲目行里要写的署名：去掉页头已经列过的组。 */
export function ownTrackCredits(credits: readonly ReleaseCreditDTO[], shared: ReadonlySet<string>): readonly ReleaseCreditDTO[] {
  return shared.size === 0 ? credits : credits.filter((credit) => !shared.has(groupKeyOf(credit)));
}
