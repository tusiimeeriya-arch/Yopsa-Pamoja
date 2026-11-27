# Manual test scripts (UI-only items)

These items are visual or interaction-only. There is no browser test in the
repository, so each has an exact script below. Run them against the demo seed
(`docker compose -f deploy/docker-compose.app.yml up -d`, then sign in with a
demo account; the password is `PamojaDemo#2026`).

Record the result in `docs/VERIFICATION_REPORT.md` before claiming the item.

---

## P2-11 Registration screens (group list, detail tabs, member form, leadership, documents, review queue)

1. Sign in as `officer@yopsa.demo`. Open **Groups**.
2. Confirm the list shows only Kampala Central groups and that each row has a
   status chip.
3. Open **Tukolere Wamu VSLA**. Visit every tab: Overview, Members, Leadership,
   Meetings, Payments, Documents.
4. Press **Add member**. Submit with an empty name → a field error appears, no
   record is created. Fill name and phone and submit → the member appears in the
   Members tab.
5. Open **Documents**. Upload a `.txt` file → refused with a friendly message.
   Upload a PNG under 5 MB → accepted and listed.
6. Sign in as `manager@yopsa.demo`. Open **Admin → Review queue** (or Groups
   filtered to Submitted). The group submitted in P2-5 is listed.

Expected: every tab renders, invalid input is refused inline, the uploaded file
is listed, and the review queue shows the submitted group.

## P2-5 submitGroupForReview

1. As `officer@yopsa.demo`, open a **Draft** group in Groups.
2. Press **Submit for review**.
3. Expected: status becomes Submitted, the button is no longer offered, and the
   group appears in the review queue.

## P3-3 / P3-15 amendment reviewer differs from requester

1. As `secretary@yopsa.demo`, open a **closed** meeting and add an amendment
   (original value, new value, reason). Submit it.
2. Sign in as the same secretary and try to approve the amendment.
   Expected: refused.
3. Sign in as `chair@yopsa.demo` and approve the same amendment.
   Expected: approved, and the meeting shows the amended value with the reason.

## P3-11 Meeting screens (live register, minutes, actions, member attendance with dispute)

1. As `secretary@yopsa.demo`, open a scheduled meeting and press **Open**.
2. Check in one member from the register. Press **Confirm register**, then
   **Close**.
3. Open the **Minutes** tab, add minutes and one action item without an owner →
   refused; add it with an owner and deadline → saved.
4. Sign in as `member@yopsa.demo`, open **Meetings → my attendance**, and raise a
   dispute on an absent record. Expected: the dispute is created and the member
   sees only their own attendance.

## P4-5 resolveDifference modes

1. As `verifier@yopsa.demo`, open **Payments → the Tukolere Wamu submission**
   (200,000 submitted, 180,000 received).
2. Try to resolve by **missing receipt** with an amount that still leaves a
   difference. Expected: the submission stays in Difference and shows the exact
   remaining amount.
3. Try **partial verification** with no entries selected. Expected: refused with
   a message to select fully evidenced entries.

## P4-11 Member payment view chips

1. Sign in as `member@yopsa.demo` and open **Payments → My payments**.
2. Expected: only the member's own entries, each with a chip — Pending,
   Verified, Disputed, Posting pending or Returned. No other member's row is
   visible.

## P5-10 Repeat application obligations and affordability

1. As `chair@yopsa.demo`, open **Loans** for a group that already has a loan and
   start a new application.
2. Expected: the form shows the group's existing obligations and an
   affordability summary before submission; no numeric credit score is shown.

## P5-15 Loan screens (wizard, tracker, appraisal, review/approval queues)

1. As `chair@yopsa.demo`, run the application wizard end to end and submit.
2. As `officer@yopsa.demo`, open the appraisal screen and complete the appraisal.
3. As `approver@yopsa.demo`, open the approval queue and approve.
4. Expected: the status tracker moves Draft → GroupAuthorised → OfficerAppraisal
   → OperationsReview → Approved, and each queue shows the loan only at its own
   stage.

## P6-1 Dashboard filters

1. As `manager@yopsa.demo`, open **Dashboard**.
2. Set branch = Mbarara, then group type = VSLA, then period = last 90 days.
3. Expected: every card and the top-ten list change together, and the group
   count matches the number of groups shown by the same filters in **Groups**.

## P6-2 Card meta and drill-down

1. On **Dashboard**, confirm every card shows last refresh, cut-off and
   reconciliation status.
2. Press the **members** card. Expected: the drill-down lists distinct people,
   and the number of rows equals the card's people count.

## P6-3 CSV export

1. On **Dashboard**, press **Export CSV**.
2. Expected: a one-time code is requested. Enter it; the file downloads and a new
   row appears in **Admin → Audit** with action `report.export`.
3. Sign in as `auditor@yopsa.demo` and try the export. Expected: allowed (read
   only) and audited; the auditor cannot verify, approve or post anything.

## P6-10 Branch scope and auditor read-only

1. As `officer2@yopsa.demo` (Mbarara), open **Dashboard**. Expected: only
   Mbarara groups appear.
2. As `auditor@yopsa.demo`, open **Payments** and **Loans**. Expected: every
   action button (verify, approve, post, disburse) is absent or disabled.

## P6-16 Cards reconcile to drill-down and export

1. On **Dashboard**, note the submitted, verified and posted counts.
2. Open each drill-down and count the rows. Expected: the counts match the cards,
   submitted ≥ verified ≥ posted, and the exported CSV contains the same rows.
