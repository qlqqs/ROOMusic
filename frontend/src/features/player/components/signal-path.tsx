import { usePlayer } from "../player-provider";
import { statusLabels } from "./player-labels";
import styles from "./signal-path.module.css";

const containers: Record<string, string> = { flac_vorbis_comment: "FLAC", m4a: "MP4 / M4A", mp3_id3v2: "MP3", wav_info: "WAV", ogg_vorbis_comment: "Ogg", opus_tags: "Ogg", cue_virtual: "CUE 虚拟轨" };
export function SignalPath() {
  const { snapshot } = usePlayer();
  const source = snapshot.current?.track.source;
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
      <li><h3>服务端</h3><p>原文件直出</p><p>字节不改动，不转码。播放前仍需核对来源与权限。</p></li>
      <li><h3>浏览器</h3><p>{statusLabels[snapshot.status]}</p><p>由浏览器解码，可能按输出设备重新采样。</p></li>
      <li><h3>系统输出</h3><p>未知</p><p>网页无法得知声卡的实际格式与音量链路。</p></li>
    </ol>
  </section>;
}
