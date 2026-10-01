import { SetupForm } from "../../features/auth";
import { AuthLayout } from "./auth-layout";

export function SetupPage() {
  return (
    <AuthLayout title="创建管理员" description="这是第一次使用：创建管理员账号后，用它登录并添加音乐目录。">
      <SetupForm />
    </AuthLayout>
  );
}
