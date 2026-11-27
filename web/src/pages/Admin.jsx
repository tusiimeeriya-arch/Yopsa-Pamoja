import React, { useEffect, useState } from 'react';
import { Link, NavLink, useParams } from 'react-router-dom';
import { get, post } from '../api.js';
import { useAuth } from '../auth.jsx';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, toast } from '../components.jsx';
import { date, dateTime, money } from '../fmt.js';

const TABS = [
  { id: '', label: 'Overview' }, { id: 'roles', label: 'Roles' }, { id: 'users', label: 'Users' }, { id: 'policies', label: 'Policies' },
  { id: 'audit', label: 'Audit log' }, { id: 'checks', label: 'System checks' }, { id: 'build-progress', label: 'Build progress' },
  { id: 'security', label: 'Security' }, { id: 'jobs', label: 'Scheduled jobs' }, { id: 'assumptions', label: 'About and assumptions' }, { id: 'demo', label: 'Demo guide' }, { id: 'exceptions', label: 'Exceptions' },
];

const ROLES = ['member', 'secretary', 'treasurer', 'chairperson', 'field_officer', 'verifier', 'credit_approver', 'management', 'auditor', 'it_admin'];

function statusChip(s) {
  if (s === 'BUILT AND VERIFIED') return <span className="chip verified">BUILT AND VERIFIED</span>;
  if (s === 'EXCEPTION') return <span className="chip pending">EXCEPTION</span>;
  if (s === 'NOT VERIFIED HERE') return <span className="chip pending">NOT VERIFIED HERE</span>;
  if (s === 'PARTIAL') return <span className="chip pending">PARTIAL</span>;
  if (s === 'BUILT BUT FAILING') return <span className="chip disputed">BUILT BUT FAILING</span>;
  return <span className="chip returned">NOT BUILT</span>;
}

