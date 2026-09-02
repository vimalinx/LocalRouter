# Contributing

## Set up

Use Go 1.25.13 or newer plus Bun:

```bash
git clone https://github.com/vimalinx/LocalRouter.git
cd LocalRouter
bun install --cwd gateway/web-src --frozen-lockfile
./tests/verify.sh
```

## Project boundaries

- Keep the listener and administration plane on loopback.
- Never commit legacy `gateway/data`, XDG runtime state, `.ai`, logs, binaries, credentials, cookies,
  private pool locators, account identities, or private upstream addresses.
- Keep registration, CAPTCHA, human OAuth consent, payment, and anti-bot work
  outside the request path.
- Do not vendor reference gateway repositories into this tree. Keep protocol
  compatibility fixtures small, local, deterministic, and secret-free.

Protocol Pack changes must follow the project Skill in
`.agents/skills/localrouter-protocol-pack/SKILL.md`, including validation,
impact review, exact-digest apply, live verification, and rollback evidence.

## Verification

Run the complete local suite before proposing a change:

```bash
./tests/verify.sh
```

Real provider and paid tests remain opt-in and require explicit authorization.
They are not part of the default contribution gate.
