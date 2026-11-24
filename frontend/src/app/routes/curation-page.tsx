import { CurationBatchesSection, CurationExceptionsSection, CurationModelSection, CurationProgressSection, CurationSourcesSection, CurationSwitchSection, PluginsSection, SuppressionsSection, useCurationSettings } from "../../features/curation";
import { useViewerScope } from "../../features/auth";
import { useStewardModels } from "../../features/steward";
import { Page } from "../../shared/ui";
import { useReleaseDataRefresh } from "./use-release-refresh";

/**
 * 自动整理（管理员，M7 E1、E2）：总开关与暂停、后台进度、模型判断（整理用的模型、每日上限与用量）、例外、各外部源与插件（M8）、批次与按批撤销、“不再提出”的标记。
 * 管理员专用，由路由层的 AdminGate 把关。batchPage、markerPage、exceptionPage 来自地址（第 1 页没有该参数）。撤销与采纳之后由 app 决定刷新哪些数据（目录、歌词等）。
 * 当前管理员就是开启者时，模型的不可用原因链接到他的 Agent 设置；他的审查模型与有效的整理模型相同时提示另选一个（U11）。
 */
export function CurationPage({ batchPage, markerPage, exceptionPage }: { batchPage: number; markerPage: number; exceptionPage: number }) {
  const refresh = useReleaseDataRefresh();
  const settings = useCurationSettings();
  const { viewer } = useViewerScope();
  const models = useStewardModels();
  const mine = settings.data?.enabled_by?.id === viewer.id;
  const curationModel = settings.data === undefined ? "" : settings.data.model || (models?.model ?? "");
  const sameReviewModel = mine && models !== undefined && curationModel !== "" && models.reviewModel === curationModel;
  return (
    <Page title="自动整理" description="打开之后，后台把曲库与外部资料对上，自动补封面、歌词与空着的信息；程序拿不准的由模型判断。每一项都能撤销。">
      <CurationSwitchSection />
      <CurationProgressSection settings={settings.data} />
      <CurationModelSection settings={settings.data} mine={mine} sameReviewModel={sameReviewModel} />
      <CurationExceptionsSection page={exceptionPage} mine={mine} refresh={refresh} />
      <CurationSourcesSection settings={settings.data} refresh={refresh} />
      <PluginsSection />
      <CurationBatchesSection page={batchPage} refresh={refresh} />
      <SuppressionsSection page={markerPage} />
    </Page>
  );
}
