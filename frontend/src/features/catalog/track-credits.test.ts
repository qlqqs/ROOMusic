import { describe, expect, it } from "vitest";
import type { ReleaseCreditDTO, ReleaseTrackDTO } from "./api/decoders";
import { groupTrackCredits, ownTrackCredits, sharedTrackCredits } from "./track-credits";

type Credit = Pick<ReleaseCreditDTO, "role" | "name"> & { detail?: string | null };
const credit = ({ role, name, detail = null }: Credit): ReleaseCreditDTO => ({ role, name, detail, artist_refs: [] });
/** 只有署名的曲目：sharedTrackCredits 只看 credits。 */
const track = (...credits: Credit[]) => ({ credits: credits.map(credit) }) as unknown as ReleaseTrackDTO;
const medium = (...tracks: ReleaseTrackDTO[]) => ({ tracks });
const labels = (credits: readonly ReleaseCreditDTO[]) => groupTrackCredits(credits).map((group) => `${group.label}:${group.entries.map((entry) => entry.name).join("/")}`);

describe("groupTrackCredits", () => {
  it("有细分的按细分成组，否则按角色；组与姓名保持接口顺序", () => {
    const credits = [
      credit({ role: "composer", name: "甲" }),
      credit({ role: "performer", name: "乙", detail: "piano" }),
      credit({ role: "composer", name: "丙" }),
      credit({ role: "performer", name: "丁", detail: "violin" }),
    ];
    expect(labels(credits)).toEqual(["作曲:甲/丙", "钢琴:乙", "小提琴:丁"]);
  });
});

describe("sharedTrackCredits", () => {
  it("每一首都是同一组人：全部上提，组按第一首的顺序", () => {
    const shared = sharedTrackCredits([
      medium(track({ role: "composer", name: "甲" }, { role: "conductor", name: "乙" }), track({ role: "conductor", name: "乙" }, { role: "composer", name: "甲" })),
      medium(track({ role: "composer", name: "甲" }, { role: "conductor", name: "乙" })),
    ]);
    expect([...shared.keys]).toEqual(["role:composer", "role:conductor"]);
    expect(shared.groups.map((group) => group.label)).toEqual(["作曲", "指挥"]);
  });

  it("同一组人顺序不同也算相同；多一个人或少一个人就不同", () => {
    const sameOrder = sharedTrackCredits([medium(track({ role: "composer", name: "甲" }, { role: "composer", name: "乙" }), track({ role: "composer", name: "乙" }, { role: "composer", name: "甲" }))]);
    expect([...sameOrder.keys]).toEqual(["role:composer"]);
    const extra = sharedTrackCredits([medium(track({ role: "composer", name: "甲" }), track({ role: "composer", name: "甲" }, { role: "composer", name: "乙" }))]);
    expect(extra.keys.size).toBe(0);
    expect(extra.groups).toEqual([]);
  });

  it("部分相同：只上提每一首都有、而且同一组人的组", () => {
    const shared = sharedTrackCredits([
      medium(
        track({ role: "composer", name: "甲" }, { role: "conductor", name: "乙" }, { role: "performer", name: "丙", detail: "piano" }),
        track({ role: "composer", name: "甲" }, { role: "conductor", name: "丁" }, { role: "performer", name: "丙", detail: "piano" }),
        track({ role: "composer", name: "甲" }, { role: "performer", name: "丙", detail: "piano" }),
      ),
    ]);
    expect([...shared.keys]).toEqual(["role:composer", "detail:piano"]);
  });

  it("有一首没有署名、只有一首曲目或没有曲目：不上提", () => {
    expect(sharedTrackCredits([medium(track({ role: "composer", name: "甲" }), track())]).keys.size).toBe(0);
    expect(sharedTrackCredits([medium(track({ role: "composer", name: "甲" }))]).keys.size).toBe(0);
    expect(sharedTrackCredits([medium()]).keys.size).toBe(0);
    expect(sharedTrackCredits([]).keys.size).toBe(0);
  });
});

describe("ownTrackCredits", () => {
  it("去掉页头列过的组，只留这一首自己的；没有共同署名时原样返回", () => {
    const credits = [credit({ role: "composer", name: "甲" }), credit({ role: "conductor", name: "乙" }), credit({ role: "performer", name: "丙", detail: "piano" })];
    expect(ownTrackCredits(credits, new Set(["role:composer", "detail:piano"])).map((entry) => entry.name)).toEqual(["乙"]);
    expect(ownTrackCredits(credits, new Set())).toBe(credits);
  });
});
