import React, { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { get, post } from '../api.js';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, toast } from '../components.jsx';
import { date } from '../fmt.js';

const TYPES = ['VSLA', 'InvestmentClub', 'Association', 'Cooperative', 'SACCO'];

export default function Groups() {
  const [data, setData] = useState(null);
  const [branches, setBranches] = useState([]);
  const [error, setError] = useState(null);
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState({ name: '', type: 'VSLA', branchId: '' });
  const [busy, setBusy] = useState(false);
  const [filters, setFilters] = useState({ groupType: '', branchId: '' });

  const load = async () => {
    try {
      const qs = new URLSearchParams(Object.entries(filters).filter(([, v]) => v)).toString();
      setData(await get('/groups' + (qs ? `?${qs}` : '')));
      setError(null);
    } catch (err) { setError(err); }
  };
  useEffect(() => { load(); }, [filters]);
  useEffect(() => { get('/branches').then((r) => { setBranches(r.branches); setForm((f) => ({ ...f, branchId: f.branchId || r.branches[0]?.id || '' })); }).catch(() => {}); }, []);

  const create = async (e) => {
    e.preventDefault(); setBusy(true);
    try { const r = await post('/groups', form); setOpen(false); toast('Group created as a draft.'); window.location.assign(`/groups/${r.group.id}`); }
    catch (err) { toast(err.message, 'error'); }
    finally { setBusy(false); }
  };

  if (!data && !error) return <Loading rows={4} />;
  return (
    <div>
      <PageHead title="Groups" sub="VSLA, investment clubs, associations, cooperatives and SACCOs in your scope." actions={data?.canCreate && <button className="btn" onClick={() => setOpen(true)}>New group</button>} />
      <ErrorBox error={error} />
      <div className="toolbar">
        <Field label="Type"><select className="input" value={filters.groupType} onChange={(e) => setFilters((f) => ({ ...f, groupType: e.target.value }))}><option value="">All types</option>{TYPES.map((t) => <option key={t}>{t}</option>)}</select></Field>
        <Field label="Branch"><select className="input" value={filters.branchId} onChange={(e) => setFilters((f) => ({ ...f, branchId: e.target.value }))}><option value="">All branches</option>{branches.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}</select></Field>
      </div>
      {data?.groups?.length === 0 ? <Card><Empty title="No groups in your scope"><p>An assigned field officer can register the first group.</p></Empty></Card> : (
        <div className="table-wrap">
          <table><thead><tr><th>Group</th><th>Type</th><th>Branch</th><th>Status</th><th className="right">Members</th><th className="right">Meetings</th><th className="right">Loans</th><th>Created</th><th /></tr></thead>
            <tbody>{data?.groups?.map((g) => <tr key={g.id}><td><Link to={`/groups/${g.id}`} className="strong">{g.name}</Link><div className="small muted">Evidence: {g.registration_evidence_status}</div></td><td>{g.type}</td><td>{g.branch_name}</td><td><StatusChip status={g.status} /></td><td className="right">{g.counts.members}</td><td className="right">{g.counts.meetings}</td><td className="right">{g.counts.loans}</td><td>{date(g.created_at)}</td><td><Link className="link-btn" to={`/groups/${g.id}`}>Open</Link></td></tr>)}</tbody></table>
        </div>
      )}
      <Modal open={open} onClose={() => setOpen(false)} title="Register a group">
        <form onSubmit={create}>
          <Field label="Group name" htmlFor="g-name"><input id="g-name" className="input" value={form.name} onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))} required minLength={2} /></Field>
          <Field label="Group type" htmlFor="g-type"><select id="g-type" className="input" value={form.type} onChange={(e) => setForm((f) => ({ ...f, type: e.target.value }))}>{TYPES.map((t) => <option key={t}>{t}</option>)}</select></Field>
          <Field label="Branch" htmlFor="g-branch"><select id="g-branch" className="input" value={form.branchId} onChange={(e) => setForm((f) => ({ ...f, branchId: e.target.value }))} required>{branches.map((b) => <option key={b.id} value={b.id}>{b.name} · {b.district}</option>)}</select></Field>
          <div className="alert info small">A type label does not prove legal registration. Registration evidence is tracked separately.</div>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setOpen(false)}>Cancel</button><button className="btn" disabled={busy || !form.name || !form.branchId}>{busy ? 'Creating…' : 'Create draft'}</button></div>
        </form>
      </Modal>
    </div>
  );
}
