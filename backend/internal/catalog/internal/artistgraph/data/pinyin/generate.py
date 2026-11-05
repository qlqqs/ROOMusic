#!/usr/bin/env python3
"""从已下载的固定 Unihan 17.0.0 生成首字母表；不联网、不改写上游输入。"""

import argparse
import hashlib
import io
import json
from pathlib import Path
import unicodedata
import zipfile

GENERATOR_VERSION = 1
UNICODE_VERSION = "17.0.0"
ZIP_SHA256 = "f7a48b2b545acfaa77b2d607ae28747404ce02baefee16396c5d2d7a8ef34b5e"
READINGS_SHA256 = "575e69c9ad85a4737a889a4f94cbd987042a90a1a6cc16dd3f4ed995c715b17c"
LICENSE_SHA256 = "e7a93b009565cfce55919a381437ac4db883e9da2126fa28b91d12732bc53d96"
EXPECTED_COUNT = 44348


def checked(data: bytes, expected: str, name: str) -> bytes:
    if hashlib.sha256(data).hexdigest() != expected:
        raise ValueError(f"{name} SHA-256 不匹配")
    return data


def extract(readings: bytes) -> tuple[bytes, list[dict], list[dict]]:
    entries = {}
    multiple = []
    unknown = []
    for line_number, line in enumerate(readings.decode("utf-8").splitlines(), 1):
        if not line or line.startswith("#"):
            continue
        fields = line.split("\t")
        if len(fields) != 3:
            raise ValueError(f"第 {line_number} 行不是三个 TAB 字段")
        codepoint, field, value = fields
        if field != "kMandarin":
            continue
        scalar = int(codepoint.removeprefix("U+"), 16)
        if (not 0 <= scalar <= 0x10FFFF or 0xD800 <= scalar <= 0xDFFF
                or codepoint != f"U+{scalar:04X}"):
            raise ValueError(f"第 {line_number} 行不是规范 Unicode scalar")
        if scalar in entries:
            raise ValueError(f"第 {line_number} 行有重复 kMandarin 键")
        pronunciations = value.split()
        if not pronunciations:
            raise ValueError(f"第 {line_number} 行没有读音")
        # 仅取上游排列的首读音，不选择姓氏读法，不扩展为全拼音搜索。
        first = "".join(c for c in unicodedata.normalize("NFD", pronunciations[0])
                        if not unicodedata.category(c).startswith("M")).upper()
        initial = first[:1]
        if len(pronunciations) > 1:
            multiple.append({"codepoint": codepoint, "readings": pronunciations,
                             "selected": pronunciations[0], "initial": initial})
        if not initial or not "A" <= initial <= "Z":
            unknown.append({"codepoint": codepoint, "reading": pronunciations[0]})
        entries[scalar] = initial
    if unknown:
        raise ValueError("首读音不能转换为 A–Z：" + json.dumps(unknown, ensure_ascii=False))
    table = "".join(f"U+{cp:04X}\t{entries[cp]}\n" for cp in sorted(entries)).encode("ascii")
    return table, sorted(multiple, key=lambda row: int(row["codepoint"][2:], 16)), unknown


def generate(archive: bytes, license_data: bytes) -> dict[str, bytes]:
    checked(archive, ZIP_SHA256, "Unihan.zip")
    checked(license_data, LICENSE_SHA256, "LICENSE")
    with zipfile.ZipFile(io.BytesIO(archive)) as source:
        readings = checked(source.read("Unihan_Readings.txt"), READINGS_SHA256,
                           "Unihan_Readings.txt")
    table, multiple, unknown = extract(readings)
    count = table.count(b"\n")
    if count != EXPECTED_COUNT:
        raise ValueError(f"kMandarin 条目数应为 {EXPECTED_COUNT}，实际 {count}")
    manifest = {
        "generator_version": GENERATOR_VERSION,
        "unicode_version": UNICODE_VERSION,
        "unihan_zip_sha256": ZIP_SHA256,
        "readings_sha256": READINGS_SHA256,
        "license_sha256": LICENSE_SHA256,
        "source_header": readings.decode("utf-8").split("\nU+", 1)[0],
        "initials_sha256": hashlib.sha256(table).hexdigest(),
        "entry_count": count,
        "multiple_readings_count": len(multiple),
        "multiple_readings": multiple,
        "unknown_count": len(unknown),
        "unknown": unknown,
    }
    return {"initials.tsv": table, "LICENSE": license_data,
            "manifest.json": (json.dumps(manifest, ensure_ascii=False, indent=2) + "\n").encode("utf-8")}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--unihan", required=True, type=Path, help="固定版本的 Unihan.zip")
    parser.add_argument("--license", required=True, type=Path, help="原始 Unicode License V3")
    parser.add_argument("--output", required=True, type=Path, help="生成目录，不得覆盖输入")
    args = parser.parse_args()
    outputs = generate(args.unihan.read_bytes(), args.license.read_bytes())
    inputs = {args.unihan.resolve(), args.license.resolve()}
    if any((args.output / name).resolve() in inputs for name in outputs):
        parser.error("输出不能覆盖上游输入")
    args.output.mkdir(parents=True, exist_ok=True)
    for name, content in outputs.items():
        (args.output / name).write_bytes(content)
    print(f"已生成 {EXPECTED_COUNT} 条首字母；SHA-256 "
          f"{hashlib.sha256(outputs['initials.tsv']).hexdigest()}")


if __name__ == "__main__":
    main()
