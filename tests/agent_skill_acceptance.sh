#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
skill_root="$project_root/.agents/skills/localrouter-protocol-pack"

test -f "$skill_root/SKILL.md"
test -f "$skill_root/references/pack-v2.md"
test -f "$skill_root/references/pack-v3.md"
test -f "$skill_root/references/acceptance.md"
test -x "$skill_root/scripts/protocol_pack_doctor.py"
"$project_root/tools/lr" call --help | grep -q 'lr call <pack> <operation>'
"$project_root/tools/lr" find model --help | grep -q 'lr find model'

python3 - "$project_root" <<'PY'
import json
import sys
from pathlib import Path

import jsonschema

root = Path(sys.argv[1])
skill = (root / ".agents/skills/localrouter-protocol-pack/SKILL.md").read_text()
assert skill.startswith("---\nname: localrouter-protocol-pack\n")
frontmatter = skill.split("---", 2)[1]
assert "\ndescription:" in frontmatter
assert len(skill.split()) < 900

for phrase in (
    "Start from the live contract",
    "Load only the relevant detail",
    "Non-negotiable boundaries",
    "Required lifecycle",
    "impact.files",
    "impact.protocols",
    "impact.pool_ids",
    "protocol_pack_doctor.py",
):
    assert phrase in skill, phrase

references = {
    "pack-v2.md",
    "pack-v3.md",
    "protocol-recipes.md",
    "pool-quota.md",
    "security.md",
    "port-authoring.md",
    "agent-documentation.md",
    "compatibility.md",
    "release-lifecycle.md",
    "troubleshooting.md",
    "acceptance.md",
}
for name in references:
    assert (root / ".agents/skills/localrouter-protocol-pack/references" / name).is_file()
    assert f"references/{name}" in skill

agents = (root / "AGENTS.md").read_text()
assert ".agents/skills/localrouter-protocol-pack/SKILL.md" in agents
assert "localrouter.maintain" in agents
assert "/manage/mcp" in agents
assert "structured error" in agents
assert "lr resolve" in agents
assert "lr catalog" in agents
assert "lr preflight" in agents
assert "lr watch" in agents

schema = json.loads((root / "gateway/protocols/schema/protocol-pack-v2.schema.json").read_text())
validator = jsonschema.Draft202012Validator(schema, format_checker=jsonschema.FormatChecker())
schema_v3 = json.loads((root / "gateway/protocols/schema/protocol-pack-v3.schema.json").read_text())
jsonschema.Draft202012Validator.check_schema(schema_v3)
validator_v3 = jsonschema.Draft202012Validator(schema_v3, format_checker=jsonschema.FormatChecker())
for relative in (
    "tests/fixtures/search-v3.json",
    "tests/fixtures/universal-v3.json",
    "tests/fixtures/video-v2.json",
):
    candidate = json.loads((root / relative).read_text())
    (validator_v3 if candidate.get("schema_version") == "3" else validator).validate(candidate)

catalog = json.loads((root / "gateway/protocols/catalogs/pool-catalog.json").read_text())
assert catalog["schema_version"] == "1"
assert catalog["summary"]["indexed"] == len(catalog["pools"]) == 0
assert not list((root / "gateway/protocols").glob("*.json"))

print("Agent Skill lifecycle, AGENTS bootstrap, and Protocol Pack v2/v3 schema acceptance passed")
PY
