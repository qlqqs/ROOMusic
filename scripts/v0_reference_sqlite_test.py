#!/usr/bin/env python3

from __future__ import annotations

import copy
import hashlib
import importlib.util
import json
import os
import pathlib
import sqlite3
import tempfile
import unittest
from unittest import mock


MODULE_PATH = pathlib.Path(__file__).with_name("v0_reference_sqlite.py")
SPEC = importlib.util.spec_from_file_location("v0_reference_sqlite", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def write_rows(path: pathlib.Path, releases: list[dict], *, corrupt_digest: bool = False) -> None:
    header = {
        "record_type": "header",
        "format_version": 1,
        "implementation": "v0_release_graph_generated_corrected",
        "generation_mode": "standalone_scanner",
        "baseline_scope": "release_graph_only",
        "degraded": False,
        "excluded_evidence": [
            "local_evidence",
            "quality_badges",
            "scan_diagnostics",
            "production_runtime_status",
        ],
    }
    encoded_releases = [
        json.dumps(release, ensure_ascii=True, sort_keys=True, separators=(",", ":")).encode("ascii") + b"\n"
        for release in releases
    ]
    counts = {
        "release_count": len(releases),
        "medium_count": sum(len(release["media"]) for release in releases),
        "track_count": sum(len(medium["tracks"]) for release in releases for medium in release["media"]),
        "file_count": sum(len(release["files"]) for release in releases),
        "credit_count": sum(
            len(release["credits"]) + sum(len(track.get("credits", [])) for medium in release["media"] for track in medium["tracks"])
            for release in releases
        ),
        "field_evidence_count": sum(len(release["field_evidence"]) for release in releases),
    }
    rows_hash = hashlib.sha256(b"".join(encoded_releases)).hexdigest()
    footer = {"record_type": "complete", **counts, "records_sha256": "0" * 64 if corrupt_digest else rows_hash}
    with path.open("wb") as handle:
        handle.write(json.dumps(header, sort_keys=True, separators=(",", ":")).encode("ascii") + b"\n")
        for encoded in encoded_releases:
            handle.write(encoded)
        handle.write(json.dumps(footer, sort_keys=True, separators=(",", ":")).encode("ascii") + b"\n")
    os.chmod(path, 0o600)


def release_fixture() -> dict:
    return {
        "record_type": "release",
        "reference": "a" * 64,
        "title": "Example Album",
        "album_artist": "Example Artist",
        "year": 2024,
        "country": "",
        "catalog": "CAT-1",
        "barcode": "",
        "source_type": "cd",
        "media_type": "cd",
        "provider": "",
        "edition": "",
        "release_type": "album",
        "label": "Example Label",
        "genre": "Rock",
        "candidate_kind": "release",
        "parent_collection_path": "Box",
        "media": [
            {
                "position": 1,
                "format": "FLAC",
                "tracks": [
                    {
                        "position": 1,
                        "title": "One",
                        "artist": "Example Artist",
                        "source_kind": "cue_virtual",
                        "cue_sheet_path": "Box/Album/disc.cue",
                        "cue_parent_relative_path": "Box/Album/image.flac",
                        "cue_index_frames": 0,
                        "cue_end_frames": 750,
                        "duration_ms": 10000,
                        "codec": "FLAC",
                        "sample_rate": 44100,
                        "channels": 2,
                        "bitrate": 900,
                        "bit_depth": 16,
                        "credits": [{"role": "performer", "name": "Example Artist"}],
                    }
                ],
            }
        ],
        "files": [{"relative_path": "Box/Album/image.flac", "media": "FLAC", "size": 123}],
        "credits": [{"role": "album_artist", "name": "Example Artist"}],
        "field_evidence": [
            {
                "field": "album_title",
                "value": "Example Album",
                "source": "tag",
                "confidence": "high",
                "action": "auto_apply",
                "rule_id": "tag.album.explicit",
            }
        ],
        "grouping_track_count": 1,
        "grouping_medium_count": 1,
    }


class ReferenceSQLiteTest(unittest.TestCase):
    def test_builds_normalized_sqlite_and_round_trips_canonical_snapshot(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            rows = root / "rows.ndjson"
            database = root / "reference.sqlite"
            snapshot = root / "snapshot.json"
            write_rows(rows, [release_fixture()])

            result = MODULE.build_reference(
                rows_path=str(rows),
                database_path=str(database),
                snapshot_path=str(snapshot),
                corpus_digest="b" * 64,
                code_hash="c" * 64,
                adapter_hash="d" * 64,
                generated_at="2026-09-03T00:00:00Z",
            )
            self.assertEqual(result["counts"]["release"], 1)
            self.assertEqual(result["counts"]["track"], 1)
            self.assertEqual(database.stat().st_mode & 0o777, 0o600)
            self.assertEqual(snapshot.stat().st_mode & 0o777, 0o600)

            with sqlite3.connect(database) as connection:
                self.assertEqual(connection.execute("PRAGMA integrity_check").fetchone(), ("ok",))
                self.assertIsNone(connection.execute("PRAGMA foreign_key_check").fetchone())
                self.assertEqual(connection.execute("SELECT COUNT(*) FROM releases").fetchone(), (1,))
                self.assertEqual(connection.execute("SELECT COUNT(*) FROM files").fetchone(), (1,))
                self.assertEqual(connection.execute("SELECT size FROM files").fetchone(), (123,))
                self.assertEqual(connection.execute("SELECT value FROM baseline_manifest WHERE key='degraded'").fetchone(), ("false",))
                self.assertEqual(connection.execute("SELECT value FROM baseline_manifest WHERE key='generation_mode'").fetchone(), ("standalone_scanner",))

            canonical = json.loads(snapshot.read_text(encoding="ascii"))
            self.assertEqual(canonical["snapshot_version"], 2)
            self.assertEqual(canonical["implementation"], "v0_release_graph_generated_corrected")
            self.assertEqual(canonical["baseline_scope"], "release_graph_only")
            self.assertFalse(canonical["degraded"])
            self.assertEqual(len(canonical["files"]), 1)
            self.assertEqual(canonical["files"][0]["size"], 0)
            self.assertEqual(canonical["files"][0]["release_key"], canonical["releases"][0]["key"])
            self.assertEqual(canonical["tracks"][0]["parent_source_key"], canonical["files"][0]["source_key"])
            self.assertEqual(canonical["tracks"][0]["fields"]["codec"], "flac")
            self.assertEqual(canonical["tracks"][0]["fields"]["cue_index_frames"], "0")
            self.assertIn("title", canonical["releases"][0]["fields"])
            self.assertNotIn("album_title", canonical["releases"][0]["fields"])
            self.assertNotIn("Box/Album/image.flac", database.read_bytes().decode("utf-8", "ignore"))

    def test_release_identity_includes_files_omitted_from_track_projection(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            rows = root / "rows.ndjson"
            database = root / "reference.sqlite"
            snapshot = root / "snapshot.json"
            fixture = release_fixture()
            fixture["files"].append(
                {"relative_path": "Box/Album/omitted.flac", "media": "FLAC", "size": 456}
            )
            write_rows(rows, [fixture])

            MODULE.build_reference(
                rows_path=str(rows), database_path=str(database), snapshot_path=str(snapshot),
                corpus_digest="b" * 64, code_hash="c" * 64, adapter_hash="d" * 64,
                generated_at="2026-09-03T00:00:00Z",
            )

            canonical = json.loads(snapshot.read_text(encoding="ascii"))
            source_keys = sorted(
                MODULE.digest("source\x00" + path)
                for path in ("Box/Album/image.flac", "Box/Album/omitted.flac")
            )
            expected_release_key = MODULE.digest("release\x00" + "\x00".join(source_keys))
            self.assertEqual(canonical["releases"][0]["key"], expected_release_key)
            self.assertEqual({item["release_key"] for item in canonical["files"]}, {expected_release_key})

    def test_failure_leaves_no_partial_baseline(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            rows = root / "rows.ndjson"
            database = root / "reference.sqlite"
            snapshot = root / "snapshot.json"
            fixture = release_fixture()
            fixture["media"][0]["tracks"].append(dict(fixture["media"][0]["tracks"][0]))
            write_rows(rows, [fixture])
            with self.assertRaises(MODULE.ContractError):
                MODULE.build_reference(
                    rows_path=str(rows), database_path=str(database), snapshot_path=str(snapshot),
                    corpus_digest="b" * 64, code_hash="c" * 64, adapter_hash="d" * 64,
                )
            self.assertFalse(database.exists())
            self.assertFalse(snapshot.exists())

    def test_rejects_path_escape_and_rows_digest_drift(self) -> None:
        for mutation in ("path", "foreign_key", "digest"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as directory:
                root = pathlib.Path(directory)
                rows = root / "rows.ndjson"
                fixture = release_fixture()
                if mutation == "path":
                    fixture["files"][0]["relative_path"] = "../private.flac"
                if mutation == "foreign_key":
                    fixture["media"][0]["tracks"][0]["cue_parent_relative_path"] = "missing.flac"
                write_rows(rows, [fixture], corrupt_digest=mutation == "digest")
                with self.assertRaises(MODULE.ContractError):
                    MODULE.build_reference(
                        rows_path=str(rows), database_path=str(root / "reference.sqlite"),
                        snapshot_path=str(root / "snapshot.json"), corpus_digest="b" * 64,
                        code_hash="c" * 64, adapter_hash="d" * 64,
                    )

    def test_rejects_nondeterministic_release_order(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            rows = root / "rows.ndjson"
            first = release_fixture()
            first["reference"] = "b" * 64
            second = copy.deepcopy(first)
            second["reference"] = "a" * 64
            second["files"][0]["relative_path"] = "Other/track.flac"
            second["media"][0]["tracks"][0]["cue_sheet_path"] = "Other/disc.cue"
            second["media"][0]["tracks"][0]["cue_parent_relative_path"] = "Other/track.flac"
            write_rows(rows, [first, second])
            with self.assertRaises(MODULE.ContractError):
                MODULE.build_reference(
                    rows_path=str(rows), database_path=str(root / "reference.sqlite"),
                    snapshot_path=str(root / "snapshot.json"), corpus_digest="b" * 64,
                    code_hash="c" * 64, adapter_hash="d" * 64,
                )

    def test_round_trip_drift_removes_partial_baseline(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            rows = root / "rows.ndjson"
            database = root / "reference.sqlite"
            snapshot = root / "snapshot.json"
            write_rows(rows, [release_fixture()])
            original = MODULE.canonical_snapshot
            calls = 0

            def drifting_snapshot(connection: sqlite3.Connection) -> dict:
                nonlocal calls
                calls += 1
                result = original(connection)
                if calls == 2:
                    result["releases"][0]["title"] = "round-trip drift"
                return result

            with mock.patch.object(MODULE, "canonical_snapshot", side_effect=drifting_snapshot):
                with self.assertRaises(MODULE.ContractError):
                    MODULE.build_reference(
                        rows_path=str(rows), database_path=str(database), snapshot_path=str(snapshot),
                        corpus_digest="b" * 64, code_hash="c" * 64, adapter_hash="d" * 64,
                    )
            self.assertFalse(database.exists())
            self.assertFalse(snapshot.exists())


if __name__ == "__main__":
    unittest.main()
