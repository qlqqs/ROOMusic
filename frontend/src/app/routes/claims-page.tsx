import { ClaimsView, useReleaseCommands, type ClaimsSearch } from "../../features/governance";
import { Page } from "../../shared/ui";
import { useReleaseDataRefresh } from "./use-release-refresh";

/** 待认领页不跟随版本的移动：认领之后新版本可能换了专辑，结果提示里的“打开这张专辑”带用户去看，页面本身留在待认领。 */
function stayOnPage() {}

/**
 * 待认领：扫描因为证明不了是同一个版本而新建了发行，旧发行不可见，你在它上面做过的修改被留在原处。这里列出这些旧版本和它们留着的修改，
 * 推荐可能接替它们的新版本，也可以搜索任意可见版本；选中后先预览、再认领，认领可以撤销。管理员专用，由路由层的 AdminGate 把关。
 * 页码、选中的旧版本与新版本都来自地址。认领与撤销之后，专辑列表、版本详情与概览的数字一起刷新。
 */
export function ClaimsPage({ search }: { search: ClaimsSearch }) {
  const refresh = useReleaseDataRefresh();
  const commands = useReleaseCommands({ refresh, onMoved: stayOnPage });
  return (
    <Page title="待认领" description="扫描新建了发行、而旧发行已经不可见时，你在旧发行上做过的修改（字段修正、所在的专辑、默认版本、问题核对）被留在原处。这里列出它们，并推荐可能接替它们的新版本；选中后先预览，再把修改迁过去。">
      <ClaimsView search={search} commands={commands} />
    </Page>
  );
}