export default function Admin() {
  const params = useParams();
  const tab = params['*'] || '';
  const { runSensitive, isAdmin } = useAuth();
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(true);
  const [roles, setRoles] = useState(null);
  const [users, setUsers] = useState([]);
  const [branches, setBranches] = useState([]);
  const [groups, setGroups] = useState([]);
  const [policies, setPolicies] = useState(null);
  const [audit, setAudit] = useState([]);
  const [integrity, setIntegrity] = useState(null);
  const [checks, setChecks] = useState(null);
  const [progress, setProgress] = useState(null);
  const [assumptions, setAssumptions] = useState(null);
  const [security, setSecurity] = useState(null);
  const [demo, setDemo] = useState(null);
  const [exceptions, setExceptions] = useState([]);
  const [jobs, setJobs] = useState(null);
  const [modal, setModal] = useState(null);
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState({ userId: '', role: 'member', scopeType: 'Org', scopeId: 'org' });
  const [policyEdit, setPolicyEdit] = useState({ key: '', value: '' });
  const [confirmText, setConfirmText] = useState('');

  const loadTab = async () => {
    setLoading(true); setError(null);
    try {
      if (tab === 'roles') {
        const r = await get('/admin/role-assignments'); setRoles(r);
        const u = await get('/admin/users'); setUsers(u.users);
        const b = await get('/branches'); setBranches(b.branches);
        const g = await get('/groups'); setGroups(g.groups);
      } else if (tab === 'users') setUsers((await get('/admin/users')).users);
      else if (tab === 'policies') setPolicies(await get('/admin/policies'));
      else if (tab === 'audit') { setAudit((await get('/admin/audit')).entries); setIntegrity(null); }
      else if (tab === 'build-progress') setProgress(await get('/admin/build-progress'));
      else if (tab === 'security') setSecurity(await get('/admin/security'));
      else if (tab === 'assumptions') setAssumptions(await get('/admin/assumptions'));
      else if (tab === 'demo') setDemo(await get('/admin/demo-guide'));
      else if (tab === 'exceptions') setExceptions((await get('/admin/exceptions')).exceptions);
      else if (tab === 'jobs') setJobs(await get('/admin/jobs'));
    } catch (err) { setError(err); }
    finally { setLoading(false); }
  };
  useEffect(() => { loadTab(); }, [tab]);

  const runChecks = async () => {
    setBusy(true);
    try { setChecks(await post('/admin/system-checks', {})); toast('System checks finished.'); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };
  const propose = async (e) => {
    e.preventDefault(); setBusy(true);
    try { await runSensitive(() => post('/admin/role-assignments', form), 'role_change'); toast('Role proposed. A second administrator must approve.'); setModal(null); loadTab(); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };
  const approve = async (id) => { setBusy(true); try { await runSensitive(() => post(`/admin/role-assignments/${id}/approve`, {}), 'role_change'); toast('Role approved.'); loadTab(); } catch (err) { toast(err.message, 'error'); } finally { setBusy(false); } };
  const revoke = async (id) => { setBusy(true); try { await runSensitive(() => post(`/admin/role-assignments/${id}/revoke`, { reason: 'Revoked by administrator' }), 'role_change'); toast('Role revoked.'); loadTab(); } catch (err) { toast(err.message, 'error'); } finally { setBusy(false); } };
  const savePolicy = async (e) => {
    e.preventDefault(); setBusy(true);
    try { const value = JSON.parse(policyEdit.value); await runSensitive(() => post('/admin/policies', { key: policyEdit.key, value }), 'policy'); toast('Policy version created.'); setModal(null); loadTab(); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };
  const checkIntegrity = async () => { setBusy(true); try { setIntegrity(await get('/admin/audit/integrity')); } catch (err) { toast(err.message, 'error'); } finally { setBusy(false); } };
  const purge = async () => {
    setBusy(true);
    try { await runSensitive(() => post('/admin/purge-demo', { confirm: confirmText }), 'purge'); toast('Demo data purged. Audit rows retained.'); setConfirmText(''); loadTab(); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };
  const resolveException = async (id) => {
    const reason = window.prompt('Resolution note');
    if (!reason) return;
    try { await post(`/admin/exceptions/${id}/resolve`, { reason }); toast('Exception resolved.'); loadTab(); } catch (err) { toast(err.message, 'error'); }
  };

  if (!isAdmin) return <ErrorBox error={{ message: 'Administrator access is required.' }} />;
  if (loading && !progress) return <Loading rows={4} />;
  return (
    <div>
      <PageHead title="Administration" sub="Roles, audit, policy, checks and the build record. IT Admin has no financial verify, approve or post rights." />
      <div className="tabs" style={{ flexWrap: 'wrap' }}>{TABS.map((t) => <NavLink key={t.id} to={`/admin${t.id ? '/' + t.id : ''}`} end className={({ isActive }) => `tab ${isActive ? 'active' : ''}`}>{t.label}</NavLink>)}</div>
      <ErrorBox error={error} />

      {tab === '' && (
        <div className="grid grid-2">
          <Card title="Controls"><div className="row"><Link className="btn btn-secondary" to="/admin/build-progress">Build progress</Link><Link className="btn btn-secondary" to="/admin/checks">Run system checks</Link><Link className="btn btn-secondary" to="/admin/audit">Audit log</Link><Link className="btn btn-secondary" to="/admin/demo">Demo guide</Link></div></Card>
          <Card title="Security posture"><ul className="small"><li>Default deny; every sensitive write is a backend function.</li><li>Roles and scopes live only in RoleAssignment records.</li><li>Separation of duties: no self-verification or self-approval.</li><li>Append-only, hash-chained audit log.</li><li>Uploads are private with short-lived signed links.</li></ul></Card>
        </div>
      )}

      {tab === 'roles' && roles && (
        <>
          <Card title="Role assignments" sub="IT Admin proposes; a second administrator approves. Both are audited." actions={roles.canPropose && <button className="btn btn-sm" onClick={() => setModal('role')}>Propose role</button>}>
            {roles.assignments.length === 0 ? <Empty title="No role assignments" /> : <div className="table-wrap"><table><thead><tr><th>User</th><th>Role</th><th>Scope</th><th>Effective</th><th>Status</th><th>Proposed / approved by</th><th /></tr></thead><tbody>{roles.assignments.map((a) => <tr key={a.id}><td>{a.user_name}<div className="small muted">{a.email}</div></td><td>{a.role}</td><td>{a.scope_type}{a.scope_type !== 'Org' ? ` · ${a.scope_id.slice(0, 8)}…` : ''}</td><td>{date(a.effective_from)} – {a.effective_to ? date(a.effective_to) : 'open'}</td><td><StatusChip status={a.status} /></td><td className="small">{a.granted_by_name || '—'} / {a.approved_by_name || '—'}</td><td className="nowrap">{a.status === 'pending' && <button className="link-btn" disabled={busy} onClick={() => approve(a.id)}>Approve</button>}{a.status === 'active' && <button className="link-btn" disabled={busy} onClick={() => revoke(a.id)}>Revoke</button>}</td></tr>)}</tbody></table></div>}
          </Card>
          <Modal open={modal === 'role'} onClose={() => setModal(null)} title="Propose a role assignment">
            <form onSubmit={propose}>
              <Field label="User"><select className="input" value={form.userId} onChange={(e) => setForm((f) => ({ ...f, userId: e.target.value }))} required><option value="">Select user</option>{users.map((u) => <option key={u.id} value={u.id}>{u.full_name} ({u.email})</option>)}</select></Field>
              <Field label="Role"><select className="input" value={form.role} onChange={(e) => setForm((f) => ({ ...f, role: e.target.value }))}>{ROLES.map((r) => <option key={r}>{r}</option>)}</select></Field>
              <Field label="Scope"><select className="input" value={form.scopeType} onChange={(e) => setForm((f) => ({ ...f, scopeType: e.target.value, scopeId: e.target.value === 'Org' ? 'org' : '' }))}><option value="Org">Organisation</option><option value="Branch">Branch</option><option value="Group">Group</option></select></Field>
              {form.scopeType === 'Branch' && <Field label="Branch"><select className="input" value={form.scopeId} onChange={(e) => setForm((f) => ({ ...f, scopeId: e.target.value }))} required><option value="">Select branch</option>{branches.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}</select></Field>}
              {form.scopeType === 'Group' && <Field label="Group"><select className="input" value={form.scopeId} onChange={(e) => setForm((f) => ({ ...f, scopeId: e.target.value }))} required><option value="">Select group</option>{groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}</select></Field>}
              <div className="alert warn small">You will confirm with a one-time code. A second administrator approves. You cannot approve your own role grant.</div>
              <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy || !form.userId || !form.scopeId}>Propose</button></div>
            </form>
          </Modal>
        </>
      )}

      {tab === 'users' && (
        <Card title="Users"><div className="table-wrap"><table><thead><tr><th>Name</th><th>Email</th><th>Status</th><th>Roles</th></tr></thead><tbody>{users.map((u) => <tr key={u.id}><td>{u.full_name}{u.is_demo && <span className="pill" style={{ marginLeft: 6 }}>demo</span>}</td><td>{u.email}</td><td><StatusChip status={u.status} /></td><td className="small">{u.roles.map((r) => `${r.role} (${r.scope_type})`).join(', ') || '—'}</td></tr>)}</tbody></table></div></Card>
      )}

      {tab === 'policies' && policies && (
        <Card title="Versioned policy values" sub="Nothing sensitive is hard-coded. Each change creates a new version.">
          <div className="table-wrap"><table><thead><tr><th>Key</th><th>Value</th><th>Version</th><th>Status</th><th>Effective</th><th /></tr></thead><tbody>{policies.policies.map((p) => <tr key={p.id}><td>{p.key}</td><td className="small"><code>{JSON.stringify(p.value)}</code></td><td>{p.version}</td><td><StatusChip status={p.status} /></td><td>{date(p.effective_from)}</td><td>{policies.canEdit && <button className="link-btn" onClick={() => { setPolicyEdit({ key: p.key, value: JSON.stringify(p.value, null, 2) }); setModal('policy'); }}>New version</button>}</td></tr>)}</tbody></table></div>
          <Modal open={modal === 'policy'} onClose={() => setModal(null)} title={`New version · ${policyEdit.key}`}>
            <form onSubmit={savePolicy}><Field label="Value (JSON)"><textarea className="input" rows={8} value={policyEdit.value} onChange={(e) => setPolicyEdit((p) => ({ ...p, value: e.target.value }))} /></Field><div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy}>Save version</button></div></form>
          </Modal>
        </Card>
      )}

      {tab === 'audit' && (
        <>
          <Card title="Audit log" sub="Append-only. Contains ids and operational values, not personal names." actions={<button className="btn btn-sm" onClick={checkIntegrity} disabled={busy}>Run integrity check</button>}>
            {integrity && <div className={`alert ${integrity.ok ? 'success' : 'danger'}`}>{integrity.ok ? `Hash chain intact: ${integrity.entries} entries verified.` : `Chain broken at sequence ${integrity.firstBreakAt}.`}</div>}
            <div className="table-wrap"><table><thead><tr><th>Seq</th><th>When</th><th>Actor</th><th>Action</th><th>Entity</th><th>Reason</th><th>Hash</th></tr></thead><tbody>{audit.map((a) => <tr key={a.seq}><td>{a.seq}</td><td>{dateTime(a.at)}</td><td>{a.actor_name || a.actor_user_id?.slice(0, 8) || '—'}</td><td>{a.action}</td><td>{a.entity}{a.entity_id ? ` · ${String(a.entity_id).slice(0, 8)}…` : ''}</td><td className="small">{a.reason || '—'}</td><td className="small"><code>{a.hash.slice(0, 10)}…</code></td></tr>)}</tbody></table></div>
          </Card>
        </>
      )}

      {tab === 'checks' && (
        <Card title="System checks" sub="Runs marked test data inside the app, asserts each security rule, then removes the test rows. Audit entries stay." actions={<button className="btn" onClick={runChecks} disabled={busy}>{busy ? 'Running…' : 'Run all checks'}</button>}>
          {!checks ? <p className="muted small">No run yet in this session.</p> : <>
            <div className={`alert ${checks.failed ? 'danger' : 'success'}`}>{checks.passed} passed, {checks.failed} failed, {checks.total} checks · {dateTime(checks.ranAt)}</div>
            <div className="table-wrap"><table><thead><tr><th>Check</th><th>Result</th><th>Detail</th></tr></thead><tbody>{checks.results.map((r) => <tr key={r.id}><td>{r.name}</td><td>{r.status === 'PASS' ? <span className="chip verified">PASS</span> : <span className="chip disputed">FAIL</span>}</td><td className="small">{r.detail}</td></tr>)}</tbody></table></div>
          </>}
        </Card>
      )}

      {tab === 'build-progress' && progress && (
        <>
          <div className="grid grid-4 mb-2">{Object.entries(progress.counts).map(([k, v]) => <div className="stat" key={k}><div className="stat-label">{k}</div><div className="stat-value">{v}</div></div>)}</div>
          <div className="alert warn small"><strong>Exceptions</strong><ul>{progress.exceptions.map((e) => <li key={e}>{e}</li>)}</ul></div>
          {progress.stages.map((s) => <Card key={s.stage} title={`${s.stage} · ${s.title}`} className="mt-2"><div className="table-wrap"><table><thead><tr><th style={{ width: 80 }}>Item</th><th>Requirement</th><th style={{ width: 190 }}>Status</th><th>Note / next step</th></tr></thead><tbody>{s.items.map((i) => <tr key={i.id}><td>{i.id}</td><td>{i.name}</td><td>{statusChip(i.status)}</td><td className="small">{i.note || '—'}{i.nextStep ? <div className="muted">Next: {i.nextStep}</div> : null}</td></tr>)}</tbody></table></div></Card>)}
        </>
      )}

      {tab === 'security' && security && (
        <>
          <Card title="Security model" sub={security.browserWritePolicy}><ul className="small">{security.principles.map((p) => <li key={p}>{p}</li>)}</ul></Card>
          <Card title="Every entity and its rules" className="mt-2"><div className="table-wrap"><table><thead><tr><th>Entity</th><th>Row-level rule</th><th>Field-level rule</th><th>Browser writes</th></tr></thead><tbody>{security.entities.map((e) => <tr key={e[0]}><td className="strong">{e[0]}</td><td className="small">{e[1]}</td><td className="small">{e[2]}</td><td><span className="chip verified">{e[3]}</span></td></tr>)}</tbody></table></div></Card>
          <Card title="Every backend function" className="mt-2"><div className="table-wrap"><table><thead><tr><th>Function</th><th>Allowed callers</th><th>Scope</th><th>Separation of duties</th><th>Idempotency</th><th>Audit</th><th>Validation</th></tr></thead><tbody>{security.functions.map((f) => <tr key={f[0]}><td className="strong">{f[0]}</td><td className="small">{f[1]}</td><td className="small">{f[2]}</td><td className="small">{f[3]}</td><td className="small">{f[4]}</td><td className="small">{f[5]}</td><td className="small">{f[6]}</td></tr>)}</tbody></table></div></Card>
        </>
      )}

      {tab === 'assumptions' && assumptions && (
        <>
          <Card title="About Pamoja"><p>{assumptions.product.name} · {assumptions.product.org}</p><p className="small muted">{assumptions.product.tagline}. {assumptions.product.scope}</p></Card>
          <Card title="Assumptions and values awaiting approval" className="mt-2"><div className="table-wrap"><table><thead><tr><th>ID</th><th>Assumption</th><th>Value</th><th>Status</th></tr></thead><tbody>{assumptions.assumptions.map((a) => <tr key={a.id}><td>{a.id}</td><td>{a.title}</td><td className="small">{a.value}</td><td><span className="chip pending">{a.status}</span></td></tr>)}</tbody></table></div></Card>
          <Card title="Approvals required" className="mt-2"><ul className="small">{assumptions.approvals.map((a) => <li key={a.value}>{a.value} — {a.owner}</li>)}</ul></Card>
        </>
      )}

      {tab === 'demo' && demo && (
        <>
          <Card title="Demo guide" sub="Demo logins are shown here only, never on the login page.">
            <div className="alert warn">Demo password: <code>{demo.password}</code>. {demo.note}</div>
            <div className="table-wrap"><table><thead><tr><th>Name</th><th>Email</th><th>Roles</th></tr></thead><tbody>{demo.users.map((u) => <tr key={u.id}><td>{u.full_name}</td><td>{u.email}</td><td>{u.roles.join(', ') || '—'}</td></tr>)}</tbody></table></div>
          </Card>
          <Card title="Purge demo data" className="mt-2">
            <p className="small">Deletes only records tagged <code>isDemo</code>. AuditLog rows are never deleted. Demo accounts are disabled.</p>
            <div className="field" style={{ maxWidth: 360 }}><label className="label" htmlFor="confirm">Type PURGE DEMO DATA</label><input id="confirm" className="input" value={confirmText} onChange={(e) => setConfirmText(e.target.value)} /></div>
            <button className="btn btn-danger" disabled={busy || confirmText !== 'PURGE DEMO DATA' || !demo.demoExists} onClick={purge}>Purge demo data</button>
          </Card>
        </>
      )}

      {tab === 'jobs' && jobs && (
        <>
          <Card title="Scheduled jobs" sub="The ranking snapshot and task escalation run on a schedule and every run is recorded.">
            <div className="table-wrap"><table><thead><tr><th>Job</th><th className="right">Runs</th><th className="right">OK</th><th className="right">Failed</th><th>Last run</th></tr></thead><tbody>{jobs.summary.map((j) => <tr key={j.job}><td>{j.job}</td><td className="right">{j.runs}</td><td className="right">{j.ok}</td><td className="right">{j.failed}</td><td>{dateTime(j.last_run)}</td></tr>)}</tbody></table></div>
          </Card>
          <Card title="Recent runs" className="mt-2">
            <div className="table-wrap"><table><thead><tr><th>Job</th><th>Started</th><th>Finished</th><th>Status</th><th>Result</th></tr></thead><tbody>{jobs.runs.map((r) => <tr key={r.id}><td>{r.job}</td><td>{dateTime(r.started_at)}</td><td>{r.finished_at ? dateTime(r.finished_at) : '—'}</td><td><StatusChip status={r.status} /></td><td className="small"><code>{r.result ? JSON.stringify(r.result).slice(0, 160) : (r.error || '—')}</code></td></tr>)}</tbody></table></div>
          </Card>
        </>
      )}

      {tab === 'exceptions' && (
        <Card title="Finance exceptions"><div className="table-wrap"><table><thead><tr><th>Kind</th><th>Detail</th><th>Owner</th><th>Deadline</th><th>Status</th><th /></tr></thead><tbody>{exceptions.map((e) => <tr key={e.id}><td>{e.kind}</td><td>{e.detail}</td><td>{e.owner_name}</td><td>{date(e.deadline)}</td><td><StatusChip status={e.status} /></td><td>{e.status === 'open' && <button className="link-btn" onClick={() => resolveException(e.id)}>Resolve</button>}</td></tr>)}</tbody></table></div></Card>
      )}
    </div>
  );
}
