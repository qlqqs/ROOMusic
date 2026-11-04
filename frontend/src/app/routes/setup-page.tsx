import { SetupForm } from "../../features/auth";
import { AuthLayout } from "./auth-layout";

export function SetupPage() {
  return (
    <AuthLayout title="创建管理员" description="第一次使用，先创建一个管理员账号。">
      <SetupForm />
    </AuthLayout>
  );
}
