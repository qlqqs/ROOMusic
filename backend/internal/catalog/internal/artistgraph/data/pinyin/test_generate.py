"""生成器局部测试：不联网、不需要下载原始压缩包。"""

import unittest

import generate


class GenerateTests(unittest.TestCase):
    def test_sorted_first_reading_and_tones(self):
        table, multiple, unknown = generate.extract(
            "# 夹具\nU+4E7E\tkMandarin\tqián gān\n"
            "U+20000\tkMandarin\thē\nU+3400\tkMandarin\tqiū\n"
            "U+4E7E\tkJapanese\tカン\n".encode())
        self.assertEqual(table, b"U+3400\tQ\nU+4E7E\tQ\nU+20000\tH\n")
        self.assertEqual(multiple, [{"codepoint": "U+4E7E", "readings": ["qián", "gān"],
                                    "selected": "qián", "initial": "Q"}])
        self.assertEqual(unknown, [])

    def test_output_independent_of_input_order(self):
        rows = ["U+4E7E\tkMandarin\tqián gān\n", "U+3400\tkMandarin\tqiū\n",
                "U+3D14\tkMandarin\tjí xī\n"]
        self.assertEqual(generate.extract("".join(rows).encode()),
                         generate.extract("".join(reversed(rows)).encode()))

    def test_combining_tone_and_umlaut(self):
        table, _, _ = generate.extract("U+3400\tkMandarin\tnu\u0308\u030c\n".encode())
        self.assertEqual(table, b"U+3400\tN\n")

    def test_bad_records(self):
        for row in ["U+3400\tkMandarin\t", "U+3400\tkMandarin\t123",
                    "U+3400\tkMandarin\tα", "U+D800\tkMandarin\ta",
                    "U+110000\tkMandarin\ta", "U+03400\tkMandarin\ta",
                    "U+ZZZZ\tkMandarin\ta", "3400\tkMandarin\ta",
                    "U+3400\tkMandarin\ta\textra"]:
            with self.subTest(row=row), self.assertRaises(ValueError):
                generate.extract((row + "\n").encode())

    def test_duplicate(self):
        with self.assertRaises(ValueError):
            generate.extract(b"U+3400\tkMandarin\ta\nU+3400\tkMandarin\tb\n")

    def test_invalid_utf8(self):
        with self.assertRaises(UnicodeDecodeError):
            generate.extract(b"\xff")

    def test_input_hash_mismatch(self):
        with self.assertRaisesRegex(ValueError, "Unihan.zip SHA-256"):
            generate.generate(b"bad zip", b"bad license")
        with self.assertRaisesRegex(ValueError, "LICENSE SHA-256"):
            generate.checked(b"bad license", generate.LICENSE_SHA256, "LICENSE")
        with self.assertRaisesRegex(ValueError, "Unihan_Readings.txt SHA-256"):
            generate.checked(b"bad readings", generate.READINGS_SHA256, "Unihan_Readings.txt")


if __name__ == "__main__":
    unittest.main()
