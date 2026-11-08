import styles from "./avatar.module.css";

const segmenter = typeof Intl.Segmenter === "function" ? new Intl.Segmenter(undefined, { granularity: "grapheme" }) : null;

/** 头像里的字：用户名的第一个字（按字素取，带组合符号的字不会被拆开），拉丁字母转大写。 */
function initialOf(username: string): string {
  const first = segmenter?.segment(username)[Symbol.iterator]().next().value?.segment ?? Array.from(username)[0] ?? "";
  return first.toLocaleUpperCase();
}

/** 圆形头像：用户名的首字。装饰性的，用户名总在旁边写出来。 */
export function Avatar({ name }: { name: string }) {
  return (
    <span className={styles.avatar} aria-hidden="true">
      {initialOf(name)}
    </span>
  );
}
