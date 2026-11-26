# Flutter client

Targets: **Android** (primary, low-cost devices), **iOS**, **Windows desktop**
(back office).

**Status:** P0 shell. Theme tokens, routing, the status home screen and the test
harness exist. The client has **not been compiled or tested in this
environment** — no Flutter SDK is installed here — so `flutter analyze` and
`flutter test` are reported as *not verified here* until CI runs them.

## Structure (feature-first)

```
lib/
  main.dart
  src/
    app.dart            MaterialApp.router + go_router
    theme/              Yopsa blue/green tokens, typography, tap targets
    features/
      home/             P0 status shell; replaced by role-based homes in P2
      auth/             (P2) sign-in, MFA, device sessions
      groups/           (P2) registration, roster, authority
      meetings/         (P3) register, minutes, action items
      payments/         (P4) entries, submissions, receipts
      loans/            (P5) applications, offers, disbursements
      dashboard/        (P6) cards and drill-downs
      learning/         (P7) lessons, progress
      disputes/         (P7) raise and track a dispute
```

## Rules

- The client is untrusted: every rule is enforced by the API. The UI only
  reflects server decisions and shows the status chips the API returns.
- Offline drafts live in an encrypted drift/SQLCipher database; the key is in
  `flutter_secure_storage`. Verification, approval, disbursement and posting are
  never offered offline.
- Release builds are obfuscated (`--obfuscate --split-debug-info`), contain no
  secrets, and pin certificates with a documented rotation procedure.

## Run locally

```sh
flutter pub get
flutter run -d <device-id>
```

`flutter devices` lists attached Android emulators, iOS simulators and the
Windows desktop target.
