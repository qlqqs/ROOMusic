import { decode as d } from "../../../shared/api";

export const userRoles = ["admin", "user"] as const;
export type UserRole = (typeof userRoles)[number];

// /auth/me、/auth/login 与 /setup/admin 都返回这一结构。用户 ID 是查看者的标识；用户名唯一，但只用来显示。
const viewerDecoder = d.object({
  id: d.uuid(),
  username: d.string({ min: 1 }),
  role: d.oneOf(userRoles),
});

export const decodeViewer = d.defineDecoder(viewerDecoder);
export type ViewerDTO = d.Infer<typeof viewerDecoder>;

const setupStatusDecoder = d.object({ setup_required: d.boolean() });

export const decodeSetupStatus = d.defineDecoder(setupStatusDecoder);
export type SetupStatusDTO = d.Infer<typeof setupStatusDecoder>;

export const decodeLoggedOut = d.defineDecoder(d.object({ logged_out: d.literal(true) }));

// POST /auth/password 成功：当前会话不变，本人的其他会话已被撤销。
export const decodePasswordChanged = d.defineDecoder(d.object({ password_changed: d.literal(true) }));
