import { MoreHorizontal, Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useMembers } from "../../members";
import { Button, ConfirmDialog, FailureNotice, MenuButton, QueryContent, SettingRow, SettingsGroup, Skeleton, StatusTag, Switch, TextField } from "../../../shared/ui";
import type { LanDeviceDTO, LanDeviceSettingsDTO } from "../api/decoders";
import { useAddLanDevice, useLanDevices, useLanDeviceSettings, usePutLanDeviceGrants, useRemoveLanDevice, useUpdateLanDeviceSettings } from "../hooks/use-lan-devices";
import styles from "./lan-devices-settings.module.css";

const addFailures = { invalid_address: "需要设备的局域网 IP，或这台设备自己的 HTTP 描述地址（例如 http://192.168.1.25:49152/description.xml）。" } as const;

/** 采样率写成“48 kHz”“44.1 kHz”。 */
function rateText(rate: number): string {
  return `${(rate / 1000).toLocaleString("zh-CN", { maximumFractionDigits: 1 })} kHz`;
}

/** 一台设备的说明：型号、在线、格式与上限、音量是否可调、手动添加。 */
function deviceDescription(device: LanDeviceDTO): string {
  const parts = [device.model === "" ? null : device.model, device.online ? "在线" : "离线"];
  const formats = device.capabilities.format_declared ? device.capabilities.stream_formats.map((format) => format.toUpperCase()).join("、") : "没有声明支持的格式，先试 FLAC";
  parts.push(`${formats}，最高 ${rateText(device.capabilities.max_sample_rate)} / ${device.capabilities.max_bit_depth} 位`);
  if (device.capabilities.volume === "fixed") parts.push("音量在设备上调");
  parts.push(device.address);
  return parts.filter((part) => part !== null).join(" · ");
}

/**
 * 设置 › 局域网设备（管理员，M8 U3）：两个开关（发现与使用、账号间共享），找到的设备列表，手动添加，共享关闭时按设备分配成员。
 * 选设备播放在播放器的设备列表里，这里只管哪些设备、给谁用。
 */
export function LanDevicesSettings() {
  const settings = useLanDeviceSettings();
  const update = useUpdateLanDeviceSettings();
  const pending = update.isPending ? update.variables : undefined;
  const value = (key: keyof LanDeviceSettingsDTO, current: boolean) => pending?.[key] ?? current;
  return (
    <>
      <SettingsGroup title="局域网设备" footer={update.isError ? <FailureNotice error={update.error} context="write" title="开关没有改成" /> : undefined}>
        <QueryContent query={settings} loading={<Skeleton label="正在加载设置" lines={2} />}>
          {(data) => (
            <>
              <SettingRow label="使用局域网里的播放设备" description="打开后在局域网里查找支持 UPnP/DLNA 的功放、音箱、电视与播放软件，找到的设备出现在播放器的设备列表里。">
                {({ controlId, descriptionId }) => (
                  <Switch id={controlId} aria-describedby={descriptionId} checked={value("enabled", data.enabled)} disabled={update.isPending} onChange={(enabled) => update.mutate({ enabled })} />
                )}
              </SettingRow>
              <SettingRow label="所有成员共用" description="关掉后，每台设备只给你分配的成员使用；管理员始终能看到全部设备。同一时间一台设备只为一个人播放，别人可以确认后接管。">
                {({ controlId, descriptionId }) => (
                  <Switch id={controlId} aria-describedby={descriptionId} checked={value("shared", data.shared)} disabled={update.isPending} onChange={(shared) => update.mutate({ shared })} />
                )}
              </SettingRow>
            </>
          )}
        </QueryContent>
      </SettingsGroup>
      {settings.data === undefined ? null : <DeviceList settings={settings.data} />}
    </>
  );
}

