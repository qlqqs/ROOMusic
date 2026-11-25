import { MoreHorizontal, Plus, TriangleAlert } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { useMembers } from "../../members";
import { Button, ConfirmDialog, FailureNotice, MenuButton, QueryContent, SettingRow, SettingsGroup, Skeleton, StatusTag, Switch, TextField } from "../../../shared/ui";
import type { AirPlayFormatDTO, CastFormatDTO, LanDeviceDTO, LanDeviceSettingsChange, LanDeviceSettingsDTO, SlimProtoFormatDTO } from "../api/decoders";
import { useAddLanDevice, useFinishAirPlayPairing, useLanDevices, useLanDeviceSettings, usePutAirPlayFormat, usePutCastFormat, usePutSlimProtoFormat, usePutLanDeviceGrants, useRemoveLanDevice, useStartAirPlayPairing, useUpdateLanDeviceSettings } from "../hooks/use-lan-devices";
import styles from "./lan-devices-settings.module.css";

const addFailures = {
  upnp: { invalid_address: "需要设备的局域网 IP，或这台设备自己的 HTTP 描述地址（例如 http://192.168.1.25:49152/description.xml）。", upnp_disabled: "UPnP/DLNA 已关闭，先在上面打开。" },
  cast: { invalid_address: "需要设备的局域网 IP，可以带端口（例如 192.168.1.25 或 192.168.1.25:8009）。", cast_disabled: "Google Cast 已关闭，先在上面打开。" },
  airplay: { invalid_address: "需要设备的局域网 IP，可以带端口（例如 192.168.1.25 或 192.168.1.25:7000）。", airplay_disabled: "AirPlay 已关闭，先在上面打开。" },
} as const;

type ManualKind = keyof typeof addFailures;

const kindLabels = { upnp: "UPnP/DLNA", cast: "Google Cast", airplay: "AirPlay", slimproto: "SlimProto" } as const;
const codecLabels = { mp3: "MP3", aac: "AAC", flac: "FLAC" } as const;

/** AirPlay 精确对时（PTP）不可用的原因码。 */
const timingReasons: Record<string, string> = {
  port_in_use: "对时端口（UDP 319/320）被本机其他程序占用",
  clock_unavailable: "对时服务没有运行",
  missing_airplay_service: "设备没有公告 AirPlay 2 服务",
};

/** 采样率写成“48 kHz”“44.1 kHz”。 */
function rateText(rate: number): string {
  return `${(rate / 1000).toLocaleString("zh-CN", { maximumFractionDigits: 1 })} kHz`;
}

/** 一台设备的说明：型号、在线、格式与上限、音量是否可调、手动添加。Google Cast 的格式是管理员设定的编码。 */
function deviceDescription(device: LanDeviceDTO): string {
  const parts = [device.model === "" ? null : device.model, device.online ? "在线" : "离线"];
  const airplay = device.capabilities.airplay;
  const slim = device.capabilities.slimproto ?? null;
  if (device.kind === "slimproto") {
    if (slim !== null && slim.firmware !== "") parts.push(`${slim.model_name === "" ? "播放器" : slim.model_name} ${slim.firmware}`);
    parts.push(slim?.unsupported === true ? "播放器不支持 FLAC，暂时不能使用" : `FLAC，最高 ${rateText(device.capabilities.max_sample_rate)} / ${device.capabilities.max_bit_depth} 位`);
  } else if (device.format !== null && device.format.codec === "alac") {
    parts.push(`ALAC，最高 ${rateText(device.format.max_sample_rate)} / ${device.format.max_bit_depth} 位`);
    if (airplay?.timing_protocol != null) parts.push(airplay.timing_protocol === "PTP" ? "精确对时" : "兼容模式");
    if (airplay?.timing_reason != null) parts.push(timingReasons[airplay.timing_reason] ?? "精确对时暂不可用");
  } else if (device.format !== null && !("volume" in device.format)) {
    parts.push(`${codecLabels[device.format.codec]}，最高 ${rateText(device.format.max_sample_rate)}，曲目之间可能有短暂停顿`);
  } else {
    const formats = device.capabilities.format_declared ? device.capabilities.stream_formats.map((format) => format.toUpperCase()).join("、") : "没有声明支持的格式，先试 FLAC";
    parts.push(`${formats}，最高 ${rateText(device.capabilities.max_sample_rate)} / ${device.capabilities.max_bit_depth} 位`);
  }
  if (device.capabilities.volume === "fixed") parts.push(device.kind === "slimproto" ? "固定音量，在功放或解码器上调" : "音量在设备上调");
  parts.push(device.address);
  return parts.filter((part) => part !== null).join(" · ");
}

