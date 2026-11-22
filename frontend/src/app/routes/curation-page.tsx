import { CurationBatchesSection, CurationProgressSection, CurationSourcesSection, CurationSwitchSection, SuppressionsSection, useCurationSettings } from "../../features/curation";
import { Page } from "../../shared/ui";
import { useReleaseDataRefresh } from "./use-release-refresh";

/**
 * 自动整理（管理员，M7 E1）：总开关与暂停、后台进度、各外部源、批次与按批撤销、“不再提出”的标记。管理员专用，由路由层的 AdminGate 把关。
 * batchPage 与 markerPage 来自地址（batch_page、marker_page，第 1 页没有该参数）。撤销之后由 app 决定刷新哪些数据（目录、歌词等）。
 */
export function CurationPage({ batchPage, markerPage }: { batchPage: number; markerPage: number }) {
  const refresh = useReleaseDataRefresh();
  const settings = useCurationSettings();
  return (
    <Page title="自动整理" description="打开之后，后台把曲库与外部资料对上，自动补封面、歌词与空着的信息；每一项都能撤销。">
      <CurationSwitchSection />
      <CurationProgressSection settings={settings.data} />
      <CurationSourcesSection settings={settings.data} refresh={refresh} />
      <CurationBatchesSection page={batchPage} refresh={refresh} />
      <SuppressionsSection page={markerPage} />
    </Page>
  );
}
