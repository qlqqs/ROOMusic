import { MoreHorizontal, Upload } from "lucide-react";
import { useRef, useState, type ChangeEvent } from "react";
import { Button, ConfirmDialog, FailureNotice, MenuButton, QueryContent, SettingRow, SettingsGroup, Skeleton, StatusTag } from "../../../shared/ui";
import type { InstalledPluginDTO, PluginPreviewDTO } from "../api/plugins";
import { useDeletePlugin, useInstallPlugin, usePlugins, usePreviewPlugin } from "../hooks/use-plugins";
import styles from "./plugins-section.module.css";

const capabilityText: Record<PluginPreviewDTO["capabilities"][number], string> = { cover_art: "封面", lyrics: "歌词", artist_image: "艺人图片", artist_bio: "艺人简介" };
const previewFailures = { invalid_plugin: "这不是可用的插件包：需要只含 manifest.json 与 plugin.wasm 的 zip，清单与导出的函数要对得上。" } as const;

function capabilitiesOf(items: readonly PluginPreviewDTO["capabilities"][number][]): string {
  return items.length === 0 ? "没有可用的能力" : items.map((item) => capabilityText[item]).join("、");
}

function pluginDescription(plugin: InstalledPluginDTO): string {
  const parts = [`${plugin.manifest.version}`, plugin.manifest.author === "" ? null : plugin.manifest.author, capabilitiesOf(plugin.capabilities)];
  if (plugin.disabled_reason === "hash_changed") parts.push("插件文件被改动过，已停用；重新上传确认后才能再用");
  return parts.filter((part) => part !== null).join(" · ");
}

/**
 * 插件（管理员，M8，ADR 0007）：上传 zip 包 → 看清它会提供什么、访问哪些地址、要哪些密钥 → 确认安装。
 * 装好的插件默认不启用，在“外部源”里像内置来源一样打开、填密钥、撤销它的修改。插件在沙箱里运行，只能访问它声明的地址。
 */
export function PluginsSection() {
  const plugins = usePlugins();
  const preview = usePreviewPlugin();
  const remove = useDeletePlugin();
  const fileInput = useRef<HTMLInputElement>(null);
  const [deleting, setDeleting] = useState<InstalledPluginDTO | null>(null);

  function choose(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (file !== undefined) preview.mutate(file);
  }
  return (
    <SettingsGroup
      title="插件"
      action={
        <>
          <input ref={fileInput} type="file" accept=".zip,application/zip" className={styles.file} tabIndex={-1} aria-hidden="true" onChange={choose} />
          <Button icon={<Upload size={16} />} busy={preview.isPending} onClick={() => fileInput.current?.click()}>
            上传插件
          </Button>
        </>
      }
      footer={
        <>
          {preview.isError ? <FailureNotice error={preview.error} context="write" title="插件没有通过检查" messages={previewFailures} /> : null}
        </>
      }
    >
      <QueryContent query={plugins} loading={<Skeleton label="正在加载插件" lines={2} />}>
        {(items) =>
          items.length === 0 ? (
            <p className={styles.empty}>还没有安装插件。插件由第三方提供，补充内置来源没有的封面、歌词与艺人资料；它在沙箱里运行，只能访问自己声明的地址。</p>
          ) : (
            items.map((plugin) => (
              <SettingRow key={plugin.manifest.id} label={plugin.manifest.name} description={pluginDescription(plugin)}>
                {() => (
                  <span className={styles.rowEnd}>
                    {plugin.disabled_reason === "hash_changed" ? <StatusTag>需要重新确认</StatusTag> : plugin.enabled ? <StatusTag>已启用</StatusTag> : null}
                    <MenuButton
                      label={`${plugin.manifest.name} 的更多操作`}
                      iconOnly
                      icon={<MoreHorizontal size={18} />}
                      actions={[{ id: "delete", label: `删除 ${plugin.manifest.name}`, danger: true, onSelect: () => setDeleting(plugin) }]}
                    />
                  </span>
                )}
              </SettingRow>
            ))
          )
        }
      </QueryContent>
      {preview.data === undefined ? null : <InstallDialog key={preview.data.data.preview_token} preview={preview.data.data} onClose={() => preview.reset()} />}
      <ConfirmDialog
        open={deleting !== null}
        title={`删除插件“${deleting?.manifest.name ?? ""}”？`}
        confirmLabel="删除"
        pending={remove.isPending}
        error={remove.isError ? <FailureNotice error={remove.error} context="write" title="插件没有删除" /> : undefined}
        onCancel={() => {
          setDeleting(null);
          remove.reset();
        }}
        onConfirm={() => {
          if (deleting !== null) remove.mutate(deleting.manifest.id, { onSuccess: () => setDeleting(null) });
        }}
      >
        <p>插件会停用并删除。它已经补上的内容不会自动去掉；需要时先在“外部源”里撤销来自它的全部自动修改。</p>
      </ConfirmDialog>
    </SettingsGroup>
  );
}

function InstallDialog({ preview, onClose }: { preview: PluginPreviewDTO; onClose: () => void }) {
  const install = useInstallPlugin();
  const { manifest } = preview;
  return (
    <ConfirmDialog
      open
      title={`安装插件“${manifest.name}”？`}
      confirmLabel="安装"
      pending={install.isPending}
      error={install.isError ? <FailureNotice error={install.error} context="write" /> : undefined}
      onCancel={onClose}
      onConfirm={() => install.mutate(preview.preview_token, { onSuccess: onClose })}
    >
      <dl className={styles.facts}>
        <dt>作者与版本</dt>
        <dd>
          {manifest.author === "" ? "未署名" : manifest.author} · {manifest.version}
        </dd>
        <dt>提供</dt>
        <dd>{capabilitiesOf(preview.capabilities)}</dd>
        <dt>会访问</dt>
        <dd>
          {manifest.upstreams.length === 0 ? "不访问任何地址" : (
            <ul className={styles.list}>
              {manifest.upstreams.map((upstream) => (
                <li key={upstream.id} className={styles.address}>{upstream.base_url}</li>
              ))}
            </ul>
          )}
        </dd>
        <dt>需要的密钥</dt>
        <dd>{manifest.keys.length === 0 ? "不需要" : manifest.keys.map((key) => (key.optional === true ? `${key.name}（可选）` : key.name)).join("、")}</dd>
        <dt>文件指纹</dt>
        <dd className={styles.address}>{preview.sha256.slice(0, 12)}</dd>
      </dl>
      <p className={styles.note}>插件由第三方编写。安装后默认不启用，在“外部源”里打开它才会使用；密钥由服务器代为附在请求上，插件读不到。</p>
    </ConfirmDialog>
  );
}
