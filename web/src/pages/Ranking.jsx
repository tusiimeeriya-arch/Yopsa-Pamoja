import React, { useEffect, useState } from 'react';
import { get, post } from '../api.js';
import { useAuth } from '../auth.jsx';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, toast } from '../components.jsx';
import { date, pct } from '../fmt.js';

export default function Ranking() {
  const { runSensitive } = useAuth();
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);
  const [policies, setPolicies] = useState([]);
  const [modal, setModal] = useState(null);
  const [form, setForm] = useState({ repayment: 50, arrears: 25, attendance: 15, records: 10, minLoans: 1, minMeetings: 4, overdueExclusionDays: 30 });
  const [snap, setSnap] = useState(null);

  const load = async () => {
    try { setData(await get('/ranking')); setError(null); } catch (err) { setError(err); }
    try { const p = await get('/ranking/policies'); setPolicies(p.policies); } catch { /* not permitted */ }
  };
  useEffect(() => { load(); }, []);

  const takeSnapshot = async () => {
    setBusy(true);
    try {
      const r = await runSensitive(() => post('/ranking/snapshot', { cutoff: data.window.to }), 'snapshot');
      setSnap(r.snapshot); toast('Ranking snapshot saved.'); load();
    } catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };
  const propose = async (e) => {
    e.preventDefault(); setBusy(true);
    try { await post('/ranking/policies', { weights: { repayment: Number(form.repayment), arrears: Number(form.arrears), attendance: Number(form.attendance), records: Number(form.records) }, minLoans: Number(form.minLoans), minMeetings: Number(form.minMeetings), overdueExclusionDays: Number(form.overdueExclusionDays) }); toast('Policy version proposed. A different manager must approve it.'); setModal(null); load(); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };
  const approve = async (id) => {
    setBusy(true);
    try { await runSensitive(() => post(`/ranking/policies/${id}/approve`, {}), 'policy'); toast('Policy version approved.'); load(); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };

  if (!data && !error) return <Loading rows={4} />;
  return (
    <div>
      <PageHead title="Operational ranking" sub={`${data?.label} · window ${date(data?.window.from)} to ${date(data?.window.to)} · policy v${data?.policy.version}`} actions={<button className="btn" disabled={busy} onClick={takeSnapshot}>Save snapshot</button>} />
      <ErrorBox error={error} />
      {snap && <div className="alert success">Snapshot {snap.id} saved for cut-off {date(snap.cutoff)}.</div>}
      <Card title={`Eligible groups (${data?.eligibleCount})`} sub="Fewer than ten are shown when fewer qualify. Groups with a loan more than 30 days overdue are excluded.">
        {data?.top?.length ? <div className="table-wrap"><table><thead><tr><th>#</th><th>Group</th><th className="right">Score</th><th className="right">Repayment</th><th className="right">Arrears</th><th className="right">Attendance</th><th className="right">Records</th><th>Flags</th></tr></thead><tbody>{data.top.map((r, i) => <tr key={r.groupId}><td>{i + 1}</td><td><strong>{r.groupName}</strong><div className="small muted">{r.groupType} · {r.inputs.disbursedLoans} disbursed loan(s)</div></td><td className="right strong">{r.score}</td><td className="right">{r.components.repayment}</td><td className="right">{r.components.arrears}</td><td className="right">{r.components.attendance}</td><td className="right">{r.components.records}</td><td>{r.flags.restructured ? 'Restructured ' : ''}{r.flags.materialDisputes ? 'Material dispute' : ''}{!r.flags.restructured && !r.flags.materialDisputes ? '—' : ''}</td></tr>)}</tbody></table></div> : <Empty title="No eligible groups" />}
      </Card>
      <Card title="All active groups and why they are excluded" className="mt-2">
        <div className="table-wrap"><table><thead><tr><th>Group</th><th className="right">Score</th><th>Eligible</th><th>Reason</th><th className="right">Overdue days</th><th className="right">Attendance</th><th className="right">Records</th></tr></thead><tbody>{data?.groups?.map((r) => <tr key={r.groupId}><td>{r.groupName}</td><td className="right">{r.score}</td><td>{r.eligible ? <StatusChip status="approved" /> : <StatusChip status="returned" />}</td><td>{r.exclusionReason || '—'}</td><td className="right">{r.metrics.maxOverdue}</td><td className="right">{pct(r.metrics.attendancePercent)}</td><td className="right">{r.inputs.meetingsWithMinutes}/{r.inputs.closedMeetings}</td></tr>)}</tbody></table></div>
      </Card>
      <Card title="Ranking policy versions" sub="Management approves a version; its author cannot approve it." className="mt-2" actions={<button className="btn btn-sm" onClick={() => setModal('propose')}>Propose version</button>}>
        {policies.length === 0 ? <p className="muted small">Policy versions are visible to management, auditors and credit.</p> : <div className="table-wrap"><table><thead><tr><th>Version</th><th>Weights</th><th>Min loans / meetings</th><th>Overdue exclusion</th><th>Status</th><th /></tr></thead><tbody>{policies.map((p) => <tr key={p.id}><td>v{p.version}</td><td>{Object.entries(p.weights).map(([k, v]) => `${k} ${v}`).join(' · ')}</td><td>{p.min_loans} / {p.min_meetings}</td><td>{p.overdue_exclusion_days} days</td><td><StatusChip status={p.status} /></td><td>{p.status === 'proposed' && <button className="link-btn" disabled={busy} onClick={() => approve(p.id)}>Approve</button>}</td></tr>)}</tbody></table></div>}
      </Card>

      <Modal open={modal === 'propose'} onClose={() => setModal(null)} title="Propose a ranking policy version">
        <form onSubmit={propose}>
          <div className="form-grid"><Field label="Repayment"><input className="input" type="number" value={form.repayment} onChange={(e) => setForm((f) => ({ ...f, repayment: e.target.value }))} /></Field><Field label="Arrears"><input className="input" type="number" value={form.arrears} onChange={(e) => setForm((f) => ({ ...f, arrears: e.target.value }))} /></Field><Field label="Attendance"><input className="input" type="number" value={form.attendance} onChange={(e) => setForm((f) => ({ ...f, attendance: e.target.value }))} /></Field><Field label="Records"><input className="input" type="number" value={form.records} onChange={(e) => setForm((f) => ({ ...f, records: e.target.value }))} /></Field><Field label="Minimum loans"><input className="input" type="number" value={form.minLoans} onChange={(e) => setForm((f) => ({ ...f, minLoans: e.target.value }))} /></Field><Field label="Minimum meetings"><input className="input" type="number" value={form.minMeetings} onChange={(e) => setForm((f) => ({ ...f, minMeetings: e.target.value }))} /></Field></div>
          <div className="alert info small">Weights must add up to 100. The author cannot approve the version.</div>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy}>Propose</button></div>
        </form>
      </Modal>
    </div>
  );
}
