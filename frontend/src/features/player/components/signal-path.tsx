import { usePlaybackCapabilities } from "../hooks/use-playback-capabilities";
import { usePlayer } from "../player-provider";
import { statusLabels } from "./player-labels";
import styles from "./signal-path.module.css";

const containers: Record<string, string> = { flac_vorbis_comment: "FLAC", m4a: "MP4 / M4A", mp3_id3v2: "MP3", wav_info: "WAV", ogg_vorbis_comment: "Ogg", opus_tags: "Ogg", cue_virtual: "CUE 虚拟轨" };
/** 服务端环节按当前表示显示：直出原文件，或无损转换（ALAC 解码后重新编码为 FLAC，位深、采样率与声道不变，发布前核对解码后的 PCM 一致）。 */
function ServerStage({ delivery }: { delivery: "original" | "derived" | "pending" }) {
  if (delivery === "pending") return <><p>读取中</p><p>正在取得服务端的交付方式。</p></>;
  if (delivery === "derived") return <><p>无损转换</p><p>ALAC 转成 FLAC，位深、采样率与声道不变。</p></>;
  return <><p>原文件直出</p><p>原样发送，不转码。</p></>;
}
export function SignalPath() {
  const { snapshot } = usePlayer();
  const capabilities = usePlaybackCapabilities();
  const source = snapshot.current?.track.source;
  const table = capabilities.data;
  // 格式表还没取到时不替 ALAC 曲目说“原文件直出”。
  const delivery = source !== undefined && table === undefined && capabilities.isPending ? "pending" : source === undefined || table === undefined ? "original" : (table.deliveryOf({ sourceKind: source.sourceKind, codec: source.codec }) ?? "original");
  return <section id="signal-path" aria-labelledby="signal-heading" className={styles.section}>
    <h2 id="signal-heading">信号路径</h2>
    <ol role="list" className={styles.path}>
      <li><h3>源文件</h3><dl>
        <div><dt>容器</dt><dd>{source === undefined ? "未知" : (containers[source.sourceKind] ?? "未知")}</dd></div>
        <div><dt>编码</dt><dd>{source?.codec?.toUpperCase() ?? "未知"}</dd></div>
        <div><dt>位深</dt><dd>{source?.bitDepth == null ? "未知" : `${source.bitDepth} bit`}</dd></div>
        <div><dt>采样率</dt><dd>{source?.sampleRate == null ? "未知" : `${source.sampleRate / 1000} kHz`}</dd></div>
        <div><dt>声道</dt><dd>{source?.channels ?? "未知"}</dd></div>
      </dl></li>
      <li><h3>服务端</h3><ServerStage delivery={delivery} /></li>
      <li><h3>浏览器</h3><p>{statusLabels[snapshot.status]}</p><p>由浏览器解码，可能按输出设备重新采样。</p></li>
      <li><h3>系统输出</h3><p>未知</p><p>网页看不到声卡的实际格式。</p></li>
    </ol>
  </section>;
}
