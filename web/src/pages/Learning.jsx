import React, { useEffect, useMemo, useState } from 'react';
import { get, post } from '../api.js';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, toast } from '../components.jsx';
import { date } from '../fmt.js';

const CATS = ['financial_literacy', 'business_cash_flow', 'borrowing_responsibilities', 'group_governance', 'recordkeeping', 'fraud_prevention'];

export default function Learning() {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [q, setQ] = useState('');
  const [cat, setCat] = useState('');
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState({ title: '', category: 'financial_literacy', kind: 'text', body: '', language: 'English', contentOwner: 'Yopsa Training', reviewDate: '' });
  const [busy, setBusy] = useState(false);
  const [groups, setGroups] = useState([]);
  const [assign, setAssign] = useState({});

  const load = () => get('/learning').then(setData).catch(setError);
  useEffect(() => { load(); get('/groups').then((r) => setGroups(r.groups)).catch(() => {}); }, []);
  const items = useMemo(() => (data?.items || []).filter((i) => (!cat || i.category === cat) && (!q || i.title.toLowerCase().includes(q.toLowerCase()))), [data, cat, q]);

  const progress = async (id, status) => {
    try { await post(`/learning/${id}/progress`, { status }); toast(status === 'completed' ? 'Marked complete. This never clears arrears or changes lending eligibility.' : 'Opened.'); load(); }
    catch (err) { toast(err.message, 'error'); }
  };
  const assignGroup = async (id) => {
    if (!assign[id]) return;
    try { await post(`/learning/${id}/assign`, { groupId: assign[id] }); toast('Lesson assigned to the group.'); load(); }
    catch (err) { toast(err.message, 'error'); }
  };
  const create = async (e) => {
    e.preventDefault(); setBusy(true);
    try { await post('/learning', { ...form, reviewDate: form.reviewDate || null }); toast('Lesson added.'); setOpen(false); load(); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };

  if (!data && !error) return <Loading rows={3} />;
  return (
    <div>
      <PageHead title="Learning library" sub="Financial literacy, cash flow, borrowing responsibilities, governance, recordkeeping and fraud prevention." actions={data?.canManage && <button className="btn" onClick={() => setOpen(true)}>Add lesson</button>} />
      <ErrorBox error={error} />
      <div className="toolbar">
        <Field label="Search"><input className="input" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search lessons" /></Field>
        <Field label="Category"><select className="input" value={cat} onChange={(e) => setCat(e.target.value)}><option value="">All categories</option>{CATS.map((c) => <option key={c} value={c}>{c.replace(/_/g, ' ')}</option>)}</select></Field>
      </div>
      {items.length === 0 ? <Card><Empty title="No lessons found" /></Card> : <div className="grid grid-2">{items.map((i) => (
        <Card key={i.id} title={i.title} sub={`${i.category.replace(/_/g, ' ')} · ${i.language} · v${i.version} · owner ${i.content_owner}${i.review_date ? ` · review ${date(i.review_date)}` : ''}`}>
          <p className="small">{i.body}</p>
          {i.url && <a className="link-btn" href={i.url} target="_blank" rel="noreferrer">Open {i.kind}</a>}
          <div className="row between mt-2">
            <span className="small muted">{i.assignedGroups.length ? `Assigned to ${i.assignedGroups.join(', ')}` : 'Not assigned to a group'}</span>
            <span>{i.progress?.status === 'completed' ? <span className="chip verified">Completed</span> : i.progress?.status === 'opened' ? <span className="chip pending">Opened</span> : <span className="chip draft">Not started</span>}</span>
          </div>
          <div className="row mt-2">
            <button className="btn btn-sm btn-secondary" onClick={() => progress(i.id, 'opened')}>Mark opened</button>
            <button className="btn btn-sm" onClick={() => progress(i.id, 'completed')}>Mark completed</button>
          </div>
          {data?.canManage && <div className="row mt-2"><select className="input" style={{ maxWidth: 220 }} value={assign[i.id] || ''} onChange={(e) => setAssign((a) => ({ ...a, [i.id]: e.target.value }))}><option value="">Assign to group…</option>{groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}</select><button className="btn btn-sm btn-secondary" disabled={!assign[i.id]} onClick={() => assignGroup(i.id)}>Assign</button></div>}
        </Card>
      ))}</div>}
      <p className="small muted mt-2">Completion never clears arrears or changes lending eligibility.</p>

      <Modal open={open} onClose={() => setOpen(false)} title="Add a learning item">
        <form onSubmit={create}>
          <Field label="Title"><input className="input" value={form.title} onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))} required /></Field>
          <div className="form-grid"><Field label="Category"><select className="input" value={form.category} onChange={(e) => setForm((f) => ({ ...f, category: e.target.value }))}>{CATS.map((c) => <option key={c} value={c}>{c.replace(/_/g, ' ')}</option>)}</select></Field><Field label="Kind"><select className="input" value={form.kind} onChange={(e) => setForm((f) => ({ ...f, kind: e.target.value }))}><option value="text">Text</option><option value="video">Video link</option><option value="pdf">PDF link</option></select></Field><Field label="Language"><input className="input" value={form.language} onChange={(e) => setForm((f) => ({ ...f, language: e.target.value }))} /></Field><Field label="Review date"><input className="input" type="date" value={form.reviewDate} onChange={(e) => setForm((f) => ({ ...f, reviewDate: e.target.value }))} /></Field></div>
          <Field label="Content"><textarea className="input" value={form.body} onChange={(e) => setForm((f) => ({ ...f, body: e.target.value }))} /></Field>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setOpen(false)}>Cancel</button><button className="btn" disabled={busy || !form.title}>Add lesson</button></div>
        </form>
      </Modal>
    </div>
  );
}