function DeviceList({ settings }: { settings: LanDeviceSettingsDTO }) {
  const devices = useLanDevices(settings.enabled);
  const remove = useRemoveLanDevice();
  const [adding, setAdding] = useState(false);
  const [granting, setGranting] = useState<LanDeviceDTO | null>(null);
  return (
    <SettingsGroup
      title="设备"
      action={<Button icon={<Plus size={16} />} onClick={() => setAdding(true)} disabled={!settings.enabled}>手动添加</Button>}
      footer={remove.isError ? <FailureNotice error={remove.error} context="write" title="设备没有移除" /> : undefined}
    >
      <QueryContent query={devices} loading={<Skeleton label="正在加载设备" lines={3} />}>
        {(items) =>
          items.length === 0 ? (
            <p className={styles.empty}>
              {settings.enabled
                ? "还没有找到设备。确认设备和服务器在同一个局域网；服务器用 bridge 网络部署时自动查找不到，可以手动添加，并在部署配置里设置 ROOMUSIC_LAN_URL。"
                : "打开“使用局域网里的播放设备”后开始查找。"}
            </p>
          ) : (
            items.map((device) => {
              const actions = [
                ...(settings.shared ? [] : [{ id: "grants", label: `分配 ${device.name} 给成员…`, onSelect: () => setGranting(device) }]),
                ...(device.manual ? [{ id: "remove", label: `移除 ${device.name}`, danger: true, onSelect: () => remove.mutate(device.udn) }] : []),
              ];
              const assigned = settings.shared ? null : device.user_ids.length === 0 ? "只有管理员可用" : `分配给 ${device.user_ids.length} 位成员`;
              return (
                <SettingRow key={device.udn} label={device.name === "" ? "未命名设备" : device.name} description={assigned === null ? deviceDescription(device) : `${deviceDescription(device)} · ${assigned}`}>
                  {() => (
                    <span className={styles.rowEnd}>
                      {device.manual ? <StatusTag>手动添加</StatusTag> : null}
                      {actions.length === 0 ? <span className={styles.menuSpacer} aria-hidden="true" /> : <MenuButton label={`${device.name} 的更多操作`} iconOnly icon={<MoreHorizontal size={18} />} actions={actions} />}
                    </span>
                  )}
                </SettingRow>
              );
            })
          )
        }
      </QueryContent>
      <AddDeviceDialog open={adding} onClose={() => setAdding(false)} />
      <GrantsDialog device={granting} onClose={() => setGranting(null)} />
    </SettingsGroup>
  );
}

function AddDeviceDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const add = useAddLanDevice();
  const [address, setAddress] = useState("");
  const trimmed = address.trim();
  function close() {
    setAddress("");
    add.reset();
    onClose();
  }
  function submit(event?: FormEvent) {
    event?.preventDefault();
    if (trimmed === "" || add.isPending) return;
    add.mutate(trimmed, { onSuccess: close });
  }
  return (
    <ConfirmDialog
      open={open}
      title="手动添加设备"
      confirmLabel="添加"
      pending={add.isPending}
      error={add.isError ? <FailureNotice error={add.error} context="write" messages={addFailures} /> : undefined}
      onCancel={close}
      onConfirm={() => submit()}
    >
      <form onSubmit={submit} className={styles.form}>
        <TextField label="设备地址" value={address} placeholder="192.168.1.25" autoFocus onChange={(event) => setAddress(event.target.value)} />
        <p className={styles.hint}>填设备的局域网 IP；如果设备不响应查找，填它的描述地址（http://IP:端口/…xml）。</p>
      </form>
    </ConfirmDialog>
  );
}

function GrantsDialog({ device, onClose }: { device: LanDeviceDTO | null; onClose: () => void }) {
  return device === null ? null : <GrantsForm key={device.udn} device={device} onClose={onClose} />;
}

function GrantsForm({ device, onClose }: { device: LanDeviceDTO; onClose: () => void }) {
  const members = useMembers();
  const put = usePutLanDeviceGrants();
  const [selected, setSelected] = useState(() => new Set(device.user_ids));
  function toggle(id: string, on: boolean) {
    setSelected((current) => {
      const next = new Set(current);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  }
  return (
    <ConfirmDialog
      open
      title={`哪些成员可以用“${device.name}”`}
      confirmLabel="保存"
      pending={put.isPending}
      error={put.isError ? <FailureNotice error={put.error} context="write" /> : undefined}
      onCancel={onClose}
      onConfirm={() => put.mutate({ udn: device.udn, userIds: [...selected] }, { onSuccess: onClose })}
    >
      <QueryContent query={members} loading={<Skeleton label="正在加载成员" lines={3} />}>
        {(items) => (
          <fieldset className={styles.members}>
            <legend className={styles.hint}>管理员始终可以使用，不用勾选。</legend>
            {items
              .filter((member) => member.role !== "admin" && !member.disabled)
              .map((member) => (
                <label key={member.id} className={styles.member}>
                  <input type="checkbox" checked={selected.has(member.id)} onChange={(event) => toggle(member.id, event.target.checked)} />
                  {member.username}
                </label>
              ))}
          </fieldset>
        )}
      </QueryContent>
    </ConfirmDialog>
  );
}
