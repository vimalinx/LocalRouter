---
id: quickstart
title: Universal matrix Agent guide
summary: Deterministic instructions for model discovery and exact operation invocation.
status: observed
last_verified: 2026-09-02
operations:
  - models
  - chat.completions
visibility: local
---
# Exact discovery and invocation

Use `lr find model matrix-text-v1`, choose the provider-qualified result, then inspect `lr describe modelauth chat.completions`. The semantic operation ID is not a URL. Invoke it with `lr call modelauth chat.completions` so LocalRouter uses the published `call_url`.

The request example documents shape only. Select an exact model returned by the live `models` operation before calling.
