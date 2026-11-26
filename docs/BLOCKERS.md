# Blockers

Ordered by severity. Each entry states what is blocked, why, what the owner must
do, and the exact command to run later. A blocker never weakens a control.

## B-01 — Requested branch name `build/overnight` is not checked out

- **Severity:** low (process, not product).
- **What:** The brief asks for a branch named `build/overnight`. The Base44
  sandbox owns branch creation/switching and commits/pushes, and this task was
  started on its prepared branch `overnight-autonomous-mode`. Switching branches
  from inside the session would end the turn and could strand the build.
- **Why:** Platform rule: the agent never switches branches or pushes.
- **What you must do:** After reviewing, rename or merge the branch:
  ```sh
  git branch -m overnight-autonomous-mode build/overnight   # local
  git push origin build/overnight                            # if desired
  ```
- **Status:** NOT VERIFIED HERE for the exact name; all work is on
  `overnight-autonomous-mode`.

## B-02 — Flutter client cannot be compiled or tested here

- **Severity:** medium (client journeys not executable in this sandbox).
- **What:** P3–P8 include client screens and client tests (offline SQLCipher,
  accessibility). The sandbox has no Flutter SDK and installing it needs a large
  download; the platform also requires app code to run from the compose stack.
- **Why:** Environment limitation.
- **What you must do:** On a workstation with Flutter 3.47+:
  ```sh
  cd app && flutter pub get && flutter analyze && flutter test
  ```
  The API journeys for the same features are implemented and tested here; the
  client remains a shell for the new screens.
- **Status:** NOT VERIFIED HERE.

## B-03 — Live Alogsys ledger and real SMS/WhatsApp provider are unavailable

- **Severity:** medium (integration surface).
- **What:** The brief allows a sandbox ledger when the vendor is unavailable.
  The build ships a fully working PostgreSQL-backed `LedgerAdapter` sandbox and
  a config-driven Alogsys skeleton. Real SMS delivery is a stub adapter.
- **Why:** No vendor credentials or endpoints; no accounts may be created.
- **What you must do:** Obtain endpoints/credentials from Yopsa and set
  `LEDGER_ADAPTER=alogsys`, `SMS_PROVIDER=<provider>` plus the provider secrets.
  The exact fields Alogsys must supply are listed in `docs/ASSUMPTIONS.md`
  (A-023).
- **Status:** NOT VERIFIED HERE for the live connector; sandbox adapter is tested.

## B-04 — iOS build, store submission and production deployment

- **Severity:** low for this build.
- **What:** iOS needs macOS/Xcode; store submission needs owner accounts and
  policy decisions (`docs/STORE_POLICY_REGISTER.md`); production deployment is
  out of scope and no production system may be touched.
- **Status:** NOT VERIFIED HERE by design.
