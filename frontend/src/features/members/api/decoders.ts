import { decode as d } from "../../../shared/api";
import { userRoles } from "../../auth";

const memberDecoder = d.object({
  id: d.uuid(),
  username: d.string({ min: 1 }),
  role: d.oneOf(userRoles),
  disabled: d.boolean(),
  created_at: d.timestamp(),
});

// 后端不给成员列表设上限、也不分页（后端缺口），所以不限条数，只限字段长度。
const memberListDecoder = d.object({ items: d.array(memberDecoder, { max: d.unbounded }) });

export const decodeMemberList = d.defineDecoder(memberListDecoder);
export type MemberDTO = d.Infer<typeof memberDecoder>;
export type MemberListDTO = d.Infer<typeof memberListDecoder>;

export const decodeCreatedMember = d.defineDecoder(
  d.object({ id: d.uuid(), username: d.string({ min: 1 }), role: d.oneOf(userRoles) }),
);

/** PATCH /users/{id} 的结果：停用与重新启用都返回目标状态。 */
export const decodeMemberDisabledState = d.defineDecoder(d.object({ disabled: d.boolean() }));

export const decodeSessionsRevoked = d.defineDecoder(d.object({ revoked: d.literal(true) }));
