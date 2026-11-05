// auth 的公开入口：会话生命周期、已登录范围、登录与初始化表单。其他 feature 与 app 只经这里使用，不引用 auth 内部文件。
export {
  invalidAccountMessage,
  MAX_PASSWORD_LENGTH,
  MIN_PASSWORD_LENGTH,
  passwordHint,
  utf8ByteLength,
  validateNewAccount,
  type NewAccountErrors,
  type NewAccountInput,
} from "./account-rules";
export { userRoles, type UserRole, type ViewerDTO } from "./api/decoders";
export { LoginForm } from "./components/login-form";
export { SetupForm } from "./components/setup-form";
export { useLogin, useLogout, useSetupAdmin } from "./hooks/use-session-actions";
export { roleLabels } from "./roles";
export { sanitizeRedirect } from "./session/redirect";
export { createBroadcastSessionChannel, type SessionChannel } from "./session/session-channel";
export {
  SessionProvider,
  useSession,
  useSessionController,
  useViewerScope,
  ViewerScopeProvider,
  viewerQueryKey,
  type ViewerScope,
} from "./session/session-context";
export {
  createSessionController,
  type SessionController,
  type SessionEndReason,
  type SessionNotice,
  type SessionState,
} from "./session/session-controller";
