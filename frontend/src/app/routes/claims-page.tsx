import { ClaimsView, type ClaimsSearch } from "../../features/governance";
import { Page } from "../../shared/ui";

/**
 * 待认领：扫描因为证明不了是同一个版本而新建了发行，旧发行不可见，你在它上面做过的修改被留在原处。这里列出这些旧版本和它们留着的修改，
 * 推荐可能接替它们的新版本。管理员专用，由路由层的 AdminGate 把关。页码与选中的版本都来自地址。
 */
export function ClaimsPage({ search }: { search: ClaimsSearch }) {
  return (
    <Page title="待认领" description="扫描新建了发行、而旧发行已经不可见时，你在旧发行上做过的修改（字段修正、所在的专辑、默认版本、问题核对）被留在原处。这里列出它们，并推荐可能接替它们的新版本。">
      <ClaimsView search={search} />
    </Page>
  );
}
