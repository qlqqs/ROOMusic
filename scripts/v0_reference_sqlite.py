#!/usr/bin/env python3
"""把 standalone V0 adapter 的临时行原子写入 normalized SQLite。

该工具只处理 Smoke 临时目录中的数据。SQLite 是本地审计载体；比较器使用的
canonical JSON 必须完全由 SQLite 回读产生，不能直接信任 adapter 的序列化结果。
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import pathlib
import posixpath
import re
import sqlite3
import stat
import sys
import tempfile
import urllib.parse
from collections.abc import Iterable
from typing import Any


ROWS_FORMAT_VERSION = 1
SQLITE_SCHEMA_VERSION = 2
SNAPSHOT_VERSION = 2
IMPLEMENTATION = "v0_release_graph_generated_corrected"
GENERATION_MODE = "standalone_scanner"
BASELINE_SCOPE = "release_graph_only"
EXCLUDED_EVIDENCE = [
    "local_evidence",
    "quality_badges",
    "scan_diagnostics",
    "production_runtime_status",
]
HASH_RE = re.compile(r"^[0-9a-f]{64}$")
REFERENCE_RE = re.compile(r"^[0-9a-f]{64}$")
MAX_ROWS_BYTES = 1 << 30
MAX_LINE_BYTES = 32 << 20


SCHEMA_SQL = """
PRAGMA foreign_keys = ON;
PRAGMA trusted_schema = OFF;

CREATE TABLE baseline_manifest (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

CREATE TABLE releases (
    key TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    artist TEXT NOT NULL,
    album_artist TEXT NOT NULL,
    year INTEGER NOT NULL CHECK (year >= 0),
    source_type TEXT NOT NULL,
    media_type TEXT NOT NULL,
    edition TEXT NOT NULL,
    label TEXT NOT NULL,
    catalog TEXT NOT NULL,
    genre TEXT NOT NULL,
    country TEXT NOT NULL,
    barcode TEXT NOT NULL,
    provider TEXT NOT NULL,
    release_type TEXT NOT NULL,
    candidate_kind TEXT NOT NULL
) STRICT;

CREATE TABLE media (
    key TEXT PRIMARY KEY,
    release_key TEXT NOT NULL REFERENCES releases(key) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position > 0),
    title TEXT NOT NULL,
    format TEXT NOT NULL,
    UNIQUE (release_key, position)
) STRICT;

CREATE TABLE files (
    key TEXT PRIMARY KEY,
    release_key TEXT NOT NULL REFERENCES releases(key) ON DELETE CASCADE,
    source_key TEXT NOT NULL UNIQUE,
    media TEXT NOT NULL,
    size INTEGER NOT NULL CHECK (size >= 0)
) STRICT;

CREATE TABLE tracks (
    key TEXT PRIMARY KEY,
    medium_key TEXT NOT NULL REFERENCES media(key) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position > 0),
    source_key TEXT NOT NULL UNIQUE,
    parent_file_key TEXT REFERENCES files(key),
    title TEXT NOT NULL,
    artist TEXT NOT NULL,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('physical', 'cue_virtual')),
    duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
    codec TEXT NOT NULL,
    sample_rate INTEGER NOT NULL CHECK (sample_rate >= 0),
    channels INTEGER NOT NULL CHECK (channels >= 0),
    bitrate INTEGER NOT NULL CHECK (bitrate >= 0),
    bit_depth INTEGER NOT NULL CHECK (bit_depth >= 0),
    cue_index_frames INTEGER CHECK (cue_index_frames >= 0),
    cue_end_frames INTEGER CHECK (cue_end_frames >= 0),
    cue_isrc TEXT NOT NULL,
    UNIQUE (medium_key, position)
) STRICT;

CREATE TABLE release_credits (
    release_key TEXT NOT NULL REFERENCES releases(key) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position > 0),
    role TEXT NOT NULL,
    name TEXT NOT NULL,
    PRIMARY KEY (release_key, role, name)
) STRICT;

CREATE TABLE track_credits (
    track_key TEXT NOT NULL REFERENCES tracks(key) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position > 0),
    role TEXT NOT NULL,
    name TEXT NOT NULL,
    PRIMARY KEY (track_key, role, name)
) STRICT;

CREATE TABLE release_field_evidence (
    release_key TEXT NOT NULL REFERENCES releases(key) ON DELETE CASCADE,
    field TEXT NOT NULL,
    value TEXT NOT NULL,
    source TEXT NOT NULL,
    confidence TEXT NOT NULL,
    action TEXT NOT NULL,
    rule_id TEXT NOT NULL,
    PRIMARY KEY (release_key, field)
) STRICT;

