# Open-source release gate

A release is ready only when it can be reproduced from the Git index without
local runtime state.

## Included

- LocalRouter Go and WebUI source plus embedded WebUI assets
- Protocol Pack schemas, examples, guides, tests, and Agent Skill
- native gateway source with no vendored reference-gateway repository
- XDG installer, user systemd unit, Release archive configuration and CI workflows
- reproducible Dockerfile, hardened Linux host-network Compose deployment, LAN service isolation tests, and migration documentation
- AGPL license, notices, provenance, security policy, and dependency inventory

## Excluded

- legacy `gateway/data`, XDG user directories, logs, `.env`, `.ai`, credentials, cookies, pool sources,
  databases, generated binaries, `node_modules`, caches, and local backups
- registration, CAPTCHA, human OAuth consent, payment, and provider accounts

## Required checks

1. `python tests/open_source_release_test.py` accepts all tracked and
   non-ignored candidate files and rejects Git submodules or vendored upstream
   gateway trees.
2. `./tests/verify.sh` passes without real-provider environment variables.
3. `govulncheck ./...` reports no reachable Go vulnerability.
4. OSV Scanner reports no issue in `gateway/web-src/bun.lock`.
5. A redacted Gitleaks scan of the staged source has no unreviewed finding.
6. Build and test again from an isolated candidate tree that includes tracked,
   modified, deleted, and non-ignored untracked worktree files without changing
   the real Git index.
7. `tests/xdg_install_acceptance.sh` installs into an isolated HOME, starts the
   installed binary, generates a mode-600 API Key, calls `/v1/models` through
   the installed `lr`, and verifies XDG config/data/state/cache separation.
8. Uncached Go tests and the race detector pass, and rebuilding the Web assets
   leaves the candidate tree byte-for-byte clean.
9. A pinned GoReleaser snapshot produces verified amd64 and arm64 archives;
   checksums, CPU architecture, required legal/installer files, global Agent
   Skill installation, and the archive-only install path all pass.
10. `tests/docker_acceptance.sh` builds the pinned multi-stage image, verifies
    non-root/read-only execution, LAN management-route absence, graceful
    shutdown and persistent Token state without contacting a provider.

Run the complete clean-source gate with:

```bash
./tests/clean_release_acceptance.sh
```

The script snapshots the current non-ignored worktree through a private Git
index, performs a frozen Web install, rebuilds all artifacts, runs the
deterministic and race suites, repeats pinned Gitleaks/govulncheck/OSV checks,
builds both release archives, and installs the amd64 archive into an isolated
HOME. It does not touch the real Git index, working runtime, or provider pools.
Set `LOCALROUTER_RELEASE_TREEISH` to validate one exact committed tree instead.

The first public push must start from the accepted release tree as a clean root
commit. Do not publish a pre-release development history merely because its
current tip is clean: deleted pool inventories, private endpoint names, and
operator-specific probes remain recoverable from old Git objects. Preserve any
such history only in a local backup that is never pushed to the public remote.

Publishing, pushing, tagging, signing, and spending on real providers remain
separate explicit actions.

## Release artifacts

Tags shaped as `v*` run `.github/workflows/release.yml`. GoReleaser produces
Linux amd64 and arm64 archives plus `checksums.txt`; each archive includes the
license, notices, installer, `lr`, systemd template, example configuration, and
the complete installable `localrouter-protocol-pack` Agent Skill.
The release installer accepts a prebuilt archive binary, so end users do not
need Go or Bun.
