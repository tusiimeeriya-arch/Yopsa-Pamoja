import React, { useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { get, post } from '../api.js';
import { useAuth } from '../auth.jsx';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, Tabs, toast } from '../components.jsx';
import { date, money } from '../fmt.js';
import { saveDraft } from '../drafts.js';

const CATEGORIES = ['loan_repayment', 'savings', 'shares', 'fines', 'subscriptions', 'social_fund', 'other'];
const cat = (c) => c.replace(/_/g, ' ');
const newKey = () => crypto.randomUUID();

export default function Payments() {
  const { user, roles, runSensitive } = useAuth();
  const [params] = useSearchParams();
  const roleSet = useMemo(() => new Set(roles.map((r) => r.role)), [roles]);
  const tabs = [
    ...(roleSet.has('member') ? [{ id: 'me', label: 'My payments' }] : []),
    ...(roleSet.has('treasurer') || roleSet.has('field_officer') ? [{ id: 'entry', label: 'Record entries' }] : []),
    { id: 'submissions', label: 'Submissions' },
    ...(roleSet.has('verifier') || roleSet.has('treasurer') ? [{ id: 'cash', label: 'Cash' }] : []),
    ...(roleSet.has('verifier') ? [{ id: 'receipts', label: 'Receipts' }, { id: 'corrections', label: 'Corrections' }, { id: 'queues', label: 'Queues' }] : []),
  ];
  const [tab, setTab] = useState(params.get('tab') || tabs[0]?.id || 'submissions');
  const [groups, setGroups] = useState([]);
  const [groupId, setGroupId] = useState(params.get('group') || '');
  const [group, setGroup] = useState(null);
  const [entries, setEntries] = useState([]);
  const [submissions, setSubmissions] = useState([]);
  const [mine, setMine] = useState([]);
  const [queues, setQueues] = useState(null);
  const [corrections, setCorrections] = useState([]);
  const [cash, setCash] = useState(null);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);
  const [modal, setModal] = useState(null);
  const [selected, setSelected] = useState(null);
  const [form, setForm] = useState({ memberPersonId: '', category: 'savings', amount: '', collectionDate: new Date().toISOString().slice(0, 10) });
  const [period, setPeriod] = useState({ periodStart: new Date(Date.now() - 14 * 864e5).toISOString().slice(0, 10), periodEnd: new Date().toISOString().slice(0, 10) });
  const [receipt, setReceipt] = useState({ amount: '', receiptDate: new Date().toISOString().slice(0, 10), sourceRef: '', destination: 'Yopsa collection account', channel: 'bank', evidenceRef: '' });
  const [resolve, setResolve] = useState({ mode: 'receipt', amount: '', sourceRef: '', receiptDate: new Date().toISOString().slice(0, 10), destination: 'Yopsa collection account', evidenceRef: '', entryIds: [], reason: '' });
  const [correction, setCorrection] = useState({ entryId: '', field: 'amount', newValue: '', reason: '' });
  const online = navigator.onLine;

  useEffect(() => {
    get('/groups').then((r) => { setGroups(r.groups); if (!groupId && r.groups[0]) setGroupId(r.groups[0].id); }).catch(setError);
    get('/payments/me').then((r) => setMine(r.payments)).catch(() => {});
  }, []);
  useEffect(() => {
    if (!groupId) return;
    get(`/groups/${groupId}`).then((r) => setGroup(r)).catch(() => {});
    get(`/payments/entries?groupId=${groupId}`).then((r) => setEntries(r.entries)).catch(() => {});
    get(`/payments/submissions?groupId=${groupId}`).then((r) => setSubmissions(r.submissions)).catch(() => {});
  }, [groupId]);
  useEffect(() => {
    if (tab === 'queues') get('/payments/queues').then(setQueues).catch((err) => toast(err.message, 'error'));
    if (tab === 'corrections') get('/payments/corrections').then((r) => setCorrections(r.corrections)).catch(() => {});
    if (tab === 'cash' && groupId) get(`/payments/cash?groupId=${groupId}`).then(setCash).catch((err) => toast(err.message, 'error'));
  }, [tab, groupId]);

  const loadCash = async () => { if (groupId) setCash(await get(`/payments/cash?groupId=${groupId}`)); };
  const cashAction = async (path, body, msg) => {
    setBusy(true);
    try { await post(path, body); toast(msg); await loadCash(); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };
  const confirmDeposit = (receiptId) => {
    const bankRef = window.prompt('Bank deposit reference');
    if (!bankRef) return;
    cashAction(`/payments/cash/${receiptId}/deposit`, { bankRef, evidenceRef: 'Deposit slip' }, 'Deposit confirmed by a third person.');
  };

  const refresh = async () => {
    if (!groupId) return;
    const [e, s, g] = await Promise.all([get(`/payments/entries?groupId=${groupId}`), get(`/payments/submissions?groupId=${groupId}`), get(`/groups/${groupId}`)]);
    setEntries(e.entries); setSubmissions(s.submissions); setGroup(g);
    get('/payments/me').then((r) => setMine(r.payments)).catch(() => {});
  };

  const run = async (fn, msg) => {
    setBusy(true);
    try { await fn(); toast(msg); setModal(null); await refresh(); }
    catch (err) { toast(err.message, 'error'); }
    finally { setBusy(false); }
  };

  const entryPayload = () => ({ groupId, memberPersonId: form.memberPersonId, category: form.category, amount: Number(form.amount), collectionDate: form.collectionDate, idempotencyKey: newKey() });
  const saveEntry = () => {
    const payload = entryPayload();
    saveDraft(user.id, { type: 'payment_entry', label: `${money(payload.amount)} · ${cat(payload.category)}`, payload });
    toast('Saved on this device with a Pending sync label.');
    setForm((f) => ({ ...f, amount: '' }));
  };
  const recordEntry = async (e) => {
    e.preventDefault();
    const payload = entryPayload();
    if (!navigator.onLine) { saveEntry(); return; }
    await run(() => post('/payments/entries', payload), 'Payment entry recorded as a draft.');
    setForm((f) => ({ ...f, amount: '' }));
  };

  const submitGroup = async (e) => {
    e.preventDefault();
    await run(() => post('/payments/submissions', { groupId, ...period, idempotencyKey: newKey() }), 'Submission locked. Entries can no longer be edited.');
  };

  const doVerify = (id) => run(() => runSensitive(() => post(`/payments/submissions/${id}/verify`, {}), 'verify'), 'Verification checked against receipts.');
  const doPost = (id) => run(() => runSensitive(() => post(`/payments/submissions/${id}/post`, {}), 'post'), 'Posting attempted.');
  const doResolve = (e) => {
    e.preventDefault();
    run(() => runSensitive(() => post(`/payments/submissions/${selected.id}/resolve`, { ...resolve, amount: resolve.amount ? Number(resolve.amount) : undefined, idempotencyKey: newKey() }), 'resolve'), 'Difference resolution recorded.');
  };
  const doAllocate = (receiptId, e) => {
    e.preventDefault();
    const f = e.target;
    run(() => post('/payments/allocations', { receiptId, amount: Number(f.amount.value), category: f.category.value, idempotencyKey: newKey() }), 'Allocation recorded.');
  };
  const doReceipt = (e) => {
    e.preventDefault();
    run(() => post('/payments/receipts', { groupId, ...receipt, amount: Number(receipt.amount), idempotencyKey: newKey() }), 'Receipt recorded.');
  };
  const doCorrection = (e) => {
    e.preventDefault();
    run(() => post('/payments/corrections', { ...correction, idempotencyKey: newKey() }), 'Correction requested for independent approval.');
  };
  const approveCorrection = (id) => run(() => runSensitive(() => post(`/payments/corrections/${id}/approve`, {}), 'correction'), 'Correction approved and applied.');

  const selectedSub = selected ? submissions.find((s) => s.id === selected.id) : null;
  const totals = entries.filter((e) => e.status === 'draft').reduce((acc, e) => { acc[e.category] = (acc[e.category] || 0) + Number(e.amount); return acc; }, {});

  return (
    <div>
      <PageHead title="Payments and reconciliation" sub="Collected, received and posted are three separate facts. Pending entries never change a loan balance." />
      <Tabs tabs={tabs} active={tab} onChange={setTab} />
      <div className="toolbar">
        <Field label="Group"><select className="input" value={groupId} onChange={(e) => setGroupId(e.target.value)}>{groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}</select></Field>
      </div>
      <ErrorBox error={error} />

      {tab === 'me' && <Card title="My payment entries">{mine.length === 0 ? <Empty title="No payments yet" /> : <div className="table-wrap"><table><thead><tr><th>Date</th><th>Group</th><th>Category</th><th className="right">Amount</th><th>Status</th></tr></thead><tbody>{mine.map((p) => <tr key={p.id}><td>{date(p.collection_date)}</td><td>{p.group_name}</td><td style={{ textTransform: 'capitalize' }}>{cat(p.category)}</td><td className="right amount">{money(p.amount)}</td><td><StatusChip status={p.chip} /></td></tr>)}</tbody></table></div>}</Card>}

      {tab === 'entry' && (
        <>
          <Card title="Record a payment entry" sub="Treasurer of this group, or an assigned officer recording how the member confirmed.">
            <form onSubmit={recordEntry} className="form-grid">
              <Field label="Member" htmlFor="p-member"><select id="p-member" className="input" value={form.memberPersonId} onChange={(e) => setForm((f) => ({ ...f, memberPersonId: e.target.value }))} required><option value="">Select member</option>{group?.members?.filter((m) => !m.leave_date).map((m) => <option key={m.person_id} value={m.person_id}>{m.full_name}</option>)}</select></Field>
              <Field label="Category" htmlFor="p-cat"><select id="p-cat" className="input" value={form.category} onChange={(e) => setForm((f) => ({ ...f, category: e.target.value }))}>{CATEGORIES.map((c) => <option key={c} value={c}>{cat(c)}</option>)}</select></Field>
              <Field label="Amount (UGX)" htmlFor="p-amount"><input id="p-amount" className="input" type="number" min="1" step="1" value={form.amount} onChange={(e) => setForm((f) => ({ ...f, amount: e.target.value }))} required /></Field>
              <Field label="Collection date" htmlFor="p-date"><input id="p-date" className="input" type="date" value={form.collectionDate} onChange={(e) => setForm((f) => ({ ...f, collectionDate: e.target.value }))} required /></Field>
              <div className="full row end"><button type="button" className="btn btn-secondary" onClick={saveEntry} disabled={!form.memberPersonId || !form.amount}>Save as draft</button><button className="btn" disabled={busy || !form.memberPersonId || !form.amount}>{busy ? 'Saving…' : 'Record draft entry'}</button></div>
              {!online && <div className="full alert warn">You are offline. Saving keeps this entry on the device as a Pending sync draft; it counts only after the server accepts it.</div>}
            </form>
          </Card>
          <Card title="Draft entries and submission preview" sub="Locking a submission freezes these entries." className="mt-2" actions={<button className="btn btn-sm" onClick={() => setModal('submit')} disabled={!Object.keys(totals).length}>Lock and submit</button>}>
            {entries.length === 0 ? <Empty title="No entries yet" /> : <div className="table-wrap"><table><thead><tr><th>Member</th><th>Category</th><th className="right">Amount</th><th>Date</th><th>Status</th><th /></tr></thead><tbody>{entries.map((e) => <tr key={e.id}><td>{e.full_name}</td><td style={{ textTransform: 'capitalize' }}>{cat(e.category)}</td><td className="right amount">{money(e.amount)}</td><td>{date(e.collection_date)}</td><td><StatusChip status={e.status} /></td><td>{['locked', 'verified', 'posted'].includes(e.status) && <button className="link-btn" onClick={() => { setCorrection({ entryId: e.id, field: 'amount', newValue: '', reason: '' }); setModal('correction'); }}>Correction</button>}</td></tr>)}</tbody></table></div>}
            {Object.keys(totals).length > 0 && <div className="alert info mt-2">Draft totals: {Object.entries(totals).map(([k, v]) => <span key={k} className="pill" style={{ marginRight: 6 }}>{cat(k)}: {money(v)}</span>)}</div>}
          </Card>
        </>
      )}

      {tab === 'submissions' && (
        <Card title="Submissions" sub="A verifier cannot verify their own submission or one they recorded an entry in.">
          {submissions.length === 0 ? <Empty title="No submissions" /> : <div className="table-wrap"><table><thead><tr><th>Period</th><th className="right">Submitted</th><th className="right">Received</th><th className="right">Difference</th><th>Status</th><th /></tr></thead><tbody>{submissions.map((s) => <tr key={s.id}><td>{date(s.period_start)} – {date(s.period_end)}</td><td className="right amount">{money(s.total_amount)}</td><td className="right amount">{s.receipt_total === null ? '—' : money(s.receipt_total)}</td><td className="right amount">{s.difference === null ? '—' : money(s.difference)}</td><td><StatusChip status={s.status} /></td><td><button className="link-btn" onClick={() => setSelected(s)}>Open</button></td></tr>)}</tbody></table></div>}
        </Card>
      )}

      {tab === 'receipts' && (
        <Card title="Record a receipt" sub="Verifier / Finance only. A screenshot alone is not enough evidence.">
          <form onSubmit={doReceipt} className="form-grid">
            <Field label="Amount (UGX)" htmlFor="r-amount"><input id="r-amount" className="input" type="number" min="1" value={receipt.amount} onChange={(e) => setReceipt((r) => ({ ...r, amount: e.target.value }))} required /></Field>
            <Field label="Receipt date" htmlFor="r-date"><input id="r-date" className="input" type="date" value={receipt.receiptDate} onChange={(e) => setReceipt((r) => ({ ...r, receiptDate: e.target.value }))} required /></Field>
            <Field label="Unique source reference" htmlFor="r-ref"><input id="r-ref" className="input" value={receipt.sourceRef} onChange={(e) => setReceipt((r) => ({ ...r, sourceRef: e.target.value }))} required /></Field>
            <Field label="Destination" htmlFor="r-dest"><input id="r-dest" className="input" value={receipt.destination} onChange={(e) => setReceipt((r) => ({ ...r, destination: e.target.value }))} required /></Field>
            <Field label="Channel" htmlFor="r-channel"><select id="r-channel" className="input" value={receipt.channel} onChange={(e) => setReceipt((r) => ({ ...r, channel: e.target.value }))}><option value="bank">Bank</option><option value="cash">Cash</option><option value="mobile_money">Mobile money</option></select></Field>
            <Field label="Evidence reference" htmlFor="r-evidence"><input id="r-evidence" className="input" value={receipt.evidenceRef} onChange={(e) => setReceipt((r) => ({ ...r, evidenceRef: e.target.value }))} required /></Field>
            <div className="full row end"><button className="btn" disabled={busy || !receipt.amount || !receipt.sourceRef || !receipt.evidenceRef}>{busy ? 'Saving…' : 'Record receipt'}</button></div>
          </form>
        </Card>
      )}

      {tab === 'cash' && (
        <div className="grid grid-2">
          <Card title="Cash received, not yet reconciled" sub="Hand the cash over first. Only a cash receipt appears here.">
            {cash?.unreconciled?.length ? cash.unreconciled.map((r) => <div className="alert info" key={r.id}><div className="row between"><strong>{money(r.amount)}</strong><span className="small">{r.source_ref}</span></div><div className="small">{date(r.receipt_date)}</div><button className="btn btn-sm mt-1" disabled={busy} onClick={() => cashAction(`/payments/cash/${r.id}/handover`, {}, 'Cash handed over. The branch must receive it next.')}>Hand over cash</button></div>) : <Empty title="Nothing awaiting handover" />}
          </Card>
          <Card title="Three-step reconciliation" sub="Handover (treasurer) → received by the branch → deposit confirmed by a third person with a bank reference.">
            {cash?.reconciliations?.length ? <div className="table-wrap"><table><thead><tr><th>Amount</th><th>Status</th><th>Handed over</th><th>Received</th><th>Deposited</th><th /></tr></thead><tbody>{cash.reconciliations.map((c) => <tr key={c.id}><td className="right amount">{money(c.amount)}</td><td><StatusChip status={c.status} /></td><td className="small">{c.handover_by_name || '—'}</td><td className="small">{c.received_by_name || '—'}</td><td className="small">{c.deposited_by_name || '—'}{c.bank_ref ? ` · ${c.bank_ref}` : ''}</td><td className="nowrap">{c.status === 'awaiting_receipt' && <button className="link-btn" disabled={busy} onClick={() => cashAction(`/payments/cash/${c.receipt_id}/receive`, {}, 'Branch receipt confirmed.')}>Receive</button>}{c.status === 'awaiting_deposit' && <button className="link-btn" disabled={busy} onClick={() => confirmDeposit(c.receipt_id)}>Confirm deposit</button>}</td></tr>)}</tbody></table></div> : <Empty title="No cash in reconciliation" />}
          </Card>
        </div>
      )}

      {tab === 'corrections' && (
        <Card title="Corrections" sub="Locked or verified records change only through an approved correction.">
          {corrections.length === 0 ? <Empty title="No corrections" /> : <div className="table-wrap"><table><thead><tr><th>Requested</th><th>Field</th><th>Original</th><th>New</th><th>Reason</th><th>Status</th><th /></tr></thead><tbody>{corrections.map((c) => <tr key={c.id}><td>{c.requested_by_name}<div className="small muted">{date(c.created_at)}</div></td><td>{c.field}</td><td>{c.original_value}</td><td>{c.new_value}</td><td>{c.reason}</td><td><StatusChip status={c.status} /></td><td>{c.status === 'pending' && <button className="link-btn" disabled={busy} onClick={() => approveCorrection(c.id)}>Approve</button>}</td></tr>)}</tbody></table></div>}
        </Card>
      )}

      {tab === 'queues' && (
        <div className="grid grid-2">
          <Card title="Awaiting verification">{queues?.submissions?.length ? queues.submissions.map((s) => <div className="alert info" key={s.id}><div className="row between"><strong>{s.group_name}</strong><StatusChip status={s.status} /></div><div className="small">{money(s.total_amount)} submitted · {date(s.submitted_at)}</div><div className="row mt-1"><button className="link-btn" onClick={() => { setSelected(s); setTab('submissions'); }}>Open</button></div></div>) : <Empty title="Nothing waiting" />}</Card>
          <Card title="Open exceptions">{queues?.exceptions?.length ? queues.exceptions.map((e) => <div className="alert warn" key={e.id}>{e.detail}<div className="small">Owner {e.owner_name} · deadline {date(e.deadline)}</div></div>) : <Empty title="No exceptions" />}</Card>
          <Card title="Receipts" className="mt-2">{queues?.receipts?.length ? <div className="table-wrap"><table><thead><tr><th>Group</th><th className="right">Amount</th><th className="right">Allocated</th><th>Reference</th></tr></thead><tbody>{queues.receipts.map((r) => <tr key={r.id}><td>{r.group_name}</td><td className="right amount">{money(r.amount)}</td><td className="right amount">{money(r.allocated)}</td><td>{r.source_ref}</td></tr>)}</tbody></table></div> : <Empty title="No receipts" />}</Card>
          <Card title="Pending corrections" className="mt-2">{queues?.corrections?.length ? queues.corrections.map((c) => <div className="alert info" key={c.id}>{c.requested_by_name}: {c.field} {c.original_value} → {c.new_value}<div className="small">{c.reason}</div></div>) : <Empty title="No corrections" />}</Card>
        </div>
      )}

      <Modal open={modal === 'submit'} onClose={() => setModal(null)} title="Lock and submit entries">
        <form onSubmit={submitGroup}>
          <div className="form-grid"><Field label="Period start" htmlFor="s-start"><input id="s-start" className="input" type="date" value={period.periodStart} onChange={(e) => setPeriod((p) => ({ ...p, periodStart: e.target.value }))} required /></Field><Field label="Period end" htmlFor="s-end"><input id="s-end" className="input" type="date" value={period.periodEnd} onChange={(e) => setPeriod((p) => ({ ...p, periodEnd: e.target.value }))} required /></Field></div>
          <div className="alert warn">Once locked, entries can no longer be edited. A correction is the only route.</div>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy}>Lock and submit</button></div>
        </form>
      </Modal>

      <Modal open={!!selectedSub} onClose={() => setSelected(null)} title="Submission detail" wide>
        {selectedSub && <>
          <div className="grid grid-3 mb-2">
            <div className="stat"><div className="stat-label">Submitted</div><div className="stat-value" style={{ fontSize: '1.05rem' }}>{money(selectedSub.total_amount)}</div></div>
            <div className="stat"><div className="stat-label">Received</div><div className="stat-value" style={{ fontSize: '1.05rem' }}>{money(selectedSub.receipt_total)}</div></div>
            <div className="stat"><div className="stat-label">Difference</div><div className="stat-value" style={{ fontSize: '1.05rem', color: selectedSub.difference ? 'var(--danger)' : 'var(--success)' }}>{selectedSub.difference === null ? '—' : money(selectedSub.difference)}</div></div>
          </div>
          <div className="row mb-2"><StatusChip status={selectedSub.status} /><span className="small muted">Submitted by {selectedSub.submitted_by_name} · {date(selectedSub.submitted_at)}</span></div>
          {selectedSub.status === 'difference' && <div className="alert danger">Full verification is blocked. Received is {money(Math.abs(selectedSub.difference))} {selectedSub.difference < 0 ? 'short' : 'over'} of the submitted total. Resolve only with a missing receipt or a logged partial verification of fully evidenced entries.</div>}
          <h3>Entries</h3>
          {selectedSub.entries?.length ? <div className="table-wrap"><table><thead><tr><th>Member</th><th>Category</th><th className="right">Amount</th><th>Status</th></tr></thead><tbody>{selectedSub.entries.map((e) => <tr key={e.id}><td>{e.full_name}</td><td style={{ textTransform: 'capitalize' }}>{cat(e.category)}</td><td className="right amount">{money(e.amount)}</td><td><StatusChip status={e.status} /></td></tr>)}</tbody></table></div> : <p className="muted small">Entries are loaded in the submission list.</p>}
          <h3 className="mt-3">Receipts and allocations</h3>
          {selectedSub.receipts?.length ? selectedSub.receipts.map((r) => <div className="alert info" key={r.id}><div className="row between"><strong>{money(r.amount)}</strong><span className="small">{r.source_ref}</span></div><div className="small">{date(r.receipt_date)} · {r.channel} · allocated {money(selectedSub.allocations.filter((a) => a.receipt_id === r.id).reduce((s, a) => s + Number(a.amount), 0))}</div>
            <form className="row mt-1" onSubmit={(e) => doAllocate(r.id, e)}><input name="amount" className="input" type="number" min="1" placeholder="Allocate" style={{ maxWidth: 130 }} required /><select name="category" className="input" style={{ maxWidth: 170 }}>{CATEGORIES.map((c) => <option key={c} value={c}>{cat(c)}</option>)}</select><button className="btn btn-sm" disabled={busy}>Allocate</button></form>
          </div>) : <p className="muted small">No receipts linked.</p>}
          <div className="row end mt-2">
            {['submitted', 'difference'].includes(selectedSub.status) && <button className="btn" disabled={busy || !online} onClick={() => doVerify(selectedSub.id)}>Verify against receipts</button>}
            {selectedSub.status === 'difference' && <button className="btn btn-secondary" onClick={() => { setResolve((r) => ({ ...r, reason: '' })); setModal('resolve'); }}>Resolve difference</button>}
            {['verified', 'partially_verified'].includes(selectedSub.status) && <button className="btn btn-green" disabled={busy || !online} onClick={() => doPost(selectedSub.id)}>Post to ledger</button>}
          </div>
          {selectedSub.postings?.length > 0 && <><h3 className="mt-3">Postings</h3>{selectedSub.postings.map((p) => <div className="alert warn" key={p.id}>{money(p.amount)} · {p.status} {p.ledger_ref ? `· ${p.ledger_ref}` : ''}{p.error ? ` · ${p.error}` : ''}</div>)}</>}
        </>}
      </Modal>

      <Modal open={modal === 'resolve'} onClose={() => setModal(null)} title="Resolve the difference">
        {selectedSub && <form onSubmit={doResolve}>
          <Field label="How is it resolved?" htmlFor="res-mode"><select id="res-mode" className="input" value={resolve.mode} onChange={(e) => setResolve((r) => ({ ...r, mode: e.target.value }))}><option value="receipt">Record the missing receipt</option><option value="partial">Verify only fully evidenced entries and return the rest</option></select></Field>
          {resolve.mode === 'receipt' ? <>
            <div className="form-grid"><Field label="Amount (UGX)"><input className="input" type="number" min="1" value={resolve.amount} onChange={(e) => setResolve((r) => ({ ...r, amount: e.target.value }))} required /></Field><Field label="Receipt date"><input className="input" type="date" value={resolve.receiptDate} onChange={(e) => setResolve((r) => ({ ...r, receiptDate: e.target.value }))} /></Field><Field label="Source reference"><input className="input" value={resolve.sourceRef} onChange={(e) => setResolve((r) => ({ ...r, sourceRef: e.target.value }))} required /></Field><Field label="Evidence reference"><input className="input" value={resolve.evidenceRef} onChange={(e) => setResolve((r) => ({ ...r, evidenceRef: e.target.value }))} required /></Field></div>
          </> : <>
            <p className="small muted">Select only entries that are individually and fully evidenced. The rest are returned. A shortage is never spread across members.</p>
            {(selectedSub.entries || []).map((e) => <label className="check mb-1" key={e.id}><input type="checkbox" checked={resolve.entryIds.includes(e.id)} onChange={(ev) => setResolve((r) => ({ ...r, entryIds: ev.target.checked ? [...r.entryIds, e.id] : r.entryIds.filter((x) => x !== e.id) }))} /> {e.full_name} · {money(e.amount)} · {cat(e.category)}</label>)}
          </>}
          <Field label="Reason (logged)"><textarea className="input" value={resolve.reason} onChange={(e) => setResolve((r) => ({ ...r, reason: e.target.value }))} minLength={5} required /></Field>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy || !resolve.reason || (resolve.mode === 'partial' && !resolve.entryIds.length)}>Record resolution</button></div>
        </form>}
      </Modal>

      <Modal open={modal === 'correction'} onClose={() => setModal(null)} title="Request a correction">
        <form onSubmit={doCorrection}>
          <Field label="Field" htmlFor="c-field"><select id="c-field" className="input" value={correction.field} onChange={(e) => setCorrection((c) => ({ ...c, field: e.target.value }))}><option value="amount">Amount</option><option value="category">Category</option><option value="collection_date">Collection date</option></select></Field>
          <Field label="New value" htmlFor="c-value"><input id="c-value" className="input" value={correction.newValue} onChange={(e) => setCorrection((c) => ({ ...c, newValue: e.target.value }))} required /></Field>
          <Field label="Reason" htmlFor="c-reason"><textarea id="c-reason" className="input" value={correction.reason} onChange={(e) => setCorrection((c) => ({ ...c, reason: e.target.value }))} minLength={10} required /></Field>
          <div className="alert warn small">An independent verifier must approve. The original value is retained.</div>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy || correction.reason.length < 10 || !correction.newValue}>Request correction</button></div>
        </form>
      </Modal>
    </div>
  );
}