CREATE TABLE release_grouping_evidence (
    release_key TEXT PRIMARY KEY REFERENCES releases(key) ON DELETE CASCADE,
    candidate_kind TEXT NOT NULL,
    parent_collection_key TEXT NOT NULL,
    track_count INTEGER NOT NULL CHECK (track_count >= 0),
    medium_count INTEGER NOT NULL CHECK (medium_count >= 0)
) STRICT;
""".strip()


class ContractError(ValueError):
    """表示 adapter、SQLite 或 canonical 合同不满足。"""


def digest(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8", "surrogatepass")).hexdigest()


def canonical_token(value: str) -> str:
    return value.strip().lower()


def canonical_release_field(value: str) -> str:
    return {
        "album_title": "title",
        "edition_version": "edition",
    }.get(value, value)


def require_hash(value: str, label: str) -> str:
    if not isinstance(value, str) or HASH_RE.fullmatch(value) is None:
        raise ContractError(f"invalid {label}")
    return value


def require_text(value: Any, label: str, *, allow_empty: bool = True) -> str:
    if not isinstance(value, str) or "\x00" in value:
        raise ContractError(f"invalid {label}")
    try:
        value.encode("utf-8")
    except UnicodeEncodeError as error:
        raise ContractError(f"invalid {label}") from error
    if not allow_empty and not value.strip():
        raise ContractError(f"empty {label}")
    return value


def require_integer(value: Any, label: str, *, minimum: int = 0) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < minimum:
        raise ContractError(f"invalid {label}")
    return value


def require_list(value: Any, label: str) -> list[Any]:
    if not isinstance(value, list):
        raise ContractError(f"invalid {label}")
    return value


def relative_path(value: Any, label: str, *, allow_empty: bool = False) -> str:
    text = require_text(value, label, allow_empty=allow_empty)
    if text == "" and allow_empty:
        return ""
    if "\\" in text or text.startswith("/"):
        raise ContractError(f"absolute or non-canonical {label}")
    components = text.split("/")
    if any(component in ("", ".", "..") for component in components):
        raise ContractError(f"non-canonical {label}")
    normalized = posixpath.normpath(text)
    if normalized != text or normalized.startswith("../") or normalized == "..":
        raise ContractError(f"escaping {label}")
    return text


def json_object_no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ContractError("duplicate JSON key")
        result[key] = value
    return result


def load_rows(path: str) -> tuple[dict[str, Any], list[dict[str, Any]], dict[str, Any]]:
    info = os.lstat(path)
    if not stat.S_ISREG(info.st_mode) or info.st_size <= 0 or info.st_size > MAX_ROWS_BYTES:
        raise ContractError("invalid rows file")

    records: list[tuple[dict[str, Any], bytes]] = []
    with open(path, "rb") as handle:
        for number, raw_line in enumerate(handle, start=1):
            if len(raw_line) > MAX_LINE_BYTES or not raw_line.endswith(b"\n"):
                raise ContractError("invalid NDJSON line")
            encoded = raw_line[:-1]
            if not encoded:
                raise ContractError("empty NDJSON line")
            try:
                record = json.loads(
                    encoded,
                    object_pairs_hook=json_object_no_duplicates,
                )
            except (UnicodeDecodeError, json.JSONDecodeError) as error:
                raise ContractError(f"invalid NDJSON record {number}") from error
            if not isinstance(record, dict):
                raise ContractError("NDJSON record is not an object")
            records.append((record, raw_line))

    if len(records) < 3:
        raise ContractError("incomplete rows stream")
    header, _ = records[0]
    footer, _ = records[-1]
    releases = [record for record, _ in records[1:-1]]
    if header.get("record_type") != "header" or footer.get("record_type") != "complete":
        raise ContractError("invalid rows sequence")
    if any(release.get("record_type") != "release" for release in releases):
        raise ContractError("unexpected rows record")
    references = [release.get("reference") for release in releases]
    if any(not isinstance(reference, str) for reference in references) or references != sorted(references):
        raise ContractError("release rows are not deterministically ordered")

    records_hash = hashlib.sha256()
    for _, raw_line in records[1:-1]:
        records_hash.update(raw_line)
    if footer.get("records_sha256") != records_hash.hexdigest():
        raise ContractError("rows digest mismatch")
    return header, releases, footer


def validate_header(header: dict[str, Any]) -> None:
    if require_integer(header.get("format_version"), "rows format version") != ROWS_FORMAT_VERSION:
        raise ContractError("unsupported rows format")
    if header.get("implementation") != IMPLEMENTATION:
        raise ContractError("unexpected implementation")
    if header.get("generation_mode") != GENERATION_MODE:
        raise ContractError("unexpected generation mode")
    if header.get("baseline_scope") != BASELINE_SCOPE:
        raise ContractError("unexpected baseline scope")
    if header.get("degraded") is not False:
        raise ContractError("standalone baseline cannot be degraded")
    if header.get("excluded_evidence") != EXCLUDED_EVIDENCE:
        raise ContractError("unexpected excluded evidence scope")


def validate_footer(footer: dict[str, Any], counts: dict[str, int]) -> None:
    fields = {
        "release_count": "release",
        "medium_count": "medium",
        "track_count": "track",
        "file_count": "file",
        "credit_count": "credit",
        "field_evidence_count": "field_evidence",
    }
    for field, key in fields.items():
        if require_integer(footer.get(field), field) != counts[key]:
            raise ContractError(f"footer {field} mismatch")
    require_hash(footer.get("records_sha256"), "rows digest")


def canonical_credits(raw: Any, label: str) -> list[dict[str, str]]:
    credits: list[dict[str, str]] = []
    seen: set[tuple[str, str]] = set()
    for item in require_list(raw, label):
        if not isinstance(item, dict):
            raise ContractError(f"invalid {label}")
        role = require_text(item.get("role"), f"{label} role", allow_empty=False)
        name = require_text(item.get("name"), f"{label} name", allow_empty=False)
        identity = (role, name)
        if identity in seen:
            raise ContractError(f"duplicate {label}")
        seen.add(identity)
        credits.append({"role": role, "name": name})
    return sorted(credits, key=lambda value: (value["role"], value["name"]))


def normalize_releases(raw_releases: list[dict[str, Any]]) -> tuple[list[dict[str, Any]], dict[str, int]]:
    normalized: list[dict[str, Any]] = []
    release_refs: set[str] = set()
    release_keys: set[str] = set()
    global_track_keys: set[str] = set()
    global_file_keys: set[str] = set()
    counts = {"release": 0, "medium": 0, "track": 0, "file": 0, "credit": 0, "field_evidence": 0}

    for raw in raw_releases:
        reference = require_text(raw.get("reference"), "release reference", allow_empty=False)
        if REFERENCE_RE.fullmatch(reference) is None or reference in release_refs:
            raise ContractError("invalid or duplicate release reference")
        release_refs.add(reference)

        files: list[dict[str, Any]] = []
        file_by_path: dict[str, dict[str, Any]] = {}
        for raw_file in require_list(raw.get("files"), "release files"):
            if not isinstance(raw_file, dict):
                raise ContractError("invalid release file")
            path = relative_path(raw_file.get("relative_path"), "file path")
            if path in file_by_path:
                raise ContractError("duplicate file path in release")
            source_key = digest("source\x00" + path)
            if source_key in global_file_keys:
                raise ContractError("physical file belongs to multiple releases")
            global_file_keys.add(source_key)
            file_row = {
                "key": source_key,
                "source_key": source_key,
                "media": require_text(raw_file.get("media", ""), "file media"),
                "size": require_integer(raw_file.get("size"), "file size"),
            }
            files.append(file_row)
            file_by_path[path] = file_row

        pending_media: list[dict[str, Any]] = []
        medium_positions: set[int] = set()
        for raw_medium in require_list(raw.get("media"), "release media"):
            if not isinstance(raw_medium, dict):
                raise ContractError("invalid medium")
            medium_position = require_integer(raw_medium.get("position"), "medium position", minimum=1)
            if medium_position in medium_positions:
                raise ContractError("duplicate medium position")
            medium_positions.add(medium_position)
            tracks: list[dict[str, Any]] = []
            track_positions: set[int] = set()
            for raw_track in require_list(raw_medium.get("tracks"), "medium tracks"):
                if not isinstance(raw_track, dict):
                    raise ContractError("invalid track")
                position = require_integer(raw_track.get("position"), "track position", minimum=1)
                if position in track_positions:
                    raise ContractError("duplicate track position")
                track_positions.add(position)
                source_kind = require_text(raw_track.get("source_kind"), "track source kind", allow_empty=False)
                parent_file_key: str | None
                cue_index_frames: int | None = None
                cue_end_frames: int | None = None
                cue_isrc = ""
                if source_kind == "physical":
                    path = relative_path(raw_track.get("relative_path"), "track path")
                    if path not in file_by_path:
                        raise ContractError("physical track has no file row")
                    source_key = digest("source\x00" + path)
                    parent_file_key = source_key
                elif source_kind == "cue_virtual":
                    sheet = relative_path(raw_track.get("cue_sheet_path"), "CUE sheet path")
                    parent = relative_path(raw_track.get("cue_parent_relative_path"), "CUE parent path")
                    if parent not in file_by_path:
                        raise ContractError("CUE track has no parent file row")
                    cue_index_frames = require_integer(raw_track.get("cue_index_frames", 0), "CUE index frames")
                    if raw_track.get("cue_end_frames") is not None:
                        cue_end_frames = require_integer(raw_track.get("cue_end_frames"), "CUE end frames")
                        if cue_end_frames <= cue_index_frames:
                            raise ContractError("invalid CUE end frames")
                    cue_isrc = require_text(raw_track.get("cue_isrc", ""), "CUE ISRC")
                    source_key = digest(f"cue\x00{sheet}\x00{parent}\x00{position}\x00{cue_index_frames}")
                    parent_file_key = file_by_path[parent]["key"]
                else:
                    raise ContractError("unknown track source kind")
                if source_key in global_track_keys:
                    raise ContractError("duplicate track source key")
                global_track_keys.add(source_key)
                credits = canonical_credits(raw_track.get("credits", []), "track credits")
                track = {
                    "key": source_key,
                    "position": position,
                    "source_key": source_key,
                    "parent_file_key": parent_file_key,
                    "title": require_text(raw_track.get("title", ""), "track title"),
                    "artist": require_text(raw_track.get("artist", ""), "track artist"),
                    "source_kind": source_kind,
                    "duration_ms": require_integer(raw_track.get("duration_ms", 0), "track duration"),
                    "codec": require_text(raw_track.get("codec", ""), "track codec"),
                    "sample_rate": require_integer(raw_track.get("sample_rate", 0), "track sample rate"),
                    "channels": require_integer(raw_track.get("channels", 0), "track channels"),
                    "bitrate": require_integer(raw_track.get("bitrate", 0), "track bitrate"),
                    "bit_depth": require_integer(raw_track.get("bit_depth", 0), "track bit depth"),
                    "cue_index_frames": cue_index_frames,
                    "cue_end_frames": cue_end_frames,
                    "cue_isrc": cue_isrc,
                    "credits": credits,
                }
                tracks.append(track)
            if not tracks:
                raise ContractError("medium has no tracks")
            pending_media.append(
                {
                    "position": medium_position,
                    "title": "",
                    "format": require_text(raw_medium.get("format", ""), "medium format"),
                    "tracks": sorted(tracks, key=lambda value: (value["position"], value["key"])),
                }
            )

        if not pending_media or not files:
            raise ContractError("release graph is empty")
        # Release 身份锚定 candidate 中每个已解析物理来源，而不仅是 Track 投影。
        # 即使旧 assembler 静默遗漏已解析文件，V0/current 仍可比较。
        release_key = digest("release\x00" + "\x00".join(sorted(file["source_key"] for file in files)))
        if release_key in release_keys:
            raise ContractError("duplicate release key")
        release_keys.add(release_key)

        for file in files:
            file["release_key"] = release_key

        for medium in pending_media:
            medium["key"] = digest(f"medium\x00{release_key}\x00{medium['position']}")
            medium["release_key"] = release_key
            for track in medium["tracks"]:
                track["medium_key"] = medium["key"]

        release_credits = canonical_credits(raw.get("credits", []), "release credits")
        field_evidence: list[dict[str, str]] = []
        seen_fields: set[str] = set()
        for raw_evidence in require_list(raw.get("field_evidence"), "field evidence"):
            if not isinstance(raw_evidence, dict):
                raise ContractError("invalid field evidence")
            field = canonical_release_field(
                require_text(raw_evidence.get("field"), "field evidence key", allow_empty=False)
            )
            if field in seen_fields:
                raise ContractError("duplicate field evidence")
            seen_fields.add(field)
            field_evidence.append(
                {
                    "field": field,
                    "value": require_text(raw_evidence.get("value", ""), "field evidence value"),
                    "source": require_text(raw_evidence.get("source", ""), "field evidence source"),
                    "confidence": require_text(raw_evidence.get("confidence", ""), "field evidence confidence"),
                    "action": require_text(raw_evidence.get("action", ""), "field evidence action"),
                    "rule_id": require_text(raw_evidence.get("rule_id", ""), "field evidence rule ID"),
                }
            )

        parent_collection = relative_path(
            raw.get("parent_collection_path", ""),
            "parent collection path",
            allow_empty=True,
        )
        candidate_kind = require_text(raw.get("candidate_kind", ""), "candidate kind", allow_empty=False)
        normalized.append(
            {
                "key": release_key,
                "title": require_text(raw.get("title", ""), "release title"),
                "artist": require_text(raw.get("album_artist", ""), "release artist"),
                "album_artist": require_text(raw.get("album_artist", ""), "album artist"),
                "year": require_integer(raw.get("year", 0), "release year"),
                "source_type": require_text(raw.get("source_type", ""), "source type"),
                "media_type": require_text(raw.get("media_type", ""), "media type"),
                "edition": require_text(raw.get("edition", ""), "edition"),
                "label": require_text(raw.get("label", ""), "label"),
                "catalog": require_text(raw.get("catalog", ""), "catalog"),
                "genre": require_text(raw.get("genre", ""), "genre"),
                "country": require_text(raw.get("country", ""), "country"),
                "barcode": require_text(raw.get("barcode", ""), "barcode"),
                "provider": require_text(raw.get("provider", ""), "provider"),
                "release_type": require_text(raw.get("release_type", ""), "release type"),
                "candidate_kind": candidate_kind,
                "parent_collection_key": digest("collection\x00" + parent_collection) if parent_collection else "",
                "grouping_track_count": require_integer(raw.get("grouping_track_count", 0), "grouping track count"),
                "grouping_medium_count": require_integer(raw.get("grouping_medium_count", 0), "grouping medium count"),
                "media": sorted(pending_media, key=lambda value: (value["position"], value["key"])),
                "files": sorted(files, key=lambda value: value["key"]),
                "credits": release_credits,
                "field_evidence": sorted(field_evidence, key=lambda value: value["field"]),
            }
        )
        counts["release"] += 1
        counts["medium"] += len(pending_media)
        counts["track"] += sum(len(medium["tracks"]) for medium in pending_media)
        counts["file"] += len(files)
        counts["credit"] += len(release_credits) + sum(
            len(track["credits"])
            for medium in pending_media
            for track in medium["tracks"]
        )
        counts["field_evidence"] += len(field_evidence)

    if not normalized:
        raise ContractError("no normalized releases")
    return sorted(normalized, key=lambda value: value["key"]), counts


def schema_digest() -> str:
    return hashlib.sha256((SCHEMA_SQL + "\n").encode("utf-8")).hexdigest()


def insert_reference(
    connection: sqlite3.Connection,
    releases: list[dict[str, Any]],
    manifest: dict[str, str],
) -> None:
    connection.executescript(SCHEMA_SQL)
    with connection:
        connection.executemany(
            "INSERT INTO baseline_manifest(key,value) VALUES(?,?)",
            sorted(manifest.items()),
        )
        for release in releases:
            connection.execute(
                """INSERT INTO releases(
                    key,title,artist,album_artist,year,source_type,media_type,edition,label,
                    catalog,genre,country,barcode,provider,release_type,candidate_kind
                ) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
                (
                    release["key"], release["title"], release["artist"], release["album_artist"],
                    release["year"], release["source_type"], release["media_type"], release["edition"],
                    release["label"], release["catalog"], release["genre"], release["country"],
                    release["barcode"], release["provider"], release["release_type"], release["candidate_kind"],
                ),
            )
            for file in release["files"]:
                connection.execute(
                    "INSERT INTO files(key,release_key,source_key,media,size) VALUES(?,?,?,?,?)",
                    (file["key"], release["key"], file["source_key"], file["media"], file["size"]),
                )
            for medium in release["media"]:
                connection.execute(
                    "INSERT INTO media(key,release_key,position,title,format) VALUES(?,?,?,?,?)",
                    (medium["key"], release["key"], medium["position"], medium["title"], medium["format"]),
                )
                for track in medium["tracks"]:
                    connection.execute(
                        """INSERT INTO tracks(
                            key,medium_key,position,source_key,parent_file_key,title,artist,source_kind,
                            duration_ms,codec,sample_rate,channels,bitrate,bit_depth,cue_index_frames,
                            cue_end_frames,cue_isrc
                        ) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
                        (
                            track["key"], medium["key"], track["position"], track["source_key"],
                            track["parent_file_key"], track["title"], track["artist"], track["source_kind"],
                            track["duration_ms"], track["codec"], track["sample_rate"], track["channels"],
                            track["bitrate"], track["bit_depth"], track["cue_index_frames"],
                            track["cue_end_frames"], track["cue_isrc"],
                        ),
                    )
                    for position, credit in enumerate(track["credits"], start=1):
                        connection.execute(
                            "INSERT INTO track_credits(track_key,position,role,name) VALUES(?,?,?,?)",
                            (track["key"], position, credit["role"], credit["name"]),
                        )
            for position, credit in enumerate(release["credits"], start=1):
                connection.execute(
                    "INSERT INTO release_credits(release_key,position,role,name) VALUES(?,?,?,?)",
                    (release["key"], position, credit["role"], credit["name"]),
                )
            for evidence in release["field_evidence"]:
                connection.execute(
                    """INSERT INTO release_field_evidence(
                        release_key,field,value,source,confidence,action,rule_id
                    ) VALUES(?,?,?,?,?,?,?)""",
                    (
                        release["key"], evidence["field"], evidence["value"], evidence["source"],
                        evidence["confidence"], evidence["action"], evidence["rule_id"],
                    ),
                )
            connection.execute(
                """INSERT INTO release_grouping_evidence(
                    release_key,candidate_kind,parent_collection_key,track_count,medium_count
                ) VALUES(?,?,?,?,?)""",
                (
                    release["key"], release["candidate_kind"], release["parent_collection_key"],
                    release["grouping_track_count"], release["grouping_medium_count"],
                ),
            )


def row_count(connection: sqlite3.Connection, table: str) -> int:
    allowed = {
        "releases", "media", "tracks", "files", "release_credits", "track_credits",
        "release_field_evidence", "release_grouping_evidence",
    }
    if table not in allowed:
        raise ContractError("invalid count table")
    row = connection.execute(f"SELECT COUNT(*) FROM {table}").fetchone()
    if row is None:
        raise ContractError("missing count row")
    return int(row[0])


def validate_database(connection: sqlite3.Connection, counts: dict[str, int]) -> None:
    integrity = connection.execute("PRAGMA integrity_check").fetchone()
    if integrity != ("ok",):
        raise ContractError("SQLite integrity check failed")
    if connection.execute("PRAGMA foreign_key_check").fetchone() is not None:
        raise ContractError("SQLite foreign key check failed")
    expected = {
        "releases": counts["release"],
        "media": counts["medium"],
        "tracks": counts["track"],
        "files": counts["file"],
        "release_field_evidence": counts["field_evidence"],
    }
    for table, amount in expected.items():
        if row_count(connection, table) != amount:
            raise ContractError(f"SQLite {table} count mismatch")
    stored_credits = row_count(connection, "release_credits") + row_count(connection, "track_credits")
    if stored_credits != counts["credit"]:
        raise ContractError("SQLite credit count mismatch")
    if row_count(connection, "release_grouping_evidence") != counts["release"]:
        raise ContractError("SQLite grouping count mismatch")


def load_manifest(connection: sqlite3.Connection) -> dict[str, str]:
    return {str(key): str(value) for key, value in connection.execute(
        "SELECT key,value FROM baseline_manifest ORDER BY key"
    )}


def canonical_snapshot(connection: sqlite3.Connection) -> dict[str, Any]:
    manifest = load_manifest(connection)
    required_manifest = {
        "snapshot_version", "implementation", "generation_mode", "baseline_scope", "degraded",
        "excluded_evidence", "corpus_digest", "code_hash", "adapter_hash", "schema_digest",
    }
    if not required_manifest.issubset(manifest):
        raise ContractError("incomplete SQLite manifest")

    releases: list[dict[str, Any]] = []
    for row in connection.execute(
        """SELECT key,title,artist,album_artist,year,source_type,media_type,edition,label,
                  catalog,genre,country,barcode,provider,release_type,candidate_kind
           FROM releases ORDER BY key"""
    ):
        key = str(row[0])
        medium_keys = [str(value[0]) for value in connection.execute(
            "SELECT key FROM media WHERE release_key=? ORDER BY position,key", (key,)
        )]
        credits = [
            {"role": str(value[0]), "name": str(value[1])}
            for value in connection.execute(
                "SELECT role,name FROM release_credits WHERE release_key=? ORDER BY role,name", (key,)
            )
        ]
        fields: dict[str, str] = {}
        extras = {
            "country": str(row[11]),
            "barcode": str(row[12]),
            "provider": str(row[13]),
            "release_type": str(row[14]),
            "candidate_kind": str(row[15]),
        }
        fields.update({name: value for name, value in extras.items() if value})
        evidence: list[dict[str, str]] = []
        for value in connection.execute(
            """SELECT field,value,source,confidence,action,rule_id
               FROM release_field_evidence WHERE release_key=? ORDER BY field""",
            (key,),
        ):
            field = canonical_release_field(str(value[0]))
            if field in fields and fields[field] != str(value[1]):
                raise ContractError("conflicting canonical release field")
            fields[field] = str(value[1])
            evidence.append(
                {
                    "field": field, "value": str(value[1]), "source": str(value[2]),
                    "confidence": str(value[3]), "action": str(value[4]), "rule_id": str(value[5]),
                }
            )
        evidence.sort(key=lambda value: value["field"])
        grouping = connection.execute(
            """SELECT parent_collection_key,track_count,medium_count
               FROM release_grouping_evidence WHERE release_key=?""",
            (key,),
        ).fetchone()
        if grouping is None:
            raise ContractError("missing grouping evidence")
        if grouping[0]:
            fields["parent_collection_key"] = str(grouping[0])
        fields["grouping_track_count"] = str(grouping[1])
        fields["grouping_medium_count"] = str(grouping[2])
        releases.append(
            {
                "key": key,
                "title": str(row[1]),
                "artist": str(row[2]),
                "album_artist": str(row[3]),
                "year": int(row[4]),
                "source_type": canonical_token(str(row[5])),
                "media_type": canonical_token(str(row[6])),
                "edition": str(row[7]),
                "label": str(row[8]),
                "catalog": str(row[9]),
                "genre": str(row[10]),
                "medium_keys": medium_keys,
                "fields": dict(sorted(fields.items())),
                "credits": credits,
                "evidence": evidence,
            }
        )

    media: list[dict[str, Any]] = []
    for row in connection.execute(
        "SELECT key,release_key,position,title,format FROM media ORDER BY release_key,position,key"
    ):
        key = str(row[0])
        media.append(
            {
                "key": key,
                "release_key": str(row[1]),
                "position": int(row[2]),
                "title": str(row[3]),
                "format": canonical_token(str(row[4])),
                "track_keys": [
                    str(value[0])
                    for value in connection.execute(
                        "SELECT key FROM tracks WHERE medium_key=? ORDER BY position,key", (key,)
                    )
                ],
            }
        )

    tracks: list[dict[str, Any]] = []
    for row in connection.execute(
        """SELECT key,medium_key,position,source_key,parent_file_key,title,artist,source_kind,duration_ms,codec,
                  sample_rate,channels,bitrate,bit_depth,cue_index_frames,cue_end_frames,cue_isrc
           FROM tracks ORDER BY medium_key,position,key"""
    ):
        key = str(row[0])
        fields: dict[str, str] = {}
        for name, value in (
            ("duration_ms", row[8]), ("codec", canonical_token(str(row[9]))), ("sample_rate", row[10]),
            ("channels", row[11]), ("bitrate", row[12]), ("bit_depth", row[13]),
            ("cue_index_frames", row[14]), ("cue_end_frames", row[15]), ("cue_isrc", row[16]),
        ):
            if value not in (None, "", 0) or (name == "cue_index_frames" and value == 0):
                fields[name] = str(value)
        credits = [
            {"role": str(value[0]), "name": str(value[1])}
            for value in connection.execute(
                "SELECT role,name FROM track_credits WHERE track_key=? ORDER BY role,name", (key,)
            )
        ]
        tracks.append(
            {
                "key": key,
                "medium_key": str(row[1]),
                "position": int(row[2]),
                "source_key": str(row[3]),
                "parent_source_key": str(row[4]),
                "title": str(row[5]),
                "artist": str(row[6]),
                "source_kind": str(row[7]),
                "fields": dict(sorted(fields.items())),
                "credits": credits,
            }
        )

    files = [
        {
            "key": str(row[0]),
            "release_key": str(row[1]),
            "source_key": str(row[2]),
            "media": canonical_token(str(row[3])),
            # 当前 PostgreSQL schema 没有可靠的物理文件大小。SQLite 仍保存真实值供
            # 本地审计，canonical 明确映射为 0，避免伪造两端可比较性。
            "size": 0,
        }
        for row in connection.execute("SELECT key,release_key,source_key,media,size FROM files ORDER BY key")
    ]

    return {
        "snapshot_version": int(manifest["snapshot_version"]),
        "implementation": manifest["implementation"],
        "corpus_digest": manifest["corpus_digest"],
        "code_hash": manifest["code_hash"],
        "schema_digest": manifest["schema_digest"],
        "adapter_hash": manifest["adapter_hash"],
        "generation_mode": manifest["generation_mode"],
        "baseline_scope": manifest["baseline_scope"],
        "degraded": manifest["degraded"] == "true",
        "excluded_evidence": json.loads(manifest["excluded_evidence"]),
        "releases": releases,
        "media": media,
        "tracks": tracks,
        "files": files,
    }


def encode_snapshot(snapshot: dict[str, Any]) -> bytes:
    return (json.dumps(snapshot, ensure_ascii=True, sort_keys=True, separators=(",", ":")) + "\n").encode("ascii")


def immutable_connection(path: str) -> sqlite3.Connection:
    quoted = urllib.parse.quote(path, safe="/")
    connection = sqlite3.connect(f"file:{quoted}?mode=ro&immutable=1", uri=True)
    connection.execute("PRAGMA query_only = ON")
    connection.execute("PRAGMA foreign_keys = ON")
    return connection


def validate_output_path(path: str) -> pathlib.Path:
    candidate = pathlib.Path(path)
    if not candidate.is_absolute() or candidate.exists() or candidate.is_symlink():
        raise ContractError("invalid output path")
    parent_info = os.lstat(candidate.parent)
    if not stat.S_ISDIR(parent_info.st_mode) or stat.S_ISLNK(parent_info.st_mode):
        raise ContractError("invalid output directory")
    return candidate


def validate_input_path(path: str) -> pathlib.Path:
    candidate = pathlib.Path(path)
    if not candidate.is_absolute() or candidate.is_symlink():
        raise ContractError("invalid rows path")
    info = os.lstat(candidate)
    if not stat.S_ISREG(info.st_mode):
        raise ContractError("invalid rows path")
    return candidate


def write_file(path: str, content: bytes) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(descriptor, "wb", closefd=False) as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
    finally:
        os.close(descriptor)


def publish_no_replace(temporary: str, destination: pathlib.Path) -> None:
    """在同一目录内原子发布且绝不覆盖并发出现的目标。"""
    os.link(temporary, destination)
    os.unlink(temporary)


def build_reference(
    *,
    rows_path: str,
    database_path: str,
    snapshot_path: str,
    corpus_digest: str,
    code_hash: str,
    adapter_hash: str,
    generated_at: str | None = None,
) -> dict[str, Any]:
    rows = validate_input_path(rows_path)
    database = validate_output_path(database_path)
    snapshot = validate_output_path(snapshot_path)
    if database == snapshot:
        raise ContractError("database and snapshot paths overlap")
    corpus_digest = require_hash(corpus_digest, "corpus digest")
    code_hash = require_hash(code_hash, "code hash")
    adapter_hash = require_hash(adapter_hash, "adapter hash")
    if generated_at is None:
        generated_at = dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")
    generated_at = require_text(generated_at, "generation time", allow_empty=False)

    header, raw_releases, footer = load_rows(str(rows))
    validate_header(header)
    releases, counts = normalize_releases(raw_releases)
    validate_footer(footer, counts)

    database_fd, database_temporary = tempfile.mkstemp(prefix=".v0-reference-", suffix=".sqlite", dir=database.parent)
    os.close(database_fd)
    snapshot_fd, snapshot_temporary = tempfile.mkstemp(prefix=".v0-snapshot-", suffix=".json", dir=snapshot.parent)
    os.close(snapshot_fd)
    os.chmod(database_temporary, 0o600)
    os.chmod(snapshot_temporary, 0o600)
    published_database = False
    published_snapshot = False
    try:
        manifest = {
            "sqlite_schema_version": str(SQLITE_SCHEMA_VERSION),
            "snapshot_version": str(SNAPSHOT_VERSION),
            "implementation": IMPLEMENTATION,
            "generation_mode": GENERATION_MODE,
            "baseline_scope": BASELINE_SCOPE,
            "degraded": "false",
            "excluded_evidence": json.dumps(EXCLUDED_EVIDENCE, ensure_ascii=True, separators=(",", ":")),
            "corpus_digest": corpus_digest,
            "code_hash": code_hash,
            "adapter_hash": adapter_hash,
            "schema_digest": schema_digest(),
            "generated_at": generated_at,
            "rows_sha256": require_hash(footer["records_sha256"], "rows digest"),
            "release_count": str(counts["release"]),
            "medium_count": str(counts["medium"]),
            "track_count": str(counts["track"]),
            "file_count": str(counts["file"]),
            "credit_count": str(counts["credit"]),
            "field_evidence_count": str(counts["field_evidence"]),
        }
        connection = sqlite3.connect(database_temporary)
        try:
            connection.execute("PRAGMA journal_mode = DELETE")
            connection.execute("PRAGMA synchronous = FULL")
            insert_reference(connection, releases, manifest)
            validate_database(connection, counts)
            first_snapshot = canonical_snapshot(connection)
            encoded = encode_snapshot(first_snapshot)
            canonical_hash = hashlib.sha256(encoded).hexdigest()
            with connection:
                connection.execute(
                    "INSERT INTO baseline_manifest(key,value) VALUES('canonical_sha256',?)",
                    (canonical_hash,),
                )
            validate_database(connection, counts)
        finally:
            connection.close()

        read_only = immutable_connection(database_temporary)
        try:
            validate_database(read_only, counts)
            round_trip_snapshot = canonical_snapshot(read_only)
        finally:
            read_only.close()
        round_trip_encoded = encode_snapshot(round_trip_snapshot)
        if round_trip_encoded != encoded or hashlib.sha256(round_trip_encoded).hexdigest() != canonical_hash:
            raise ContractError("SQLite canonical round-trip mismatch")

        write_file(snapshot_temporary + ".complete", round_trip_encoded)
        os.replace(snapshot_temporary + ".complete", snapshot_temporary)
        publish_no_replace(database_temporary, database)
        published_database = True
        publish_no_replace(snapshot_temporary, snapshot)
        published_snapshot = True
        os.chmod(database, 0o600)
        os.chmod(snapshot, 0o600)
        return {
            "counts": counts,
            "schema_digest": schema_digest(),
            "canonical_sha256": canonical_hash,
        }
    except Exception:
        if published_snapshot:
            os.unlink(snapshot)
        if published_database:
            os.unlink(database)
        raise
    finally:
        for temporary in (database_temporary, snapshot_temporary, snapshot_temporary + ".complete"):
            try:
                os.unlink(temporary)
            except FileNotFoundError:
                pass


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="构建 V0 standalone normalized SQLite 基准")
    subparsers = parser.add_subparsers(dest="command", required=True)
    build = subparsers.add_parser("build", help="从 adapter NDJSON 构建 SQLite 与 canonical JSON")
    build.add_argument("--rows", required=True)
    build.add_argument("--database", required=True)
    build.add_argument("--snapshot", required=True)
    build.add_argument("--corpus-digest", required=True)
    build.add_argument("--code-hash", required=True)
    build.add_argument("--adapter-hash", required=True)
    build.add_argument("--generated-at")
    return parser


def main(argv: Iterable[str] | None = None) -> int:
    parser = build_parser()
    arguments = parser.parse_args(list(argv) if argv is not None else None)
    if arguments.command != "build":
        return 2
    try:
        build_reference(
            rows_path=arguments.rows,
            database_path=arguments.database,
            snapshot_path=arguments.snapshot,
            corpus_digest=arguments.corpus_digest,
            code_hash=arguments.code_hash,
            adapter_hash=arguments.adapter_hash,
            generated_at=arguments.generated_at,
        )
    except (OSError, sqlite3.Error, ContractError, KeyError, TypeError, ValueError):
        # 失败详情可能包含私有 metadata；runner 只需要稳定失败类别。
        print("v0-reference-sqlite: build_failed", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
