# ADR-0007: Application-level envelope encryption behind a KeyProvider

**Status:** Accepted (P0)
**Date:** 2026-11-26

## Context

Some fields are high-sensitivity even inside an access-controlled database:
national ID references, phone numbers, TOTP secrets and evidence object keys. A
stolen database dump or backup must not expose them. Full-disk encryption is a
deployment requirement but does not protect against SQL access or a leaked dump.

## Decision

- Envelope encryption: each sensitive value is encrypted with a **data
  encryption key (DEK)**; DEKs are wrapped by a **key encryption key (KEK)** held
  by a `KeyProvider`.
- `KeyProvider` interface with two implementations:
  - **local KMS-style** (file-based master key, mode 0400, owned by the service
    user, never in the repository) for the single-host deployment;
  - **cloud KMS** (documented option) for hosted deployments.
- Ciphertext is stored with a version prefix and key id: `v1:<keyid>:<nonce>:<ct>`.
- Key rotation re-wraps DEKs; a background job re-encrypts values lazily. The
  procedure is documented and tested (rotate, verify old and new values read
  correctly, retire the old key).
- Lookup of encrypted fields uses a separate **keyed hash** column (for example
  `phone_hash`) so search and duplicate detection never require decryption.
- Logs, metrics and error messages never contain plaintext or ciphertext of
  sensitive fields.

## Consequences

**Positive**

- A database dump alone does not disclose national IDs or phone numbers.
- Rotation is possible without a flag day.
- The interface keeps a cloud KMS migration cheap.

**Negative**

- Encrypted columns cannot be indexed for range queries or `LIKE`; lookups use
  the keyed hash.
- Key custody becomes a critical operational procedure (documented in the
  runbook, tested in P9).
- Slightly more code on every read/write of a sensitive field.

## Alternatives considered

- **pgcrypto with a database-held key** — the key would live with the data,
  defeating the purpose.
- **Column encryption via ORM hooks** — rejected: implicit, hard to audit.
- **No field encryption** — rejected by the brief.

## References

- [ARCHITECTURE.md §5, §7](../ARCHITECTURE.md)
- [ASSUMPTIONS.md A-006](../ASSUMPTIONS.md)
