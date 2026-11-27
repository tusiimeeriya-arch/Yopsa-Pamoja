import React, { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { get, post } from '../api.js';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, toast } from '../components.jsx';
import { date, money } from '../fmt.js';

function fileToBase64(file) {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve({ filename: file.name, mime: file.type, size: file.size, data: String(r.result).split(',')[1] });
    r.onerror = reject;
    r.readAsDataURL(file);
  });
}

const EMPTY = { groupId: '', product: 'group_loan', amount: '', purpose: '', termMonths: 12, repaymentSource: '', existingDebts: 0, cashFlow: '', memberAllocation: '', security: '', guarantees: '', guarantorPersonId: '', guarantorAmount: '', guarantorEvidence: '' };

export default function Loans() {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState(EMPTY);
  const [busy, setBusy] = useState(false);
  const [mlGroup, setMlGroup] = useState('');
  const [members, setMembers] = useState([]);
  const [memberLoans, setMemberLoans] = useState([]);
  const [mlForm, setMlForm] = useState({ lenderPersonId: '', borrowerPersonId: '', amount: '', purpose: '', dueOn: '' });

  const load = () => get('/loans').then((r) => {
    setData(r);
    if (!form.groupId && r.groups[0]) setForm((f) => ({ ...f, groupId: r.groups[0].id }));
    if (!mlGroup && r.groups[0]) setMlGroup(r.groups[0].id);
  }).catch(setError);
  useEffect(() => { load(); }, []);
  useEffect(() => {
    if (!mlGroup) return;
    get(`/groups/${mlGroup}`).then((r) => setMembers((r.members || []).filter((m) => !m.leave_date))).catch(() => {});
    get(`/member-loans?groupId=${mlGroup}`).then((r) => setMemberLoans(r.memberLoans || [])).catch(() => {});
  }, [mlGroup]);

  const createMemberLoan = async (e) => {
    e.preventDefault(); setBusy(true);
    try {
      await post('/member-loans', { groupId: mlGroup, ...mlForm, amount: Number(mlForm.amount) });
      toast('Internal member loan recorded. It is not a Yopsa loan.');
      setMlForm({ lenderPersonId: '', borrowerPersonId: '', amount: '', purpose: '', dueOn: '' });
      const r = await get(`/member-loans?groupId=${mlGroup}`); setMemberLoans(r.memberLoans || []);
    } catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };

  const create = async (e) => {
    e.preventDefault(); setBusy(true);
    try {
      const resolutionFile = e.target.resolution.files[0];
      const quorumFile = e.target.quorum.files[0];
      if (!resolutionFile || !quorumFile) { toast('Attach the borrowing resolution and quorum evidence.', 'error'); setBusy(false); return; }
      const [resDoc, qDoc] = await Promise.all([
        fileToBase64(resolutionFile).then((p) => post(`/groups/${form.groupId}/documents`, { ...p, kind: 'other' })),
        fileToBase64(quorumFile).then((p) => post(`/groups/${form.groupId}/documents`, { ...p, kind: 'other' })),
      ]);
      const payload = {
        groupId: form.groupId, product: form.product, amount: Number(form.amount), purpose: form.purpose,
        termMonths: Number(form.termMonths), repaymentSource: form.repaymentSource, existingDebts: Number(form.existingDebts || 0),
        cashFlow: form.cashFlow, memberAllocation: form.memberAllocation, security: form.security, guarantees: form.guarantees,
        resolutionRef: resDoc.file.id, quorumEvidenceRef: qDoc.file.id,
        guarantors: form.guarantorPersonId && form.guarantorAmount ? [{ personId: form.guarantorPersonId, amount: Number(form.guarantorAmount), evidenceRef: form.guarantorEvidence || 'Guarantee form' }] : [],
        idempotencyKey: crypto.randomUUID(),
      };
      const r = await post('/loans', payload);
      setOpen(false); setForm(EMPTY); toast('Loan application created as a draft.'); window.location.assign(`/loans/${r.loan.id}`);
    } catch (err) { toast(err.message, 'error'); }
    finally { setBusy(false); }
  };

  if (!data && !error) return <Loading rows={4} />;
  return (
    <div>
      <PageHead title="Group loans" sub="Yopsa group loans only. Internal member-to-member loans are a separate workflow and are never counted here." actions={<button className="btn" disabled={!data?.groups?.length} onClick={() => setOpen(true)}>Apply for a group loan</button>} />
      <ErrorBox error={error} />
      <Card>
        {data?.loans?.length ? <div className="table-wrap"><table><thead><tr><th>Group</th><th>Product</th><th className="right">Amount</th><th>Term</th><th>Status</th><th>Updated</th><th /></tr></thead><tbody>{data.loans.map((l) => <tr key={l.id}><td>{l.group_name}</td><td>{l.product.replace(/_/g, ' ')}</td><td className="right amount">{money(l.amount)}</td><td>{l.term_months} months</td><td><StatusChip status={l.status} /></td><td>{date(l.updated_at)}</td><td><Link className="link-btn" to={`/loans/${l.id}`}>Open</Link></td></tr>)}</tbody></table></div> : <Empty title="No loan applications"><p>An authorised group representative can start an application.</p></Empty>}
      </Card>

      <Card title="Internal member-to-member loans" className="mt-2" sub="A separate record between two members. It never creates a Yopsa loan, disbursement or ledger posting.">
        <div className="toolbar"><Field label="Group" htmlFor="ml-group"><select id="ml-group" className="input" value={mlGroup} onChange={(e) => setMlGroup(e.target.value)}>{data?.groups?.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}</select></Field></div>
        {memberLoans.length ? <div className="table-wrap"><table><thead><tr><th>Agreed</th><th>Lender</th><th>Borrower</th><th className="right">Amount</th><th>Due</th><th>Status</th></tr></thead><tbody>{memberLoans.map((l) => <tr key={l.id}><td>{date(l.agreed_on)}</td><td>{l.lender_name}</td><td>{l.borrower_name}</td><td className="right amount">{money(l.amount)}</td><td>{l.due_on ? date(l.due_on) : '—'}</td><td><StatusChip status={l.status} /></td></tr>)}</tbody></table></div> : <Empty title="No internal member loans"><p>Recorded by a group officer; not counted in Yopsa loans advanced.</p></Empty>}
        {members.length > 1 && <form className="form-grid mt-2" onSubmit={createMemberLoan}>
          <Field label="Lender" htmlFor="ml-lender"><select id="ml-lender" className="input" value={mlForm.lenderPersonId} onChange={(e) => setMlForm((f) => ({ ...f, lenderPersonId: e.target.value }))} required><option value="">Select member</option>{members.map((m) => <option key={m.person_id} value={m.person_id}>{m.full_name}</option>)}</select></Field>
          <Field label="Borrower" htmlFor="ml-borrower"><select id="ml-borrower" className="input" value={mlForm.borrowerPersonId} onChange={(e) => setMlForm((f) => ({ ...f, borrowerPersonId: e.target.value }))} required><option value="">Select member</option>{members.map((m) => <option key={m.person_id} value={m.person_id}>{m.full_name}</option>)}</select></Field>
          <Field label="Amount (UGX)" htmlFor="ml-amount"><input id="ml-amount" className="input" type="number" min="1" value={mlForm.amount} onChange={(e) => setMlForm((f) => ({ ...f, amount: e.target.value }))} required /></Field>
          <Field label="Due on" htmlFor="ml-due"><input id="ml-due" className="input" type="date" value={mlForm.dueOn} onChange={(e) => setMlForm((f) => ({ ...f, dueOn: e.target.value }))} /></Field>
          <div className="full"><Field label="Purpose" htmlFor="ml-purpose"><input id="ml-purpose" className="input" value={mlForm.purpose} onChange={(e) => setMlForm((f) => ({ ...f, purpose: e.target.value }))} required minLength={5} /></Field></div>
          <div className="full row end"><button className="btn" disabled={busy || !mlForm.lenderPersonId || !mlForm.borrowerPersonId || mlForm.lenderPersonId === mlForm.borrowerPersonId || !mlForm.amount || mlForm.purpose.length < 5}>Record internal loan</button></div>
        </form>}
      </Card>

      <Modal open={open} onClose={() => setOpen(false)} title="Apply for a Yopsa group loan" wide>
        <form onSubmit={create}>
          <div className="form-grid">
            <Field label="Group" htmlFor="l-group"><select id="l-group" className="input" value={form.groupId} onChange={(e) => setForm((f) => ({ ...f, groupId: e.target.value }))} required>{data?.groups?.map((g) => <option key={g.id} value={g.id}>{g.name} · {g.type}</option>)}</select></Field>
            <Field label="Amount (UGX)" htmlFor="l-amount"><input id="l-amount" className="input" type="number" min="1" value={form.amount} onChange={(e) => setForm((f) => ({ ...f, amount: e.target.value }))} required /></Field>
            <Field label="Term (months)" htmlFor="l-term"><input id="l-term" className="input" type="number" min="1" max="24" value={form.termMonths} onChange={(e) => setForm((f) => ({ ...f, termMonths: e.target.value }))} required /></Field>
            <Field label="Existing debts (UGX)" htmlFor="l-debts"><input id="l-debts" className="input" type="number" min="0" value={form.existingDebts} onChange={(e) => setForm((f) => ({ ...f, existingDebts: e.target.value }))} /></Field>
            <div className="full"><Field label="Purpose" htmlFor="l-purpose"><textarea id="l-purpose" className="input" value={form.purpose} onChange={(e) => setForm((f) => ({ ...f, purpose: e.target.value }))} required minLength={5} /></Field></div>
            <div className="full"><Field label="Repayment source" htmlFor="l-source"><input id="l-source" className="input" value={form.repaymentSource} onChange={(e) => setForm((f) => ({ ...f, repaymentSource: e.target.value }))} required /></Field></div>
            <Field label="Cash flow" htmlFor="l-cash"><input id="l-cash" className="input" value={form.cashFlow} onChange={(e) => setForm((f) => ({ ...f, cashFlow: e.target.value }))} /></Field>
            <Field label="Member allocation" htmlFor="l-alloc"><input id="l-alloc" className="input" value={form.memberAllocation} onChange={(e) => setForm((f) => ({ ...f, memberAllocation: e.target.value }))} /></Field>
            <Field label="Security" htmlFor="l-sec"><input id="l-sec" className="input" value={form.security} onChange={(e) => setForm((f) => ({ ...f, security: e.target.value }))} /></Field>
            <Field label="Guarantees" htmlFor="l-guar"><input id="l-guar" className="input" value={form.guarantees} onChange={(e) => setForm((f) => ({ ...f, guarantees: e.target.value }))} /></Field>
            <div className="full"><h3 className="mt-2">Attachments</h3><p className="small muted">A borrowing resolution and quorum evidence are required before submission. Images or PDF, 5 MB each.</p></div>
            <Field label="Borrowing resolution" htmlFor="l-resolution"><input id="l-resolution" name="resolution" className="input" type="file" accept="image/*,application/pdf" required /></Field>
            <Field label="Quorum evidence" htmlFor="l-quorum"><input id="l-quorum" name="quorum" className="input" type="file" accept="image/*,application/pdf" required /></Field>
            <div className="full"><h3 className="mt-2">Guarantor (required above UGX 500,000 at approval)</h3></div>
            <Field label="Guarantor" htmlFor="l-gperson"><select id="l-gperson" className="input" value={form.guarantorPersonId} onChange={(e) => setForm((f) => ({ ...f, guarantorPersonId: e.target.value }))}><option value="">None</option></select></Field>
            <Field label="Guarantee amount" htmlFor="l-gamount"><input id="l-gamount" className="input" type="number" min="0" value={form.guarantorAmount} onChange={(e) => setForm((f) => ({ ...f, guarantorAmount: e.target.value }))} /></Field>
          </div>
          <div className="alert info small">Only the group chairperson or an authorised representative with valid leadership authority can bind the group. Approval is not disbursement.</div>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setOpen(false)}>Cancel</button><button className="btn" disabled={busy || !form.groupId || !form.amount || !form.purpose}>{busy ? 'Creating…' : 'Create draft application'}</button></div>
        </form>
      </Modal>
    </div>
  );
}