/**
 * 设置 › 局域网设备（管理员）：总开关与两种协议（UPnP/DLNA、Google Cast）的开关、账号间共享，找到的设备列表，手动添加，共享关闭时按设备分配成员，Google Cast 设备的编码设置。
 * 选设备播放在播放器的设备列表里，这里只管哪些设备、给谁用。
 */
export function LanDevicesSettings() {
  const settings = useLanDeviceSettings();
  const update = useUpdateLanDeviceSettings();
  const pending = update.isPending ? update.variables : undefined;
  const value = (key: keyof LanDeviceSettingsChange, current: boolean) => pending?.[key] ?? current;
  return (
    <>
      <SettingsGroup title="局域网设备" footer={update.isError ? <FailureNotice error={update.error} context="write" title="开关没有改成" /> : undefined}>
        <QueryContent query={settings} loading={<Skeleton label="正在加载设置" lines={2} />}>
          {(data) => (
            <>
              <SettingRow label="使用局域网里的播放设备" description="打开后在局域网里查找功放、音箱、电视与播放软件，找到的设备出现在播放器的设备列表里。">
                {({ controlId, descriptionId }) => (
                  <Switch id={controlId} aria-describedby={descriptionId} checked={value("enabled", data.enabled)} disabled={update.isPending} onChange={(enabled) => update.mutate({ enabled })} />
                )}
              </SettingRow>
              {value("enabled", data.enabled) ? (
                <>
                  <SettingRow nested label="UPnP/DLNA" description="大多数功放、网络播放器、电视与播放软件。">
                    {({ controlId, descriptionId }) => (
                      <Switch id={controlId} aria-describedby={descriptionId} checked={value("upnp_enabled", data.upnp_enabled)} disabled={update.isPending} onChange={(on) => update.mutate({ upnp_enabled: on })} />
                    )}
                  </SettingRow>
                  <SettingRow nested label="Google Cast" description="Chromecast、Google TV、Nest 音箱，以及内置 Google Cast 的音箱与功放。">
                    {({ controlId, descriptionId }) => (
                      <Switch id={controlId} aria-describedby={descriptionId} checked={value("cast_enabled", data.cast_enabled)} disabled={update.isPending} onChange={(on) => update.mutate({ cast_enabled: on })} />
                    )}
                  </SettingRow>
                  <SettingRow nested label="AirPlay" description="HomePod、Apple TV、Mac，以及支持 AirPlay 的音箱与功放。">
                    {({ controlId, descriptionId }) => (
                      <Switch id={controlId} aria-describedby={descriptionId} checked={value("airplay_enabled", data.airplay_enabled)} disabled={update.isPending} onChange={(on) => update.mutate({ airplay_enabled: on })} />
                    )}
                  </SettingRow>
                  <SettingRow
                    nested
                    label="SlimProto"
                    description={
                      data.slimproto_unavailable === "port_in_use" && data.slimproto_enabled
                        ? "端口 3483 被本机其他程序占用（例如 Lyrion Music Server），暂时用不了；端口空出来后自动恢复。"
                        : "piCorePlayer、moOde 等装了 Squeezelite 的播放器，无损高码率。播放器会自己连上来，服务器地址填这台机器。"
                    }
                  >
                    {({ controlId, descriptionId }) => (
                      <span className={styles.rowEnd}>
                        {data.slimproto_unavailable === "port_in_use" && data.slimproto_enabled ? <TriangleAlert size={16} className={styles.warning} aria-hidden="true" /> : null}
                        <Switch id={controlId} aria-describedby={descriptionId} checked={value("slimproto_enabled", data.slimproto_enabled)} disabled={update.isPending} onChange={(on) => update.mutate({ slimproto_enabled: on })} />
                      </span>
                    )}
                  </SettingRow>
                </>
              ) : null}
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
  const [formatting, setFormatting] = useState<LanDeviceDTO | null>(null);
  const [pairing, setPairing] = useState<LanDeviceDTO | null>(null);
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
                ...(device.format === null ? [] : [{ id: "format", label: device.kind === "cast" ? `${device.name} 的编码格式…` : `${device.name} 的音质…`, onSelect: () => setFormatting(device) }]),
                ...(device.capabilities.airplay?.requires_pin === true && !device.capabilities.airplay.paired ? [{ id: "pair", label: `与 ${device.name} 配对…`, onSelect: () => setPairing(device) }] : []),
                ...(settings.shared ? [] : [{ id: "grants", label: `分配 ${device.name} 给成员…`, onSelect: () => setGranting(device) }]),
                ...(device.manual ? [{ id: "remove", label: `移除 ${device.name}`, danger: true, onSelect: () => remove.mutate(device.udn) }] : []),
              ];
              const unavailable = device.capabilities.airplay?.unavailable_reason ?? null;
              const assigned = settings.shared ? null : device.user_ids.length === 0 ? "只有管理员可用" : `分配给 ${device.user_ids.length} 位成员`;
              return (
                <SettingRow key={device.udn} label={device.name === "" ? "未命名设备" : device.name} description={assigned === null ? deviceDescription(device) : `${deviceDescription(device)} · ${assigned}`}>
                  {() => (
                    <span className={styles.rowEnd}>
                      <StatusTag>{kindLabels[device.kind]}</StatusTag>
                      {unavailable === null ? null : <StatusTag tone="outline">{unavailable}</StatusTag>}
                      {device.capabilities.airplay?.requires_pin === true && !device.capabilities.airplay.paired ? <StatusTag>待配对</StatusTag> : null}
                      {device.capabilities.slimproto?.unsupported === true ? <StatusTag tone="outline">不支持</StatusTag> : null}
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
      <FormatDialog device={formatting} onClose={() => setFormatting(null)} />
      <PairDialog device={pairing} onClose={() => setPairing(null)} />
    </SettingsGroup>
  );
}

function AddDeviceDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const add = useAddLanDevice();
  const [kind, setKind] = useState<ManualKind>("upnp");
  const [address, setAddress] = useState("");
  const trimmed = address.trim();
  function close() {
    setAddress("");
    setKind("upnp");
    add.reset();
    onClose();
  }
  function submit(event?: FormEvent) {
    event?.preventDefault();
    if (trimmed === "" || add.isPending) return;
    add.mutate({ kind, address: trimmed }, { onSuccess: close });
  }
  return (
    <ConfirmDialog
      open={open}
      title="手动添加设备"
      confirmLabel="添加"
      pending={add.isPending}
      error={add.isError ? <FailureNotice error={add.error} context="write" messages={addFailures[kind]} /> : undefined}
      onCancel={close}
      onConfirm={() => submit()}
    >
      <form onSubmit={submit} className={styles.form}>
        <fieldset className={styles.choices}>
          <legend className={styles.legend}>设备类型</legend>
          {(["upnp", "cast", "airplay"] as const).map((option) => (
            <label key={option} className={styles.choice}>
              <input type="radio" name="lan-device-kind" checked={kind === option} onChange={() => setKind(option)} />
              {kindLabels[option]}
            </label>
          ))}
        </fieldset>
        <TextField label="设备地址" value={address} placeholder="192.168.1.25" autoFocus onChange={(event) => setAddress(event.target.value)} />
        <p className={styles.hint}>
          {kind === "upnp"
            ? "填设备的局域网 IP；如果设备不响应查找，填它的描述地址（http://IP:端口/…xml）。"
            : kind === "cast"
              ? "填设备的局域网 IP；端口不是 8009 时写成 IP:端口。"
              : "填设备的局域网 IP；端口不是 7000 时写成 IP:端口。"}
        </p>
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

/** 编码设置：Google Cast 选编码与采样率，AirPlay 与 SlimProto 选音质。格式的形状按设备种类区分（Cast 与 SlimProto 都可能是 FLAC）。 */
function FormatDialog({ device, onClose }: { device: LanDeviceDTO | null; onClose: () => void }) {
  if (device === null || device.format === null) return null;
  const format = device.format;
  if (device.kind === "airplay" && format.codec === "alac") return <AirPlayFormatForm key={device.udn} device={device} format={format} onClose={onClose} />;
  if (device.kind === "slimproto" && "volume" in format) return <SlimProtoFormatForm key={device.udn} device={device} format={format} onClose={onClose} />;
  if (device.kind === "cast" && format.codec !== "alac" && !("volume" in format)) return <FormatForm key={device.udn} device={device} format={format} onClose={onClose} />;
  return null;
}

/** SlimProto 可选的采样率上限；实际只列出不超过播放器声明值的那些，服务器封顶 768 kHz。 */
const slimProtoRates = [44100, 48000, 88200, 96000, 176400, 192000, 352800, 384000, 705600, 768000] as const;
const slimProtoCeiling = 768000;

/** 一台 SlimProto 播放器的音质：采样率上限（默认跟随播放器声明）与音量方式。接功放或用解码器调音量时选固定音量，播放器按原电平输出。 */
function SlimProtoFormatForm({ device, format, onClose }: { device: LanDeviceDTO; format: SlimProtoFormatDTO; onClose: () => void }) {
  const put = usePutSlimProtoFormat();
  const declared = Math.min(device.capabilities.slimproto?.declared_max_sample_rate ?? device.capabilities.max_sample_rate, slimProtoCeiling);
  const rates = slimProtoRates.filter((rate) => rate <= declared);
  const [rate, setRate] = useState<number | null>(format.max_sample_rate);
  const [volume, setVolume] = useState(format.volume);
  return (
    <ConfirmDialog
      open
      title={`“${device.name}”的音质`}
      confirmLabel="保存"
      pending={put.isPending}
      error={put.isError ? <FailureNotice error={put.error} context="write" messages={{ invalid_input: "这台播放器不支持这个采样率。" }} /> : undefined}
      onCancel={onClose}
      onConfirm={() => put.mutate({ udn: device.udn, format: { codec: "flac", max_sample_rate: rate, volume } }, { onSuccess: onClose })}
    >
      <div className={styles.form}>
        <fieldset className={styles.choices}>
          <legend className={styles.legend}>采样率上限（FLAC 无损）</legend>
          <label className={styles.choice}>
            <input type="radio" name="slimproto-rate" checked={rate === null} onChange={() => setRate(null)} />
            跟随播放器（{rateText(declared)}）
          </label>
          {rates.map((option) => (
            <label key={option} className={styles.choice}>
              <input type="radio" name="slimproto-rate" checked={rate === option} onChange={() => setRate(option)} />
              {rateText(option)}
            </label>
          ))}
        </fieldset>
        <fieldset className={styles.choices}>
          <legend className={styles.legend}>音量</legend>
          <label className={styles.choice}>
            <input type="radio" name="slimproto-volume" checked={volume === "number"} onChange={() => setVolume("number")} />
            在 ROOMusic 里调
          </label>
          <label className={styles.choice}>
            <input type="radio" name="slimproto-volume" checked={volume === "fixed"} onChange={() => setVolume("fixed")} />
            固定，在功放或解码器上调
          </label>
        </fieldset>
        <p className={styles.hint}>高于上限的音乐会转换后再送给播放器，DSD 转成 PCM。采样率下一首起生效，音量方式立即生效。</p>
      </div>
    </ConfirmDialog>
  );
}

const airPlayQualities = [
  { id: "standard", label: "44.1 kHz / 16 位", rate: 44100, depth: 16 },
  { id: "high", label: "48 kHz / 24 位（仅 AirPlay 2 设备）", rate: 48000, depth: 24 },
] as const;

/** 一台 AirPlay 设备的音质。默认 44.1 kHz/16 位：AirPlay 1 只支持它，HomePod 在 Wi-Fi 下用 24 位可能有杂音。 */
function AirPlayFormatForm({ device, format, onClose }: { device: LanDeviceDTO; format: AirPlayFormatDTO; onClose: () => void }) {
  const put = usePutAirPlayFormat();
  const [quality, setQuality] = useState(format.max_bit_depth === 24 ? "high" : "standard");
  const chosen = airPlayQualities.find((option) => option.id === quality) ?? airPlayQualities[0];
  return (
    <ConfirmDialog
      open
      title={`“${device.name}”的音质`}
      confirmLabel="保存"
      pending={put.isPending}
      error={put.isError ? <FailureNotice error={put.error} context="write" messages={{ invalid_input: "这台设备只支持 44.1 kHz / 16 位。" }} /> : undefined}
      onCancel={onClose}
      onConfirm={() => put.mutate({ udn: device.udn, format: { codec: "alac", max_sample_rate: chosen.rate, max_bit_depth: chosen.depth } }, { onSuccess: onClose })}
    >
      <div className={styles.form}>
        <fieldset className={styles.choices}>
          <legend className={styles.legend}>音质（ALAC 无损）</legend>
          {airPlayQualities.map((option) => (
            <label key={option.id} className={styles.choice}>
              <input type="radio" name="airplay-quality" checked={quality === option.id} onChange={() => setQuality(option.id)} />
              {option.label}
            </label>
          ))}
        </fieldset>
        <p className={styles.hint}>高于上限的音乐会转换后再送给设备；HomePod 通过 Wi-Fi 播放 24 位时可能有杂音。下一首起生效。</p>
      </div>
    </ConfirmDialog>
  );
}

const pairFailures = { pairing_expired: "这次配对已过期，关掉对话框重新开始。" } as const;

/** Apple TV 等需要屏幕 PIN 的设备：打开对话框即请设备显示 PIN，输入后完成配对；凭据保存在服务器上，之后不再需要。 */
function PairDialog({ device, onClose }: { device: LanDeviceDTO | null; onClose: () => void }) {
  return device === null ? null : <PairForm key={device.udn} device={device} onClose={onClose} />;
}

function PairForm({ device, onClose }: { device: LanDeviceDTO; onClose: () => void }) {
  const start = useStartAirPlayPairing();
  const finish = useFinishAirPlayPairing();
  const [pin, setPin] = useState("");
  const { mutate: startPairing } = start;
  useEffect(() => {
    startPairing(device.udn);
  }, [startPairing, device.udn]);
  const trimmed = pin.trim();
  const error = start.error ?? finish.error;
  function submit(event?: FormEvent) {
    event?.preventDefault();
    if (trimmed === "" || finish.isPending || !start.isSuccess) return;
    finish.mutate({ udn: device.udn, pin: trimmed }, { onSuccess: onClose });
  }
  return (
    <ConfirmDialog
      open
      title={`与“${device.name}”配对`}
      confirmLabel="配对"
      pending={finish.isPending || start.isPending}
      error={error === null ? undefined : <FailureNotice error={error} context="write" messages={pairFailures} />}
      onCancel={onClose}
      onConfirm={() => submit()}
    >
      <form onSubmit={submit} className={styles.form}>
        <TextField label="屏幕上的 PIN" value={pin} inputMode="numeric" autoComplete="one-time-code" autoFocus onChange={(event) => setPin(event.target.value)} />
        <p className={styles.hint}>{start.isPending ? "正在请设备显示 PIN…" : "输入设备屏幕上显示的 4 位数字。配对只需要做一次。"}</p>
      </form>
    </ConfirmDialog>
  );
}

function FormatForm({ device, format, onClose }: { device: LanDeviceDTO; format: CastFormatDTO; onClose: () => void }) {
  const put = usePutCastFormat();
  const [codec, setCodec] = useState(format.codec);
  const [rate, setRate] = useState(format.max_sample_rate);
  const rates = codec === "flac" ? [44100, 48000, 96000] : [44100, 48000];
  const effectiveRate = rates.includes(rate) ? rate : 48000;
  return (
    <ConfirmDialog
      open
      title={`“${device.name}”的编码格式`}
      confirmLabel="保存"
      pending={put.isPending}
      error={put.isError ? <FailureNotice error={put.error} context="write" /> : undefined}
      onCancel={onClose}
      onConfirm={() => put.mutate({ udn: device.udn, format: { codec, max_sample_rate: effectiveRate } }, { onSuccess: onClose })}
    >
      <div className={styles.form}>
        <fieldset className={styles.choices}>
          <legend className={styles.legend}>编码</legend>
          {(["mp3", "aac", "flac"] as const).map((option) => (
            <label key={option} className={styles.choice}>
              <input type="radio" name="cast-codec" checked={codec === option} onChange={() => setCodec(option)} />
              {codecLabels[option]}
            </label>
          ))}
        </fieldset>
        <fieldset className={styles.choices}>
          <legend className={styles.legend}>采样率上限</legend>
          {rates.map((option) => (
            <label key={option} className={styles.choice}>
              <input type="radio" name="cast-rate" checked={effectiveRate === option} onChange={() => setRate(option)} />
              {rateText(option)}
            </label>
          ))}
        </fieldset>
        <p className={styles.hint}>MP3 兼容性最好（320 kb/s）。FLAC 无损，但部分 Nest 与三星音箱播放较长的曲目会中断。下一首起生效。</p>
      </div>
    </ConfirmDialog>
  );
}
