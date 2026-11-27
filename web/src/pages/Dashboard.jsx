import React, { useEffect, useState } from 'react';
import { api, get } from '../api.js';
import { useAuth } from '../auth.jsx';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, toast } from '../components.jsx';
import { csvDownload, date, dateTime, money, num, pct, todayLocal } from '../fmt.js';

const TYPES = ['VSLA', 'InvestmentClub', 'Association', 'Cooperative', 'SACCO'];

export default function Dashboard() {
  const { runSensitive } = useAuth();
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [branches, setBranches] = useState([]);
  const [filters, setFilters] = useState({ branchId: '', groupType: '', from: '', to: todayLocal() });
  const [drill, setDrill] = useState(null);
  const [rows, setRows] = useState([]);
  const [busy, setBusy] = useState(false);

  const load = async () => {
    try {
      const qs = new URLSearchParams(Object.entries(filters).filter(([, v]) => v)).toString();
      setData(await get('/dashboard' + (qs ? `?${qs}` : ''))); setError(null);
    } catch (err) { setError(err); }
  };
  useEffect(() => { load(); }, [filters]);
  useEffect(() => { get('/branches').then((r) => setBranches(r.branches)).catch(() => {}); }, []);

  const openDrill = async (card) => {
    setDrill(card); setRows([]);
    try { const qs = new URLSearchParams({ card, ...Object.fromEntries(Object.entries(filters).filter(([, v]) => v)) }).toString(); const r = await get('/dashboard/drill?' + qs); setRows(r.rows); }
    catch (err) { toast(err.message, 'error'); }
  };
  const exportCard = async (card) => {
    setBusy(true);
    try {
      await runSensitive(async () => {
        const qs = new URLSearchParams({ card, ...Object.fromEntries(Object.entries(filters).filter(([, v]) => v)) }).toString();
        const res = await api('/dashboard/export?' + qs, { raw: true });
        const text = await res.text();
        csvDownload(text, `pamoja-${card}-${todayLocal()}.csv`);
      }, 'export');
      toast('Export generated and audited.');
    } catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };

  if (!data && !error) return <Loading rows={4} />;
  const c = data?.cards;
  const meta = data?.meta;
  return (
    <div>
      <PageHead title="Dashboard" sub={`Cut-off ${date(meta?.cutoff)} · last refreshed ${dateTime(meta?.lastRefreshed)} · reconciliation ${meta?.reconciliationStatus}`} />
      <div className="toolbar">
        <Field label="Branch"><select className="input" value={filters.branchId} onChange={(e) => setFilters((f) => ({ ...f, branchId: e.target.value }))}><option value="">All branches in my scope</option>{branches.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}</select></Field>
        <Field label="Group type"><select className="input" value={filters.groupType} onChange={(e) => setFilters((f) => ({ ...f, groupType: e.target.value }))}><option value="">All types</option>{TYPES.map((t) => <option key={t}>{t}</option>)}</select></Field>
        <Field label="From"><input className="input" type="date" value={filters.from} onChange={(e) => setFilters((f) => ({ ...f, from: e.target.value }))} /></Field>
        <Field label="To"><input className="input" type="date" value={filters.to} onChange={(e) => setFilters((f) => ({ ...f, to: e.target.value }))} /></Field>
      </div>
      <ErrorBox error={error} />
      {c && <>
        <div className="grid grid-4">
          <button className="stat blue" style={{ textAlign: 'left', cursor: 'pointer' }} onClick={() => openDrill('groups')}><div className="stat-label">Total groups</div><div className="stat-value">{c.groups.total}</div><div className="stat-note">{Object.entries(c.groups.statusBreakdown).map(([k, v]) => `${k} ${v}`).join(' · ') || 'No groups'}</div></button>
          <button className="stat green" style={{ textAlign: 'left', cursor: 'pointer' }} onClick={() => openDrill('members')}><div className="stat-label">Active people</div><div className="stat-value">{c.members.people}</div><div className="stat-note">{c.members.memberships} memberships · {c.members.gender.map((g) => `${g.gender} ${g.n}`).join(' · ')}</div></button>
          <button className="stat" style={{ textAlign: 'left', cursor: 'pointer' }} onClick={() => openDrill('loansAdvanced')}><div className="stat-label">Loans advanced</div><div className="stat-value">{c.loansAdvanced.count}</div><div className="stat-note">{money(c.loansAdvanced.value)} from confirmed disbursements only</div></button>
          <div className="stat warn"><div className="stat-label">Outstanding principal</div><div className="stat-value">{money(c.outstandingPrincipal)}</div><div className="stat-note">From the ledger adapter at the as-of date</div></div>
        </div>
        <div className="grid grid-4 mt-2">
          <div className="stat"><div className="stat-label">Total payable</div><div className="stat-value" style={{ fontSize: '1.1rem' }}>{money(c.totalPayable.principal)}</div><div className="stat-note">Principal {money(c.totalPayable.principal)} · interest {money(c.totalPayable.interest)} · fees {money(c.totalPayable.fees)}</div></div>
          <button className="stat danger" style={{ textAlign: 'left', cursor: 'pointer' }} onClick={() => openDrill('arrears')}><div className="stat-label">Arrears and collections</div><div className="stat-value" style={{ fontSize: '1.1rem' }}>{money(c.arrears.overdue)}</div><div className="stat-note">Due {money(c.arrears.due)} · verified receipts {money(c.arrears.verifiedReceipts)} · unallocated {money(c.arrears.unallocated)} · posting failures {c.arrears.postingFailures}</div></button>
          <button className="stat" style={{ textAlign: 'left', cursor: 'pointer' }} onClick={() => openDrill('flows')}><div className="stat-label">Submitted</div><div className="stat-value" style={{ fontSize: '1.1rem' }}>{money(c.flows.submitted.value)}</div><div className="stat-note">{c.flows.submitted.count} submissions</div></button>
          <button className="stat" style={{ textAlign: 'left', cursor: 'pointer' }} onClick={() => openDrill('flows')}><div className="stat-label">Verified / Posted</div><div className="stat-value" style={{ fontSize: '1.1rem' }}>{money(c.flows.verified.value)}</div><div className="stat-note">Verified {money(c.flows.verified.value)} · Posted {money(c.flows.posted.value)} ({c.flows.posted.count})</div></button>
        </div>

        <Card title="Top ten groups" sub="Internal operational ranking — not a credit score, public endorsement or loan approval." className="mt-2" actions={<button className="btn btn-sm btn-secondary" disabled={busy} onClick={() => exportCard('groups')}>Export groups CSV</button>}>
          {c.topTen.length === 0 ? <Empty title="No eligible groups"><p>Fewer than ten groups may qualify; only eligible groups are ranked.</p></Empty> : <div className="table-wrap"><table><thead><tr><th>#</th><th>Group</th><th className="right">Score</th><th className="right">Attendance</th><th className="right">Loans</th><th>Flags</th></tr></thead><tbody>{c.topTen.map((r, i) => <tr key={r.groupId}><td>{i + 1}</td><td>{r.groupName}</td><td className="right">{r.score}</td><td className="right">{pct(r.metrics.attendancePercent)}</td><td className="right">{r.inputs.disbursedLoans}</td><td>{r.flags.restructured ? 'Restructured ' : ''}{r.flags.materialDisputes ? 'Dispute' : ''}{!r.flags.restructured && !r.flags.materialDisputes ? '—' : ''}</td></tr>)}</tbody></table></div>}
        </Card>
      </>}

      <Modal open={!!drill} onClose={() => setDrill(null)} title={`Drill-down · ${drill}`} wide>
        {rows.length === 0 ? <Loading rows={2} /> : <div className="table-wrap"><table><thead><tr>{Object.keys(rows[0]).map((k) => <th key={k}>{k.replace(/_/g, ' ')}</th>)}</tr></thead><tbody>{rows.map((r, i) => <tr key={i}>{Object.entries(r).map(([k, v]) => <td key={k} className={k.includes('amount') || k.includes('value') ? 'right amount' : ''}>{k.includes('amount') || k.includes('value') ? money(v) : String(v ?? '—')}</td>)}</tr>)}</tbody></table></div>}
        <div className="row end mt-2"><button className="btn btn-secondary" onClick={() => exportCard(drill)} disabled={busy}>Export CSV (audited)</button></div>
      </Modal>
    </div>
  );
}
