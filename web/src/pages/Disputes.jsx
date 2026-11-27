import React, { useEffect, useState } from 'react';
import { get, post } from '../api.js';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, toast } from '../components.jsx';
import { date, dateTime, money } from '../fmt.js';

export default function Disputes() {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [payments, setPayments] = useState([]);
  const [records, setRecords] = useState([]);
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState({ kind: 'payment', entityId: '', reason: '' });
  const [resolve, setResolve] = useState(null);
  const [resolution, setResolution] = useState('');
  const [busy, setBusy] = useState(false);

  const load = async () => {
    try { setData(await get('/disputes')); setError(null); } catch (err) { setError(err); }
    try { setPayments((await get('/payments/me')).payments); } catch { /* not a member */ }
    try { setRecords((await get('/me/attendance-records')).records); } catch { /* no person */ }
  };
  useEffect(() => { load(); }, []);

  const create = async (e) => {
    e.preventDefault(); setBusy(true);
    try { await post('/disputes', form); toast('Dispute sent. Only you, the assigned officer and the reviewer can see it.'); setOpen(false); setForm({ kind: 'payment', entityId: '', reason: '' }); load(); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };
  const submitResolve = async (e) => {
    e.preventDefault(); setBusy(true);
    try { await post(`/disputes/${resolve.id}/resolve`, { resolution, status: 'resolved' }); toast('Dispute resolved.'); setResolve(null); setResolution(''); load(); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };

  if (!data && !error) return <Loading rows={3} />;
  return (
    <div>
      <PageHead title="Disputes" sub="A confidential route for wrong attendance or payment records. Visible only to the member, the assigned officer and the reviewer." actions={<button className="btn" onClick={() => setOpen(true)}>Raise a dispute</button>} />
      <ErrorBox error={error} />
      <Card>
        {data?.disputes?.length ? <div className="table-wrap"><table><thead><tr><th>Kind</th><th>Raised by</th><th>Reason</th><th>Original value</th><th>Status</th><th>Raised</th><th /></tr></thead><tbody>{data.disputes.map((d) => <tr key={d.id}><td style={{ textTransform: 'capitalize' }}>{d.kind}</td><td>{d.raised_by_name}</td><td>{d.reason}</td><td>{d.original_value || '—'}</td><td><StatusChip status={d.status} /></td><td>{date(d.created_at)}</td><td>{d.status === 'open' && data.canResolve && <button className="link-btn" onClick={() => setResolve(d)}>Review</button>}</td></tr>)}</tbody></table></div> : <Empty title="No disputes" />}
      </Card>
      <Modal open={open} onClose={() => setOpen(false)} title="Raise a dispute">
        <form onSubmit={create}>
          <Field label="What is wrong?" htmlFor="d-kind"><select id="d-kind" className="input" value={form.kind} onChange={(e) => setForm((f) => ({ ...f, kind: e.target.value, entityId: '' }))}><option value="payment">A payment entry</option><option value="attendance">An attendance record</option></select></Field>
          <Field label="Record" htmlFor="d-record">
            <select id="d-record" className="input" value={form.entityId} onChange={(e) => setForm((f) => ({ ...f, entityId: e.target.value }))} required>
              <option value="">Select your record</option>
              {form.kind === 'payment' ? payments.map((p) => <option key={p.id} value={p.id}>{date(p.collection_date)} · {money(p.amount)} · {p.group_name}</option>) : records.map((r) => <option key={r.id} value={r.id}>{r.meeting_title} · {r.status} · {date(r.scheduled_for)}</option>)}
            </select>
          </Field>
          <Field label="Reason" htmlFor="d-reason"><textarea id="d-reason" className="input" value={form.reason} onChange={(e) => setForm((f) => ({ ...f, reason: e.target.value }))} minLength={10} required /></Field>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setOpen(false)}>Cancel</button><button className="btn" disabled={busy || !form.entityId || form.reason.length < 10}>Send dispute</button></div>
        </form>
      </Modal>
      <Modal open={!!resolve} onClose={() => setResolve(null)} title="Review dispute">
        {resolve && <form onSubmit={submitResolve}>
          <p className="small">{resolve.reason}</p>
          <p className="small muted">Original value: {resolve.original_value || '—'} · raised {dateTime(resolve.created_at)}</p>
          <Field label="Resolution" htmlFor="dr"><textarea id="dr" className="input" value={resolution} onChange={(e) => setResolution(e.target.value)} minLength={5} required /></Field>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setResolve(null)}>Cancel</button><button className="btn" disabled={busy || resolution.length < 5}>Resolve</button></div>
        </form>}
      </Modal>
    </div>
  );
}
