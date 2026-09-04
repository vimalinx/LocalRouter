#!/usr/bin/env python3
"""Validate the staged LocalRouter open-source distribution boundary."""

from __future__ import annotations

import json
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def git(*args: str) -> bytes:
    return subprocess.check_output(["git", "-C", str(ROOT), *args])


def candidate_files() -> list[str]:
    return [
        value.decode()
        for value in git("ls-files", "--cached", "--others", "--exclude-standard", "-z").split(b"\0")
        if value
    ]


def direct_go_modules() -> set[str]:
    text = (ROOT / "gateway/go.mod").read_text()
    block = text.split("require (", 1)[1].split(")\n\nrequire (", 1)[0]
    return {
        line.strip().split()[0]
        for line in block.splitlines()
        if line.strip() and not line.lstrip().startswith("//")
    }


def main() -> int:
    required = {
        ".gitignore",
        ".goreleaser.yaml",
        ".github/workflows/ci.yml",
        ".github/workflows/release.yml",
        ".dockerignore",
        "Dockerfile",
        "compose.yaml",
        "CHANGELOG.md",
        "Makefile",
        "VERSION",
        "LICENSE",
        "NOTICE",
        "README.md",
        "SECURITY.md",
        "CONTRIBUTING.md",
        "PROVENANCE.md",
        "THIRD-PARTY-LICENSES.md",
        "docs/OPEN_SOURCE_RELEASE.md",
        "docs/DOCKER.md",
        "packaging/localrouter.env.example",
        "packaging/docker/localrouter.env.example",
        "packaging/docker/compose.bind.yaml",
        "packaging/agent-contract/AGENTS.localrouter.md",
        "packaging/systemd/localrouter.service.in",
        "tools/install-localrouter.sh",
        "tests/release_artifact_acceptance.sh",
        "tests/xdg_install_acceptance.sh",
        "tests/lan_service_acceptance.sh",
        "tests/docker_acceptance.sh",
        ".agents/skills/localrouter-protocol-pack/SKILL.md",
        ".agents/skills/localrouter-protocol-pack/references/runtime-handoff.md",
        "gateway/protocols/catalogs/pool-catalog.json",
        "gateway/protocols/catalogs/pool-catalog.md",
    }
    tracked = set(candidate_files())
    missing = sorted(required - tracked)
    assert not missing, f"release files missing from the non-ignored release candidate: {missing}"

    forbidden_prefixes = (
        ".ai/",
        ".ruff_cache/",
        "gateway/data/",
        "gateway/logs/",
        "gateway/state-locks/",
        "gateway/protocol-state/",
        "gateway/workflow-state/",
        "gateway/protocol-revisions/",
        "gateway/protocol-drafts/",
        "gateway/web-src/node_modules/",
        "upstream/",
    )
    forbidden = sorted(name for name in tracked if name.startswith(forbidden_prefixes))
    assert not forbidden, f"private/generated paths tracked: {forbidden[:8]}"
    generated_gateway_binaries = {
        name
        for name in tracked
        if name == "gateway/localrouter"
        or name.startswith("gateway/localrouter.")
    }
    assert not generated_gateway_binaries, (
        f"generated gateway binary tracked: {sorted(generated_gateway_binaries)}"
    )

    stage = git("ls-files", "--stage").decode().splitlines()
    assert not any(line.startswith("160000 ") for line in stage), (
        "release must not depend on Git submodules"
    )
    assert (ROOT / "gateway/LICENSE").resolve() == (ROOT / "LICENSE").resolve()

    absolute_home = re.compile(rb"/home/[A-Za-z0-9._-]+/")
    leaked_paths: list[str] = []
    for name in tracked:
        path = ROOT / name
        if not path.is_file() or path.is_symlink():
            continue
        body = path.read_bytes()
        if b"\0" not in body and absolute_home.search(body):
            leaked_paths.append(name)
    assert not leaked_paths, f"machine-specific home paths tracked: {leaked_paths}"

    owner_placeholder = b"OWNER" + b"/REPOSITORY"
    repository_placeholder = b"<repository" + b"-url>"
    placeholders = [
        name
        for name in tracked
        if (ROOT / name).is_file()
        and not (ROOT / name).is_symlink()
        and (
            owner_placeholder in (ROOT / name).read_bytes()
            or repository_placeholder in (ROOT / name).read_bytes()
        )
    ]
    assert not placeholders, f"repository owner placeholders remain: {placeholders}"

    repository_url = "https://github.com/vimalinx/LocalRouter"
    for name in ("README.md", "CONTRIBUTING.md", "SECURITY.md"):
        assert repository_url in (ROOT / name).read_text(), (
            f"{name} does not name the canonical public repository"
        )

    version = (ROOT / "VERSION").read_text().strip()
    changelog = (ROOT / "CHANGELOG.md").read_text()
    assert re.search(
        rf"^## {re.escape(version)} - \d{{4}}-\d{{2}}-\d{{2}}$",
        changelog,
        re.MULTILINE,
    ), f"CHANGELOG.md has no dated entry for VERSION={version}"

    legacy_catalogs = {
        "gateway/protocols/catalogs/hao-pools.json",
        "gateway/protocols/catalogs/hao-pools.md",
    }
    assert not (legacy_catalogs & tracked), (
        "operator-specific pool catalog snapshots must not ship in source"
    )
    bundled_packs = sorted(
        name
        for name in tracked
        if name.startswith("gateway/protocols/")
        and name.count("/") == 2
        and name.endswith(".json")
    )
    assert not bundled_packs, f"provider Packs must not ship in source: {bundled_packs}"
    packaged_sidecars = sorted(
        name
        for name in tracked
        if name.startswith("gateway/cmd/")
        or name.startswith("packaging/systemd/localrouter-adapter-")
    )
    assert not packaged_sidecars, (
        f"provider-specific adapters must not ship in source: {packaged_sidecars}"
    )
    public_surfaces = [
        name
        for name in tracked
        if name == "README.md"
        or name.startswith("docs/")
        or name.startswith("gateway/protocols/")
    ]
    operator_markers: dict[str, list[str]] = {}
    for name in public_surfaces:
        path = ROOT / name
        if not path.is_file() or path.is_symlink():
            continue
        try:
            text = path.read_text()
        except UnicodeDecodeError:
            continue
        matches = [
            marker
            for marker, pattern in (
                ("hao", r"\bhao\b"),
                ("52token", r"52token"),
                ("HAO_ROOT", r"HAO_ROOT"),
            )
            if re.search(pattern, text, re.IGNORECASE)
        ]
        if matches:
            operator_markers[name] = matches
    assert not operator_markers, (
        f"operator-specific markers remain on public surfaces: {operator_markers}"
    )

    generated_whitespace: list[str] = []
    for path in (ROOT / "gateway/web").rglob("*"):
        if path.suffix not in {".css", ".html", ".js"} or not path.is_file():
            continue
        if re.search(rb"\n[\t ]+\n", path.read_bytes()):
            generated_whitespace.append(str(path.relative_to(ROOT)))
    assert not generated_whitespace, (
        f"generated Web assets contain whitespace-only lines: {generated_whitespace}"
    )

    inventory = (ROOT / "THIRD-PARTY-LICENSES.md").read_text()
    web = json.loads((ROOT / "gateway/web-src/package.json").read_text())
    dependencies = (
        direct_go_modules()
        | set(web.get("dependencies", {}))
        | set(web.get("devDependencies", {}))
    )
    unattributed = sorted(name for name in dependencies if f"`{name}`" not in inventory)
    assert not unattributed, (
        f"direct dependencies missing from inventory: {unattributed}"
    )

    release_config = (ROOT / ".goreleaser.yaml").read_text()
    assert "binary: localrouter\n" in release_config
    assert "localrouter-adapter-" not in release_config
    assert "- packaging/localrouter.env.example\n" in release_config
    assert "- packaging/**/*\n" in release_config
    assert "- .agents/skills/localrouter-protocol-pack/**/*\n" in release_config
    assert "- .agents/skills/localrouter-protocol-pack/SKILL.md\n" in release_config
    assert "- THIRD-PARTY-LICENSES.md\n" in release_config

    print(
        f"open-source release candidate accepted: files={len(tracked)} "
        f"direct_dependencies={len(dependencies)} submodules=0"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
