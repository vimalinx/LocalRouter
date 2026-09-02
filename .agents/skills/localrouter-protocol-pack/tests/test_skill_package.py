from __future__ import annotations

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).parents[1]


class SkillPackageTests(unittest.TestCase):
    def test_every_reference_is_routed_from_the_entrypoint(self) -> None:
        entry = (ROOT / "SKILL.md").read_text()
        routed = set(re.findall(r"\]\((references/[^)]+\.md)\)", entry))
        existing = {
            path.relative_to(ROOT).as_posix()
            for path in (ROOT / "references").glob("*.md")
        }
        self.assertEqual(routed, existing)
        for relative in routed:
            self.assertTrue((ROOT / relative).is_file())

    def test_entrypoint_stays_smaller_than_conditional_detail(self) -> None:
        entry_words = len((ROOT / "SKILL.md").read_text().split())
        reference_words = sum(
            len(path.read_text().split()) for path in (ROOT / "references").glob("*.md")
        )
        self.assertLess(entry_words, 900)
        self.assertGreater(reference_words, entry_words * 3)

    def test_doctor_is_reachable_and_executable(self) -> None:
        entry = (ROOT / "SKILL.md").read_text()
        script = ROOT / "scripts" / "protocol_pack_doctor.py"
        self.assertIn("scripts/protocol_pack_doctor.py", entry)
        self.assertTrue(script.is_file())
        self.assertTrue(script.stat().st_mode & 0o111)


if __name__ == "__main__":
    unittest.main()
