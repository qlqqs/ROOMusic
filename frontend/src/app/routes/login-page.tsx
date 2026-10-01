import { LoginForm, useSession } from "../../features/auth";
import { AuthLayout } from "./auth-layout";

export function LoginPage() {
  const session = useSession();
  const notice = session.status === "anonymous" ? session.notice : null;
  const username = session.status === "anonymous" ? session.username : null;
  return (
    <AuthLayout title="登录" description="登录后管理你的音乐资料库。">
      <LoginForm notice={notice} initialUsername={username} />
    </AuthLayout>
  );
}
