export function assumptions() {
  return {
    product: {
      name: 'Yopsa Pamoja',
      org: 'Yopsa Microfinance',
      tagline: 'Stronger groups. Informed decisions.',
      scope: 'Group management and financial monitoring only. Pamoja complements the Yopsa core ledger and never replaces it.',
    },
    assumptions: [
      { id: 'A-01', title: 'One organisation, branches by district', value: 'Groups belong to exactly one branch at a time; branch history is kept on the record.', status: 'Awaiting management approval' },
      { id: 'A-02', title: 'Currency and time', value: 'UGX whole integers. Timestamps stored UTC and displayed Africa/Kampala.', status: 'Fixed by design' },
      { id: 'A-03', title: 'Lateness policy', value: 'Late counts as attended; excused is excluded from expected. Configurable and versioned.', status: 'Awaiting management approval' },
      { id: 'A-04', title: 'Ranking weights', value: 'On-time repayment 50, arrears control 25, attendance 15, complete records 10; 90-day window; min 1 loan and 4 meetings.', status: 'Awaiting management approval (demo policy version 1 is approved for demonstration)' },
      { id: 'A-05', title: 'Guarantor threshold', value: 'UGX 500,000. A guarantor covering the loan is required above this amount.', status: 'Awaiting management approval' },
      { id: 'A-06', title: 'Product limits', value: 'VSLA 100,000–10,000,000 (12 months); Investment Club 2,000,000–20,000,000 (18); Association 5,000,000–50,000,000 (24); Cooperative and SACCO 10,000,000–100,000,000 (24).', status: 'Placeholder pending signed product policy' },
      { id: 'A-07', title: 'Approval authority', value: 'Credit approver up to UGX 5,000,000; committee up to 20,000,000; two approvers above 20,000,000.', status: 'Awaiting management approval' },
      { id: 'A-08', title: 'Photo policy', value: 'Meeting photos are disabled by default. A photo never proves attendance.', status: 'Awaiting management and DPO approval' },
      { id: 'A-09', title: 'Retention', value: 'Financial and audit records 7 years; disputes 3 years after resolution; learning progress 2 years; sessions 90 days. Legal review required.', status: 'Awaiting legal review' },
      { id: 'A-10', title: 'Authentication', value: 'Email and password with lockout after 5 failures. Sensitive actions need password re-confirmation within 5 minutes. No native SMS/email one-time code provider is connected, so re-authentication is the fallback second factor.', status: 'Platform limitation documented' },
      { id: 'A-11', title: 'Database row-level security', value: 'All reads are scoped in the API authorisation layer; the database has no browser path and the API is the only client. PostgreSQL policies exist on the pre-existing identity tables only.', status: 'Documented limitation; API-level scoping is enforced' },
      { id: 'A-12', title: 'Offline drafts', value: 'Drafts are stored in this browser only, with a client id and idempotency key. Verification, approval, disbursement, posting, closing and register confirmation always require the server.', status: 'Design decision' },
      { id: 'A-13', title: 'Demo data', value: 'All demo records carry isDemo and demoBatch DEMO-2026-11. Demo accounts must be disabled before go-live.', status: 'Demo only' },
      { id: 'A-14', title: 'Internal member loans', value: 'The MVP records group-to-member obligations in the group records; member-to-member lending remains a separate workflow and is never counted as a Yopsa loan.', status: 'Awaiting operations confirmation' },
    ],
    approvals: [
      { value: 'Lateness and excused-absence policy', owner: 'Management' },
      { value: 'Ranking weights and eligibility thresholds', owner: 'Management' },
      { value: 'Loan product limits and approval authority', owner: 'Management / Credit' },
      { value: 'Guarantor threshold', owner: 'Management' },
      { value: 'Photo and retention policy', owner: 'Management + DPO / Legal' },
      { value: 'Demo account removal before go-live', owner: 'IT Admin' },
    ],
  };
}
