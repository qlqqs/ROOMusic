import { decode as d } from "../../../shared/api";

export const userRoles = ["admin", "user"] as const;
export type UserRole = (typeof userRoles)[number];

// /auth/me、/auth/login 与 /setup/admin 都返回这一结构：后端没有给出用户 ID，用户名是唯一标识。
const viewerDecoder = d.object({
  username: d.string({ min: 1 }),
  role: d.oneOf(userRoles),
});

export const decodeViewer = d.defineDecoder(viewerDecoder);
export type ViewerDTO = d.Infer<typeof viewerDecoder>;

const setupStatusDecoder = d.object({ setup_required: d.boolean() });

export const decodeSetupStatus = d.defineDecoder(setupStatusDecoder);
export type SetupStatusDTO = d.Infer<typeof setupStatusDecoder>;

export const decodeLoggedOut = d.defineDecoder(d.object({ logged_out: d.literal(true) }));
