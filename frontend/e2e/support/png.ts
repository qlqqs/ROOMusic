import { deflateSync } from "node:zlib";

// 测试用的封面图片：按种子生成确定的渐变 PNG，只用 Node 自带的 zlib，不引入图像依赖，也不读取任何真实封面。

const crcTable = Array.from({ length: 256 }, (_, n) => {
  let value = n;
  for (let bit = 0; bit < 8; bit += 1) value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
  return value >>> 0;
});

function crc32(data: Buffer): number {
  let value = 0xffffffff;
  for (const byte of data) value = (crcTable[(value ^ byte) & 0xff] ?? 0) ^ (value >>> 8);
  return (value ^ 0xffffffff) >>> 0;
}

function chunk(type: string, data: Buffer): Buffer {
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
  const checksum = Buffer.alloc(4);
  checksum.writeUInt32BE(crc32(body));
  return Buffer.concat([length, body, checksum]);
}

/** size × size 的 RGB 渐变图：颜色由 seed 决定，同一个种子得到相同的字节。 */
export function gradientPng(size: number, seed: number): Buffer {
  const header = Buffer.alloc(13);
  header.writeUInt32BE(size, 0);
  header.writeUInt32BE(size, 4);
  header[8] = 8; // 位深
  header[9] = 2; // RGB
  const rows: Buffer[] = [];
  for (let y = 0; y < size; y += 1) {
    const row = Buffer.alloc(1 + size * 3); // 第一个字节是滤波类型 0
    for (let x = 0; x < size; x += 1) {
      row[1 + x * 3] = (seed * 53 + Math.floor((x * 160) / size)) & 255;
      row[2 + x * 3] = (seed * 97 + Math.floor((y * 160) / size)) & 255;
      row[3 + x * 3] = (seed * 29 + 90) & 255;
    }
    rows.push(row);
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(Buffer.concat(rows))),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}
