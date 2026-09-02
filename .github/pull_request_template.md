## Scope

Describe the smallest public behavior changed and its ownership boundary.

## Verification

- [ ] Go tests and vet
- [ ] Web typecheck/tests/build when UI changed
- [ ] Relevant real listener or transport acceptance
- [ ] `./tests/verify.sh`
- [ ] No credentials, private paths, pool contents or generated runtime state

## Protocol Pack lifecycle

If a Pack changed, include reviewed impact, exact planned digest, live verification
and rollback revision. Otherwise write “not applicable”.
