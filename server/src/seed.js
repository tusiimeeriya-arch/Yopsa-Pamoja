import crypto from 'node:crypto';
import { pool, tx } from './db.js';
import { hashPassword } from './auth.js';
import { DEFAULTS } from './config.js';
import { addDays, today } from './util.js';

const BATCH = 'DEMO-2026-11';
const PASSWORD = process.env.DEMO_PASSWORD || 'PamojaDemo#2026';
const iso = (daysAgo) => addDays(today(), -daysAgo);
const ts = (daysAgo, hour = 10) => iso(daysAgo) + `T${String(hour).padStart(2, '0')}:00:00Z`;

function pick(seed, n) { return Math.abs(Math.sin(seed * 99991) * 10000) % n; }

async function main() {
  const existing = await pool.query('SELECT 1 FROM groups WHERE demo_batch=$1 LIMIT 1', [BATCH]);
  if (existing.rowCount) { console.log('demo data already present; skipping'); await pool.end(); return; }

  await tx(async (c) => {
    // ---- reference data ----
    await c.query(`INSERT INTO group_type_configs (type, label, categories, flags) VALUES
      ('VSLA','Village Savings and Loan Association','["savings","shares","social_fund","fines","subscriptions","loan_repayment"]','{"meetings":true,"shares":true}'),
      ('InvestmentClub','Investment Club','["savings","shares","social_fund","fines","subscriptions","loan_repayment"]','{"meetings":true,"shares":true}'),
      ('Association','Association','["savings","subscriptions","social_fund","fines","loan_repayment"]','{"meetings":true}'),
      ('Cooperative','Cooperative','["savings","shares","subscriptions","fines","loan_repayment"]','{"meetings":true,"shares":true}'),
      ('SACCO','Savings and Credit Cooperative','["savings","shares","subscriptions","fines","loan_repayment"]','{"meetings":true,"shares":true}')
      ON CONFLICT (type) DO NOTHING`);

    const branchKla = (await c.query("INSERT INTO org_branches (name, district, code, is_demo, demo_batch) VALUES ('Kampala Central','Kampala','KLA',$1,$2) RETURNING *", [true, BATCH])).rows[0];
    const branchMbr = (await c.query("INSERT INTO org_branches (name, district, code, is_demo, demo_batch) VALUES ('Mbarara','Mbarara','MBR',$1,$2) RETURNING *", [true, BATCH])).rows[0];

    const pwd = hashPassword(PASSWORD);
    const mkUser = async (email, name) => (await c.query(
      `INSERT INTO users (email, password_hash, full_name, is_demo) VALUES ($1,$2,$3,true)
       ON CONFLICT (email) DO UPDATE SET password_hash=EXCLUDED.password_hash, full_name=EXCLUDED.full_name, status='active', is_demo=true
       RETURNING *`, [email, pwd, name])).rows[0];
    const admin = await mkUser('admin@yopsa.demo', 'Amina Admin');
    const manager = await mkUser('manager@yopsa.demo', 'Moses Management');
    const auditor = await mkUser('auditor@yopsa.demo', 'Alice Auditor');
    const officer = await mkUser('officer@yopsa.demo', 'Okello Field Officer');
    const officer2 = await mkUser('officer2@yopsa.demo', 'Nabirye Field Officer');
    const verifier = await mkUser('verifier@yopsa.demo', 'Violet Verifier');
    const approver = await mkUser('approver@yopsa.demo', 'Peter Credit Approver');
    const approver2 = await mkUser('approver2@yopsa.demo', 'Grace Second Approver');
    const chair = await mkUser('chair@yopsa.demo', 'Christine Chairperson');
    const secretary = await mkUser('secretary@yopsa.demo', 'Samuel Secretary');
    const treasurer = await mkUser('treasurer@yopsa.demo', 'Teresa Treasurer');
    const member = await mkUser('member@yopsa.demo', 'Mary Member');
    const member2 = await mkUser('member2@yopsa.demo', 'John Member');

    const grant = async (userId, role, scopeType, scopeId) => c.query(
      "INSERT INTO role_assignments (user_id, role, scope_type, scope_id, status, granted_by, approved_by, is_demo) VALUES ($1,$2,$3,$4,'active',$5,$6,true)",
      [userId, role, scopeType, scopeId, admin.id, manager.id],
    );
    await grant(admin.id, 'it_admin', 'Org', 'org');
    await grant(manager.id, 'management', 'Org', 'org');
    await grant(auditor.id, 'auditor', 'Org', 'org');
    await grant(officer.id, 'field_officer', 'Branch', branchKla.id);
    await grant(officer2.id, 'field_officer', 'Branch', branchMbr.id);
    await grant(verifier.id, 'verifier', 'Org', 'org');
    await grant(approver.id, 'credit_approver', 'Org', 'org');
    await grant(approver2.id, 'credit_approver', 'Org', 'org');

    // policy values (proposed defaults, active for the demo)
    for (const [key, value] of Object.entries(DEFAULTS)) {
      await c.query("INSERT INTO policy_configs (key, value, version, effective_from, approved_by, status) VALUES ($1,$2,1,current_date,$3,'active') ON CONFLICT (key, version) DO UPDATE SET value=EXCLUDED.value, approved_by=EXCLUDED.approved_by, status='active'", [key, JSON.stringify(value), manager.id]);
    }
    const rankingPolicy = (await c.query(
      `INSERT INTO ranking_policy_versions (version, weights, min_loans, min_meetings, overdue_exclusion_days, tie_break, status, author_user_id, approved_by, effective_from)
       VALUES (1,$1,1,4,30,$2,'approved',$3,$4,current_date)
       ON CONFLICT (version) DO UPDATE SET weights=EXCLUDED.weights, min_loans=EXCLUDED.min_loans, min_meetings=EXCLUDED.min_meetings, overdue_exclusion_days=EXCLUDED.overdue_exclusion_days, tie_break=EXCLUDED.tie_break, status='approved', author_user_id=EXCLUDED.author_user_id, approved_by=EXCLUDED.approved_by
       RETURNING *`,
      [JSON.stringify(DEFAULTS['ranking.weights']), JSON.stringify(DEFAULTS['ranking.tie_break']), manager.id, admin.id],
    )).rows[0];

    const groupDefs = [
      { name: 'Tukolere Wamu VSLA', type: 'VSLA', branch: branchKla, status: 'Active' },
      { name: 'Nkumba Investment Club', type: 'InvestmentClub', branch: branchKla, status: 'Active' },
      { name: 'Mbarara Dairy Association', type: 'Association', branch: branchMbr, status: 'Active' },
      { name: 'Kigezi Growers Cooperative', type: 'Cooperative', branch: branchMbr, status: 'Active' },
      { name: 'Rwenzori SACCO', type: 'SACCO', branch: branchKla, status: 'Active' },
    ];
    const groups = [];
    for (const def of groupDefs) {
      const g = (await c.query(
        `INSERT INTO groups (name, type, branch_id, status, registration_evidence_status, created_by, submitted_at, reviewed_by, reviewed_at, review_decision, review_note, activated_at, is_demo, demo_batch)
         VALUES ($1,$2,$3,$4,'Verified',$5,$6,$7,$8,'approve','Demo group approved independently',$9,true,$10) RETURNING *`,
        [def.name, def.type, def.branch.id, def.status, officer.id, ts(120), officer.id, ts(118), ts(115), BATCH],
      )).rows[0];
      groups.push(g);
    }

    const firstNames = ['Mary', 'John', 'Grace', 'Peter', 'Esther', 'David', 'Sarah', 'James', 'Joyce', 'Daniel', 'Ruth', 'Simon', 'Betty', 'Paul', 'Agnes'];
    const lastNames = ['Nakato', 'Okello', 'Namono', 'Mugisha', 'Achieng', 'Ssemakula', 'Kirabo', 'Byaruhanga', 'Nabirye', 'Tumusiime', 'Atim', 'Kato'];
    const membersByGroup = {};
    const linked = new Map([[0, chair], [1, secretary], [2, treasurer], [3, member]]);
    for (let gi = 0; gi < groups.length; gi += 1) {
      const g = groups[gi];
      membersByGroup[g.id] = [];
      for (let i = 0; i < 12; i += 1) {
        const name = `${firstNames[(gi * 5 + i) % firstNames.length]} ${lastNames[(gi * 7 + i) % lastNames.length]}`;
        const user = gi === 0 ? linked.get(i) : (gi === 1 && i === 0 ? member2 : null);
        const p = (await c.query(
          `INSERT INTO persons (full_name, phone, national_id_ref, gender, date_of_birth, linked_user_id, consent_recorded, consent_at, created_by, is_demo, demo_batch)
           VALUES ($1,$2,$3,$4,$5,$6,true,$7,$8,true,$9) RETURNING *`,
          [name, '+2567' + String(10000000 + gi * 100000 + i * 37), `DEMO-${gi + 1}-${i + 1}`, i % 2 ? 'Male' : 'Female', iso(9000 + i * 40).slice(0, 4) + '-0' + ((i % 9) + 1) + '-1' + (i % 9), user?.id || null, ts(150), officer.id, BATCH],
        )).rows[0];
        const leave = gi === 0 && i === 10 ? iso(45) : null;
        const join = gi === 0 && i === 11 ? iso(20) : iso(150 + i);
        const m = (await c.query(
          "INSERT INTO memberships (person_id, group_id, join_date, leave_date, status, created_by, is_demo) VALUES ($1,$2,$3,$4,$5,$6,true) RETURNING *",
          [p.id, g.id, join, leave, leave ? 'ended' : 'active', officer.id],
        )).rows[0];
        membersByGroup[g.id].push({ person: p, membership: m, user });
      }
    }
    // leadership for group 1
    const g1 = groups[0];
    const g1m = membersByGroup[g1.id];
    const positions = [['chairperson', g1m[0]], ['secretary', g1m[1]], ['treasurer', g1m[2]]];
    for (const [position, m] of positions) {
      await c.query(
        `INSERT INTO leaderships (group_id, person_id, position, authority_scope, effective_from, evidence_file_ref, created_by, is_demo)
         VALUES ($1,$2,$3,'group',$4,NULL,$5,true)`,
        [g1.id, m.person.id, position, iso(140), officer.id],
      );
    }
    await grant(chair.id, 'chairperson', 'Group', g1.id);
    await grant(secretary.id, 'secretary', 'Group', g1.id);
    await grant(treasurer.id, 'treasurer', 'Group', g1.id);
    await grant(member.id, 'member', 'Group', g1.id);
    await grant(member2.id, 'member', 'Group', groups[1].id);

    // meetings + attendance
    const meetingIds = {};
    for (let gi = 0; gi < groups.length; gi += 1) {
      const g = groups[gi];
      meetingIds[g.id] = [];
      for (let i = 0; i < 12; i += 1) {
        const daysAgo = 84 - i * 7;
        const cancelled = gi === 0 && i === 4;
        const m = (await c.query(
          `INSERT INTO meetings (group_id, title, scheduled_for, status, location_text, opened_by, opened_at, register_confirmed_by, register_confirmed_at, closed_by, closed_at, cancel_reason, created_by, is_demo, demo_batch)
           VALUES ($1,$2,$3,$4,'Community hall',$5,$6,$5,$6,$5,$6,$7,$5,true,$8) RETURNING *`,
          [g.id, `Weekly meeting ${i + 1}`, ts(daysAgo), cancelled ? 'cancelled' : 'closed', gi === 0 ? secretary.id : officer.id,
            cancelled ? null : ts(daysAgo), cancelled ? 'Public holiday' : null, BATCH],
        )).rows[0];
        meetingIds[g.id].push(m);
        if (cancelled) continue;
        await c.query('INSERT INTO minutes (meeting_id, body, recorded_by) VALUES ($1,$2,$3)', [m.id, `Minutes of ${g.name} weekly meeting ${i + 1}. Members reviewed savings, loans and group business.`, gi === 0 ? secretary.id : officer.id]);
        const meetingDate = iso(daysAgo);
        const asDate = (v) => (v ? new Date(v).toISOString().slice(0, 10) : null);
        const active = membersByGroup[g.id].filter((mm) => asDate(mm.membership.join_date) <= meetingDate && (!mm.membership.leave_date || asDate(mm.membership.leave_date) >= meetingDate));
        for (let k = 0; k < active.length; k += 1) {
          const roll = pick(gi * 100 + i * 13 + k, 100);
          const status = roll < 78 ? 'present' : roll < 88 ? 'late' : roll < 94 ? 'excused' : 'absent';
          if (status === 'absent') continue;
          await c.query(
            `INSERT INTO attendance_records (meeting_id, person_id, status, method, recorded_by, checked_in_at)
             VALUES ($1,$2,$3,$4,$5,$6)`,
            [m.id, active[k].person.id, status, k % 3 === 0 ? 'assisted' : 'self', gi === 0 ? secretary.id : officer.id, ts(daysAgo)],
          );
        }
      }
      // future meeting
      await c.query(
        `INSERT INTO meetings (group_id, title, scheduled_for, status, location_text, created_by, is_demo, demo_batch)
         VALUES ($1,'Next weekly meeting',$2,'scheduled','Community hall',$3,true,$4)`,
        [g.id, ts(-7), gi === 0 ? secretary.id : officer.id, BATCH],
      );
    }
    // action item
    await c.query(
      `INSERT INTO action_items (meeting_id, group_id, title, owner_person_id, deadline, created_by)
       VALUES ($1,$2,'Collect outstanding savings from three members',$3,$4,$5)`,
      [meetingIds[g1.id][1].id, g1.id, g1m[1].person.id, addDays(today(), 3), secretary.id],
    );

    // payments: group 1 shortage, group 2 posting failure, group 3 posted
    const sub1 = (await c.query(
      `INSERT INTO group_submissions (group_id, period_start, period_end, totals, total_amount, submitted_by, status, difference, receipt_total, is_demo, demo_batch, submitted_at)
       VALUES ($1,$2,$3,$4,200000,$5,'difference',-20000,180000,true,$6,$7) RETURNING *`,
      [g1.id, iso(30), iso(14), JSON.stringify({ savings: 200000 }), treasurer.id, BATCH, ts(14)],
    )).rows[0];
    for (let i = 0; i < 10; i += 1) {
      await c.query(
        `INSERT INTO payment_entries (group_id, member_person_id, category, amount, collection_date, status, submission_id, created_by, is_demo, demo_batch)
         VALUES ($1,$2,'savings',20000,$3,'locked',$4,$5,true,$6)`,
        [g1.id, g1m[i].person.id, iso(14), sub1.id, treasurer.id, BATCH],
      );
    }
    await c.query(
      `INSERT INTO receipts (group_id, submission_id, amount, receipt_date, source_ref, destination, channel, evidence_ref, recorded_by, is_demo, demo_batch)
       VALUES ($1,$2,180000,$3,'DEMO-RCPT-001','Yopsa collection account','bank','Bank slip DEMO-001',$4,true,$5)`,
      [g1.id, sub1.id, iso(13), verifier.id, BATCH],
    );

    const g2 = groups[1];
    const g2m = membersByGroup[g2.id];
    const sub2 = (await c.query(
      `INSERT INTO group_submissions (group_id, period_start, period_end, totals, total_amount, submitted_by, status, verified_by, verified_at, posting_status, is_demo, demo_batch, submitted_at)
       VALUES ($1,$2,$3,$4,50000,$5,'posting_pending',$6,$7,'failed',true,$8,$9) RETURNING *`,
      [g2.id, iso(30), iso(14), JSON.stringify({ savings: 50000 }), officer.id, verifier.id, ts(12), BATCH, ts(14)],
    )).rows[0];
    for (let i = 0; i < 5; i += 1) {
      await c.query(
        `INSERT INTO payment_entries (group_id, member_person_id, category, amount, collection_date, status, submission_id, created_by, is_demo, demo_batch)
         VALUES ($1,$2,'savings',10000,$3,'verified',$4,$5,true,$6)`,
        [g2.id, g2m[i].person.id, iso(14), sub2.id, officer.id, BATCH],
      );
    }
    const rcp2 = (await c.query(
      `INSERT INTO receipts (group_id, submission_id, amount, receipt_date, source_ref, destination, channel, evidence_ref, recorded_by, is_demo, demo_batch)
       VALUES ($1,$2,50000,$3,'DEMO-RCPT-002','Yopsa collection account','bank','Bank slip DEMO-002',$4,true,$5) RETURNING *`,
      [g2.id, sub2.id, iso(13), verifier.id, BATCH],
    )).rows[0];
    await c.query(
      `INSERT INTO ledger_postings (submission_id, amount, status, error, created_by)
       VALUES ($1,50000,'failed','Ledger unavailable: posting rejected by the core ledger.',$2)`,
      [sub2.id, verifier.id],
    );
    await c.query(
      `INSERT INTO finance_exceptions (kind, entity, entity_id, detail, owner_user_id, deadline)
       VALUES ('posting_failure','GroupSubmission',$1,'Verified submission could not be posted to the ledger.',$2,$3)`,
      [sub2.id, verifier.id, addDays(today(), 1)],
    );

    const g3 = groups[2];
    const g3m = membersByGroup[g3.id];
    const sub3 = (await c.query(
      `INSERT INTO group_submissions (group_id, period_start, period_end, totals, total_amount, submitted_by, status, verified_by, verified_at, posting_status, is_demo, demo_batch, submitted_at)
       VALUES ($1,$2,$3,$4,20000,$5,'posted',$6,$7,'posted',true,$8,$9) RETURNING *`,
      [g3.id, iso(40), iso(20), JSON.stringify({ savings: 20000 }), officer2.id, verifier.id, ts(18), BATCH, ts(20)],
    )).rows[0];
    for (let i = 0; i < 4; i += 1) {
      await c.query(
        `INSERT INTO payment_entries (group_id, member_person_id, category, amount, collection_date, status, submission_id, created_by, is_demo, demo_batch)
         VALUES ($1,$2,'savings',5000,$3,'posted',$4,$5,true,$6)`,
        [g3.id, g3m[i].person.id, iso(20), sub3.id, officer2.id, BATCH],
      );
    }
    await c.query(
      `INSERT INTO receipts (group_id, submission_id, amount, receipt_date, source_ref, destination, channel, evidence_ref, recorded_by, is_demo, demo_batch)
       VALUES ($1,$2,20000,$3,'DEMO-RCPT-003','Yopsa collection account','bank','Bank slip DEMO-003',$4,true,$5)`,
      [g3.id, sub3.id, iso(19), verifier.id, BATCH],
    );
    await c.query("INSERT INTO ledger_postings (submission_id, amount, status, ledger_ref, created_by, posted_at) VALUES ($1,20000,'posted','MOCK-DEMO003',$2,now())", [sub3.id, verifier.id]);

    // loans at every stage
    const mkLoan = async (group, status, amount, extra = {}) => {
      const l = (await c.query(
        `INSERT INTO loan_applications (group_id, product, amount, purpose, term_months, repayment_source, existing_debts, cash_flow, member_allocation, security, guarantees, status, created_by, authorised_person_id, resolution_ref, quorum_evidence_ref, approved_by, approved_at, offered_at, accepted_at, disbursed_at, is_demo, demo_batch)
         VALUES ($1,'group_loan',$2,$3,12,'Weekly member savings and business sales',$4,'Stable weekly cash flow','Equal shares','Group savings and guarantors','Group guarantee and member savings',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,true,$15) RETURNING *`,
        [group.id, amount, extra.purpose || 'Working capital for member businesses', Number(extra.existingDebts || 0), status, extra.createdBy || chair.id, extra.rep || membersByGroup[group.id][0].person.id, extra.resolution || crypto.randomUUID(), extra.quorum || crypto.randomUUID(), extra.approvedBy || null, extra.approvedAt || null, extra.offeredAt || null, extra.acceptedAt || null, extra.disbursedAt || null, BATCH],
      )).rows[0];
      return l;
    };
    const l1 = await mkLoan(g1, 'Draft', 600000, { purpose: 'Stock for member businesses (no guarantor yet)' });
    await mkLoan(g1, 'GroupAuthorised', 700000);
    await mkLoan(g1, 'OfficerAppraisal', 800000);
    await mkLoan(g1, 'OperationsReview', 900000);
    const lApproved = await mkLoan(g1, 'Approved', 600000, { approvedBy: approver.id, approvedAt: ts(10) });
    await c.query('INSERT INTO guarantors (loan_id, person_id, amount, evidence_ref) VALUES ($1,$2,600000,$3)', [lApproved.id, g1m[5].person.id, 'Guarantee form DEMO']);
    await mkLoan(g1, 'OfferIssued', 650000, { approvedBy: approver.id, approvedAt: ts(8), offeredAt: ts(7) });
    await mkLoan(g1, 'GroupAccepted', 700000, { approvedBy: approver.id, approvedAt: ts(6), offeredAt: ts(5), acceptedAt: ts(4) });
    const lDisb = await mkLoan(g1, 'Disbursed', 400000, { approvedBy: approver.id, approvedAt: ts(40), offeredAt: ts(38), acceptedAt: ts(36), disbursedAt: ts(35) });
    await c.query("INSERT INTO approval_steps (loan_id, step, actor_user_id, decision, reason) VALUES ($1,'operations_review',$2,'reviewed','Operations reviewed affordability')", [lDisb.id, approver.id]);
    await c.query("INSERT INTO disbursements (loan_id, amount, disbursed_on, confirmed_by, ledger_ref, is_demo) VALUES ($1,400000,$2,$3,'MOCK-DEMO-L1',true)", [lDisb.id, iso(35), verifier.id]);
    await mkLoan(groups[1], 'Declined', 2500000, { approvedBy: approver.id, approvedAt: ts(20) });
    await mkLoan(groups[2], 'Returned', 5000000);
    await mkLoan(groups[3], 'Withdrawn', 10000000);
    const l5 = await mkLoan(groups[4], 'Disbursed', 12000000, { approvedBy: approver.id, approvedAt: ts(60), offeredAt: ts(58), acceptedAt: ts(56), disbursedAt: ts(55) });
    await c.query("INSERT INTO approval_steps (loan_id, step, actor_user_id, decision, reason) VALUES ($1,'operations_review',$2,'reviewed','Operations reviewed')", [l5.id, approver.id]);
    await c.query("INSERT INTO disbursements (loan_id, amount, disbursed_on, confirmed_by, ledger_ref, is_demo) VALUES ($1,12000000,$2,$3,'MOCK-DEMO-L5',true)", [l5.id, iso(55), verifier.id]);
    const l2 = await mkLoan(groups[1], 'Disbursed', 2000000, { approvedBy: approver.id, approvedAt: ts(70), offeredAt: ts(68), acceptedAt: ts(66), disbursedAt: ts(65) });
    await c.query("INSERT INTO approval_steps (loan_id, step, actor_user_id, decision, reason) VALUES ($1,'operations_review',$2,'reviewed','Operations reviewed')", [l2.id, approver.id]);
    await c.query("INSERT INTO disbursements (loan_id, amount, disbursed_on, confirmed_by, ledger_ref, is_demo) VALUES ($1,2000000,$2,$3,'MOCK-DEMO-L2',true)", [l2.id, iso(65), verifier.id]);
    const l3 = await mkLoan(groups[2], 'Disbursed', 5000000, { approvedBy: approver.id, approvedAt: ts(50), offeredAt: ts(48), acceptedAt: ts(46), disbursedAt: ts(45) });
    await c.query("INSERT INTO approval_steps (loan_id, step, actor_user_id, decision, reason) VALUES ($1,'operations_review',$2,'reviewed','Operations reviewed')", [l3.id, approver.id]);
    await c.query("INSERT INTO disbursements (loan_id, amount, disbursed_on, confirmed_by, ledger_ref, is_demo) VALUES ($1,5000000,$2,$3,'MOCK-DEMO-L3',true)", [l3.id, iso(45), verifier.id]);

    // mock ledger with arrears: group1 31 days overdue (excluded), group2 exactly 30 days
    const scheduleFor = (overdueDays, amount) => [
      { due_date: iso(overdueDays), amount, paid_date: null },
      { due_date: iso(20), amount, paid_date: null },
      { due_date: iso(60), amount, paid_date: iso(58) },
      { due_date: iso(30), amount, paid_date: iso(30) },
    ];
    await c.query("INSERT INTO mock_ledger_accounts (group_id, principal, interest_due, fees_due, schedule) VALUES ($1,400000,60000,10000,$2)", [g1.id, JSON.stringify(scheduleFor(31, 70000))]);
    await c.query("INSERT INTO mock_ledger_accounts (group_id, principal, interest_due, fees_due, schedule) VALUES ($1,2000000,240000,20000,$2)", [g2.id, JSON.stringify(scheduleFor(30, 200000))]);
    await c.query("INSERT INTO mock_ledger_accounts (group_id, principal, interest_due, fees_due, schedule) VALUES ($1,5000000,500000,30000,$2)", [g3.id, JSON.stringify([{ due_date: iso(40), amount: 400000, paid_date: iso(39) }, { due_date: iso(10), amount: 400000, paid_date: iso(9) }])]);
    await c.query("INSERT INTO mock_ledger_accounts (group_id, principal, interest_due, fees_due, schedule) VALUES ($1,12000000,1200000,50000,$2)", [groups[4].id, JSON.stringify([{ due_date: iso(40), amount: 900000, paid_date: iso(40) }])]);

    // learning
    const learning = [
      ['Budgeting for a small business', 'business_cash_flow', 'text', 'Keep business money separate, list weekly costs, and plan for slow weeks.'],
      ['Understanding a group loan', 'borrowing_responsibilities', 'pdf', 'What a group loan means, who is liable, and how repayment works.'],
      ['Recordkeeping basics', 'recordkeeping', 'text', 'Write every contribution on the day it is received and reconcile receipts.'],
      ['Fraud prevention in groups', 'fraud_prevention', 'video', 'Two-person checks, receipts and open reporting protect group money.'],
      ['Saving and shares', 'financial_literacy', 'text', 'The difference between savings and shares, and why both are recorded separately.'],
      ['Running a good meeting', 'group_governance', 'text', 'Agenda, attendance, minutes, resolutions and follow-up actions.'],
    ];
    for (const [title, category, kind, body] of learning) {
      await c.query(
        "INSERT INTO learning_items (title, category, language, version, content_owner, review_date, kind, url, body, is_demo) VALUES ($1,$2,'English',1,'Yopsa Training',$3,$4,NULL,$5,true)",
        [title, category, addDays(today(), 180), kind, body],
      );
    }
    const firstLearning = (await c.query('SELECT id FROM learning_items ORDER BY title LIMIT 1')).rows[0];
    await c.query("INSERT INTO learning_progress (item_id, person_id, status, opened_at, completed_at) VALUES ($1,$2,'completed',$3,$3)", [firstLearning.id, g1m[3].person.id, ts(10)]);

    // comments, disputes, tasks
    await c.query("INSERT INTO comments (group_id, body, visibility, author_user_id) VALUES ($1,'Reminder: bring your savings book to the next meeting.','group',$2)", [g1.id, secretary.id]);
    const attendanceForDispute = (await c.query("SELECT a.* FROM attendance_records a JOIN meetings m ON m.id=a.meeting_id WHERE m.group_id=$1 AND a.person_id=$2 AND a.status='absent' LIMIT 1", [g1.id, g1m[3].person.id])).rows[0]
      || (await c.query("SELECT a.* FROM attendance_records a JOIN meetings m ON m.id=a.meeting_id WHERE m.group_id=$1 LIMIT 1", [g1.id])).rows[0];
    if (attendanceForDispute) {
      await c.query(
        `INSERT INTO disputes (kind, entity, entity_id, group_id, person_id, raised_by_user_id, reason, original_value, assigned_to_user_id, is_demo)
         VALUES ('attendance','AttendanceRecord',$1,$2,$3,$4,'I attended this meeting but the register shows me absent. Please review.','absent',$5,true)`,
        [attendanceForDispute.id, g1.id, g1m[3].person.id, member.id, officer.id],
      );
    }
    const myEntry = (await c.query("SELECT * FROM payment_entries WHERE member_person_id=$1 LIMIT 1", [g1m[3].person.id])).rows[0];
    if (myEntry) {
      await c.query(
        `INSERT INTO disputes (kind, entity, entity_id, group_id, person_id, raised_by_user_id, reason, original_value, assigned_to_user_id, is_demo)
         VALUES ('payment','PaymentEntry',$1,$2,$3,$4,'I paid 20,000 but I want the receipt confirmed on this entry.','20000',$5,true)`,
        [myEntry.id, g1.id, g1m[3].person.id, member.id, verifier.id],
      );
    }
    await c.query("INSERT INTO tasks (title, owner_user_id, deadline, linked_entity, linked_id, status, is_demo) VALUES ('Follow up the 20,000 savings difference',$1,$2,'GroupSubmission',$3,'open',true)", [verifier.id, addDays(today(), 2), sub1.id]);
    await c.query("INSERT INTO tasks (title, owner_user_id, deadline, linked_entity, linked_id, status, is_demo) VALUES ('Review the posting exception for Nkumba Investment Club',$1,$2,'GroupSubmission',$3,'open',true)", [verifier.id, addDays(today(), 1), sub2.id]);
    await c.query("INSERT INTO notifications (user_id, title, body, deadline, dedupe_key) VALUES ($1,'Demo data loaded','Explore the demo guide under Admin. This banner disappears when demo data is purged.',$2,'demo-welcome')", [admin.id, addDays(today(), 7)]);

    console.log('demo seed complete');
  });
  await pool.end();
}

main().catch(async (err) => { console.error(err); await pool.end(); process.exit(1); });
