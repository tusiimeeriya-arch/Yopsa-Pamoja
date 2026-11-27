import { q } from './db.js';

const DEFAULTS = {
  'attendance.late_counts_as_present': true,
  'attendance.excused_counts_as_present': false,
  'auth.idle_minutes': 30,
  'auth.step_up_minutes': 5,
  'auth.lockout_attempts': 5,
  'auth.lockout_minutes': 15,
  'loans.guarantor_threshold_ugx': 500000,
  'loans.product_limits': {
    VSLA: { min: 100000, max: 10000000, term: 12 },
    InvestmentClub: { min: 2000000, max: 20000000, term: 18 },
    Association: { min: 5000000, max: 50000000, term: 24 },
    Cooperative: { min: 10000000, max: 100000000, term: 24 },
    SACCO: { min: 10000000, max: 100000000, term: 24 },
  },
  'loans.approval_authority': { credit_approver_max: 5000000, committee_max: 20000000 },
  'ranking.window_days': 90,
  'ranking.min_loans': 1,
  'ranking.min_meetings': 4,
  'ranking.weights': { repayment: 50, arrears: 25, attendance: 15, records: 10 },
  'ranking.overdue_exclusion_days': 30,
  'ranking.tie_break': ['attendance_desc', 'verified_value_desc', 'group_id_asc'],
  'payments.partial_verification': true,
  'photos.enabled': false,
  'meetings.quorum_evidence_required': true,
};

let cache = { at: 0, values: {} };

export async function policy(key) {
  if (Date.now() - cache.at > 30000) {
    const rows = (await q("SELECT DISTINCT ON (key) key, value FROM policy_configs WHERE status='active' ORDER BY key, version DESC")).rows;
    const values = { ...DEFAULTS };
    for (const r of rows) values[r.key] = r.value;
    cache = { at: Date.now(), values };
  }
  return cache.values[key] ?? DEFAULTS[key];
}

export function invalidatePolicy() { cache = { at: 0, values: {} }; }
export { DEFAULTS };
