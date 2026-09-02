# Pool and quota lifecycle

Read this reference when a Pack selects credentials, consumes an externally maintained pool, exposes account health, or reports cost/quota.

## Assign each responsibility

Decide these separately: registration/replenishment, secret refresh, health measurement, request-time selection, affinity, quota measurement, and upstream OAuth.

| Shape | Pack configuration | Owner |
|---|---|---|
| One fixed protected credential | no `pool` | LocalRouter reads one `secret_file` |
| Upstream gateway owns accounts and rotation | `pool.mode=external` | upstream gateway |
| LocalRouter owns both stored credentials and selection | `pool.mode=local` + `credentials_file` | LocalRouter/operator |
| External maintainer writes a pool; LocalRouter selects requests | `pool.mode=local` + external-readonly `source` | maintainer for records, LocalRouter for selection |

Never copy a CPA/provider pool into LocalRouter merely to make a Pack self-contained. Registration, CAPTCHA, payment, human OAuth consent, and anti-bot work remain outside request handling.

## Protected source contract

Locators and resolved sources must be private regular files owned by the LocalRouter user. The installed locator lives below `$XDG_DATA_HOME/localrouter/`, is mode `0600`, contains `schema_version: "1"` and an absolute source path, and is never included in Pack source or guides.

Map only fields LocalRouter needs:

- identity and secret: `id_path`, `secret_path`, optional codec/selector;
- eligibility: `disabled_path`, `eligible_path`, `expires_at_path`;
- scheduling: `priority_path`, `weight_path`, `balance_path`, `metadata_paths`;
- quota: `quota_total_path`, `quota_remaining_path`, `quota_used_path`, `quota_unit_path`, `quota_status_path`, `quota_checked_at_path`.

The maintainer writes a temporary mode-600 file, flushes it, and atomically replaces the authoritative source. It must not mutate the file in place while LocalRouter is reading it.

## Normalized quota

Use this protected record shape when data exists:

```json
{
  "quota": {
    "total": 100,
    "remaining": 35,
    "used": 65,
    "unit": "credits",
    "status": "confirmed",
    "checked_at": "2026-08-30T12:00:00Z"
  }
}
```

Allowed meanings:

- `confirmed`: upstream supplied the value or the value follows exactly from supplied fields.
- `estimated`: a total/used value was derived from rounded percentage or another explicitly approximate source.
- `unknown`: no reliable measurement exists. Store this instead of zero.
- `stale`: the last known measurement was preserved after a failed refresh or exceeded `quota_stale_after_seconds`.
- `remaining-only`, `partial`, and `mixed-unit` are aggregate management states produced by LocalRouter.

Rules:

1. Accept only finite non-negative numbers.
2. Missing is unknown, not zero.
3. Derive `used = total - remaining` or `remaining = total - used` only when the source fields are reliable; never invent `total`.
4. Record `unit`; do not add unlike units.
5. Capture quota during successful registration when a read-only account/balance response is already available.
6. On refresh failure, preserve the previous value as stale. Do not erase historical remaining balance.
7. Quota and schedulability are independent. An account may have balance but fail login, entitlement, Nexus, or provider operation checks.
8. Prefer a cheap non-mutating provider endpoint. Do not consume paid quota merely to update a dashboard.

## Reference value from pricing

Authenticated pool views may expose `quota.reference_value` when quota telemetry and Pack pricing have one mathematically compatible rate. A pricing unit shaped as `per-<quantity>-<quota-unit>` (for example, `10 USD / per-1000-credits`) can value quota reported in `credits`; `k` and `M` quantity suffixes are supported. LocalRouter then derives the known `total`, `used`, and `remaining` monetary values independently.

Keep these boundaries explicit:

- a unit price never creates quota telemetry or invents a total;
- only an uppercase three-letter monetary currency and a matching denominator unit are eligible;
- different rates for the same quota unit produce `reference_value.status=ambiguous` and no amount;
- missing or incompatible rates omit `reference_value`;
- this is a reference value at the published rate, not an invoice, payment record, subscription tier, or provider balance;
- preserve `estimated`, `stale`, `partial`, and `remaining-only` provenance in the derived value.

For example, 20 total / 8 used / 12 remaining credits at `10 USD / per-1000-credits` yield 0.20 / 0.08 / 0.12 USD. If only 12 remaining credits are known, emit only the 0.12 USD remaining reference value.

## Pool behavior

- Selection runs only inside the best eligible priority tier.
- Exclude credentials already tried in the same request.
- Side-effecting retries still obey the route retry policy; a bigger pool does not make replay safe.
- Resource polling must return to the creating credential through affinity unless the provider contract explicitly permits failover.
- Expiring leases and inter-process locks must recover capacity after crashes.
- Reset cooldown/disable state only after diagnosing the cause. Reset does not repair a bad credential or upstream suspension.

## Verification

Report separately:

- source parsed and permissions protected;
- distinct credentials were selected/retried as expected;
- contention/crash leases recovered;
- health endpoint worked;
- quota was confirmed/estimated/unknown/stale;
- a real provider operation succeeded;
- cost was observed;
- no identity, secret, locator path, or private address appeared in public docs or logs.
