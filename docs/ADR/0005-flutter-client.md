# ADR-0005: Flutter client with Riverpod, go_router and drift/SQLCipher

**Status:** Accepted (P0)
**Date:** 2026-11-26

## Context

The client must run on low-cost Android phones, on iOS and on Windows desktop,
work on weak networks, keep offline drafts encrypted, and never be trusted by
the server. One codebase for three platforms is a hard requirement.

## Decision

- **Flutter (stable)** with Dart, feature-first folder structure.
- **Riverpod** for state, **go_router** for navigation, repository pattern
  between UI and data sources.
- **freezed + json_serializable** for immutable models and JSON.
- **dio** for HTTP with certificate pinning, idempotency keys and retry with
  backoff.
- **flutter_secure_storage** for tokens (Keychain / Keystore / DPAPI).
- **drift + SQLCipher** for the encrypted offline database, key in secure
  storage.
- **intl + ARB** for English first, structured for Luganda, Rutooro and Swahili.
- Responsive layouts: phone-first member journeys, master-detail and
  table-heavy views for desktop back office.

## Consequences

**Positive**

- One UI codebase for Android, iOS and Windows; the back office runs on the
  office desktops where it is needed.
- Encrypted offline drafts with a real schema and migrations, not ad-hoc JSON.
- Strong typing end to end reduces runtime surprises on cheap devices.

**Negative**

- Flutter desktop binaries are large; the Windows installer must be signed
  (MSIX).
- Certificate pinning requires a documented rotation procedure or the app bricks
  on certificate change.
- Dart tooling is not available in this sandbox, so client code is written but
  **not compiled here** until a Flutter toolchain runs in CI.

## Alternatives considered

- **Native Android + Windows (Kotlin/C#)** — best platform fit, two codebases
  and two teams; rejected for a small team.
- **React Native** — weaker desktop story and a heavier JS runtime.
- **Flutter web for the back office** — rejected: desktop app gives better
  keyboard/table behaviour and offline storage.

## References

- [ARCHITECTURE.md §9](../ARCHITECTURE.md)
- [THREAT_MODEL.md §7](../THREAT_MODEL.md)
