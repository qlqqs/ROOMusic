/** DSD 采样率按倍率显示：2822400 写 DSD64，48 kHz 族的 3072000 写 DSD64（48k）；不是 DSD 采样率时返回 null。 */
export function dsdRateName(hertz: number): string | null {
  for (const [base, suffix] of [[44100, ""], [48000, "（48k）"]] as const) {
    const multiple = hertz / base;
    if (Number.isInteger(multiple) && multiple >= 64 && (multiple & (multiple - 1)) === 0) return `DSD${multiple}${suffix}`;
  }
  return null;
}
