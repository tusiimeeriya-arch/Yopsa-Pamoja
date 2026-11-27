import React, { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { get, patch, post } from '../api.js';
import { useAuth } from '../auth.jsx';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, Summary, toast } from '../components.jsx';
import { date, dateTime, money, pct } from '../fmt.js';

const FLOW = ['Draft', 'GroupAuthorised', 'OfficerAppraisal', 'OperationsReview', 'Approved', 'OfferIssued', 'GroupAccepted', 'Disbursed'];
const LABEL = { GroupAuthorised: 'Group authorised', OfficerAppraisal: 'Officer appraisal', OperationsReview: 'Operations review', OfferIssued: 'Offer issued', GroupAccepted: 'Group accepted' };

export default function LoanDetail() {
  const { id } = useParams();
  const { runSensitive } = useAuth();
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);
  const [modal, setModal] = useState(null);
  const [reason, setReason] = useState('');
  const [disb, setDisb] = useState({ amount: '', disbursedOn: new Date().toISOString().slice(0, 10) });
  const [change, setChange] = useState({ amount: '', termMonths: '', purpose: '', reason: '' });
  const online = navigator.onLine;

  const load = () => get(`/loans/${id}`).then(setData).catch(setError);
  useEffect(() => { load(); }, [id]);

  const run = async (fn, msg) => {
    setBusy(true);
    try { await fn(); toast(msg); setModal(null); setReason(''); await load(); }
    catch (err) { toast(err.message, 'error'); }
    finally { setBusy(false); }
  };
  const transition = (action) => run(() => runSensitive(() => post(`/loans/${id}/transition`, { action, reason })), 'Status updated and audited.');
  const disburse = (e) => { e.preventDefault(); run(() => runSensitive(() => post(`/loans/${id}/disburse`, { amount: Number(disb.amount), disbursedOn: disb.disbursedOn, idempotencyKey: crypto.randomUUID() })), 'Disbursement recorded by Finance.'); };
  const material = (e) => { e.preventDefault(); run(() => patch(`/loans/${id}`, { amount: change.amount ? Number(change.amount) : undefined, termMonths: change.termMonths ? Number(change.termMonths) : undefined, purpose: change.purpose || undefined, reason: change.reason }), 'Material change recorded.'); };

  if (!data && !error) return <Loading rows={4} />;
  if (error) return <ErrorBox error={error} />;
  const { loan, group, steps, guarantors, disbursements, profile, ledger, permissions } = data;
  const stepIndex = FLOW.indexOf(loan.status);
  return (
    <div>
      <PageHead title={`${group.name} · ${money(loan.amount)}`} sub={`${loan.product.replace(/_/g, ' ')} · ${loan.term_months} months · ${group.type}`} actions={<><StatusChip status={loan.status} /><Link className="btn btn-secondary btn-sm" to="/loans">Back</Link></>} />

      <Card title="Status tracker" sub="Every transition is made by a backend function and audited with the actor and reason.">
        <div className="row" style={{ gap: 6, overflowX: 'auto', paddingBottom: 6 }}>
          {FLOW.map((s, i) => <React.Fragment key={s}><span className={`chip ${i <= stepIndex ? 'verified' : 'draft'}`}>{LABEL[s] || s}</span>{i < FLOW.length - 1 && <span className="muted">→</span>}</React.Fragment>)}
          {['Declined', 'Withdrawn', 'Returned'].includes(loan.status) && <span className="chip disputed">{loan.status}</span>}
        </div>
        <div className="row mt-2">
          {loan.status === 'Draft' && permissions.isChairperson && <button className="btn" disabled={busy} onClick={() => transition('submit')}>Submit for appraisal</button>}
          {loan.status === 'GroupAuthorised' && <button className="btn" disabled={busy} onClick={() => setModal('appraise')}>Officer appraisal</button>}
          {loan.status === 'OfficerAppraisal' && permissions.isCreditApprover && <button className="btn" disabled={busy} onClick={() => transition('operations_review')}>Complete Operations review</button>}
          {loan.status === 'OperationsReview' && permissions.isCreditApprover && <><button className="btn btn-green" disabled={busy || !online} onClick={() => setModal('approve')}>Approve</button><button className="btn btn-danger" disabled={busy || !online} onClick={() => setModal('decline')}>Decline</button></>}
          {loan.status === 'Approved' && permissions.isCreditApprover && <button className="btn" disabled={busy} onClick={() => transition('offer')}>Issue offer</button>}
          {loan.status === 'OfferIssued' && permissions.isChairperson && <button className="btn btn-green" disabled={busy} onClick={() => transition('accept')}>Group accepts offer</button>}
          {loan.status === 'GroupAccepted' && permissions.isVerifier && <button className="btn btn-green" disabled={!online} onClick={() => { setDisb({ amount: String(loan.amount), disbursedOn: new Date().toISOString().slice(0, 10) }); setModal('disburse'); }}>Record disbursement</button>}
          {!['Disbursed', 'Declined', 'Withdrawn'].includes(loan.status) && permissions.isChairperson && <button className="btn btn-secondary" disabled={busy} onClick={() => setModal('withdraw')}>Withdraw</button>}
          {!['Disbursed', 'Declined', 'Withdrawn'].includes(loan.status) && <button className="btn btn-secondary" onClick={() => setModal('change')}>Material change</button>}
        </div>
        {!online && <div className="alert warn">You are offline. Approval, decline and disbursement need the server.</div>}
        <p className="small muted mt-2">Operations review is mandatory. Approval is not disbursement: only Finance, a different person, can record it. Only confirmed disbursements count as loans advanced.</p>
      </Card>

      <div className="grid grid-2 mt-2">
        <Card title="Application">
          <Summary items={[['Purpose', loan.purpose], ['Repayment source', loan.repayment_source], ['Existing debts', money(loan.existing_debts)], ['Cash flow', loan.cash_flow || '—'], ['Member allocation', loan.member_allocation || '—'], ['Security', loan.security || '—'], ['Guarantees', loan.guarantees || '—'], ['Authorised representative', loan.authorised_person_id ? 'Recorded' : '—'], ['Approved', loan.approved_at ? dateTime(loan.approved_at) : '—'], ['Accepted', loan.accepted_at ? dateTime(loan.accepted_at) : '—']]} />
        </Card>
        <Card title="Transparent performance profile" sub="Not a numeric credit score.">
          <Summary items={[['Attendance', pct(profile.attendance.percent)], ['Completed cycles', profile.completedCycles], ['Outstanding principal', money(profile.outstandingPrincipal)], ['Arrears days', profile.arrearsDays], ['Open disputes', profile.openDisputes], ['Posted submissions', `${profile.verifiedRepaymentBehaviour.postedSubmissions} of ${profile.verifiedRepaymentBehaviour.totalSubmissions}`], ['Data completeness', Object.entries(profile.dataCompleteness).map(([k, v]) => `${k}: ${v ? 'yes' : 'no'}`).join(', ')]]} />
        </Card>
      </div>

      <Card title="Guarantors" className="mt-2">
        {guarantors.length === 0 ? <p className="muted small">No guarantor recorded. A guarantor covering the loan is required above UGX 500,000 at approval.</p> : <div className="table-wrap"><table><thead><tr><th>Person</th><th className="right">Amount</th><th>Evidence</th></tr></thead><tbody>{guarantors.map((g) => <tr key={g.id}><td>{g.full_name || '—'}</td><td className="right amount">{money(g.amount)}</td><td>{g.evidence_ref || '—'}</td></tr>)}</tbody></table></div>}
      </Card>

      <Card title="Approval trail" className="mt-2">
        {steps.length === 0 ? <Empty title="No decisions recorded" /> : <div className="timeline">{steps.map((s) => <div className="timeline-item" key={s.id}><span className="timeline-dot" /><div><strong>{s.step.replace(/_/g, ' ')}</strong> · {s.decision}<div className="small muted">{s.actor_name} · {dateTime(s.created_at)}{s.reason ? ` · ${s.reason}` : ''}</div></div></div>)}</div>}
      </Card>

      <Card title="Disbursements" className="mt-2">
        {disbursements.length === 0 ? <p className="muted small">No disbursement. This loan is not counted as advanced.</p> : <div className="table-wrap"><table><thead><tr><th>Date</th><th className="right">Amount</th><th>Confirmed by</th><th>Ledger ref</th></tr></thead><tbody>{disbursements.map((d) => <tr key={d.id}><td>{date(d.disbursed_on)}</td><td className="right amount">{money(d.amount)}</td><td>{d.confirmed_by_name}</td><td>{d.ledger_ref}</td></tr>)}</tbody></table></div>}
        {ledger && <p className="small muted mt-2">Ledger principal: {money(ledger.principal)} · interest due {money(ledger.interest_due)} · fees {money(ledger.fees_due)}</p>}
      </Card>

      <Modal open={!!modal} onClose={() => setModal(null)} title={modal === 'disburse' ? 'Record disbursement' : modal === 'change' ? 'Material change' : 'Record a decision'}>
        {modal === 'disburse' ? <form onSubmit={disburse}><Field label="Amount (UGX)"><input className="input" type="number" min="1" value={disb.amount} onChange={(e) => setDisb((d) => ({ ...d, amount: e.target.value }))} required /></Field><Field label="Disbursement date"><input className="input" type="date" value={disb.disbursedOn} onChange={(e) => setDisb((d) => ({ ...d, disbursedOn: e.target.value }))} required /></Field><div className="alert warn small">You must be Finance and a different person from the approver. This creates a Disbursement and a ledger entry.</div><div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy}>Confirm disbursement</button></div></form>
          : modal === 'change' ? <form onSubmit={material}><Field label="New amount (optional)"><input className="input" type="number" min="1" value={change.amount} onChange={(e) => setChange((c) => ({ ...c, amount: e.target.value }))} /></Field><Field label="New term (months, optional)"><input className="input" type="number" min="1" max="24" value={change.termMonths} onChange={(e) => setChange((c) => ({ ...c, termMonths: e.target.value }))} /></Field><Field label="New purpose (optional)"><input className="input" value={change.purpose} onChange={(e) => setChange((c) => ({ ...c, purpose: e.target.value }))} /></Field><Field label="Reason"><input className="input" value={change.reason} onChange={(e) => setChange((c) => ({ ...c, reason: e.target.value }))} required /></Field><div className="alert warn small">A material change after approval returns the application to Operations review and clears the approval.</div><div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy}>Record change</button></div></form>
            : <form onSubmit={(e) => { e.preventDefault(); transition(modal); }}><p className="small">Record the decision for <strong>{modal}</strong>. {['approve', 'decline'].includes(modal) ? 'You will be asked to confirm your password.' : ''}</p><Field label="Reason (optional)"><textarea className="input" value={reason} onChange={(e) => setReason(e.target.value)} /></Field><div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className={`btn ${modal === 'decline' ? 'btn-danger' : ''}`} disabled={busy}>Record</button></div></form>}
      </Modal>
    </div>
  );
}
