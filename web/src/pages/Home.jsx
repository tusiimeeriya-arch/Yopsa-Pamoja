import React, { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { get, post } from '../api.js';
import { useAuth } from '../auth.jsx';
import { Card, Empty, ErrorBox, Loading, Modal, PageHead, StatusChip, toast } from '../components.jsx';
import { date, dateTime, money, pct } from '../fmt.js';
import { listDrafts, removeDraft, updateDraft } from '../drafts.js';

export default function Home() {
  const { user, roles, isAdmin, demoMode, runSensitive } = useAuth();
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [drafts, setDrafts] = useState(() => listDrafts(user?.id));
  const [syncing, setSyncing] = useState(false);
  const [dispute, setDispute] = useState(null);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try { setData(await get('/me/home')); setError(null); } catch (err) { setError(err); }
  }, []);
  useEffect(() => { load(); const on = () => setDrafts(listDrafts(user?.id)); window.addEventListener('pamoja:drafts', on); return () => window.removeEventListener('pamoja:drafts', on); }, [load, user?.id]);

  const sync = async () => {
    setSyncing(true);
    for (const d of drafts.filter((x) => x.status === 'pending')) {
      try {
        if (d.type === 'payment_entry') await post('/payments/entries', { ...d.payload, idempotencyKey: d.idempotencyKey });
        else if (d.type === 'attendance') await post(`/meetings/${d.payload.meetingId}/check-in`, d.payload);
        else if (d.type === 'comment') await post('/comments', d.payload);
        removeDraft(user.id, d.id);
      } catch (err) {
        if (err.status === 409) updateDraft(user.id, d.id, { status: 'review', error: err.message });
        else updateDraft(user.id, d.id, { error: err.message });
      }
    }
    setDrafts(listDrafts(user.id));
    setSyncing(false);
    load();
    toast('Drafts checked. Anything still pending stays saved on this device.');
  };

  const raiseDispute = async () => {
    setBusy(true);
    try {
      await post('/disputes', { kind: 'payment', entityId: dispute.id, reason });
      setDispute(null); setReason(''); toast('Dispute sent to the verifier.'); load();
    } catch (err) { toast(err.message, 'error'); }
    finally { setBusy(false); }
  };

  if (!data && !error) return <Loading rows={4} />;
  const roleSet = new Set(roles.map((r) => r.role));
  return (
    <div>
      <PageHead title={`Hello, ${user.full_name.split(' ')[0]}`} sub={`${new Date().toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric', timeZone: 'Africa/Kampala' })} · ${roles.length ? roles.map((r) => r.role.replace(/_/g, ' ')).join(', ') : 'No role yet'}`} />
      <ErrorBox error={error} />
      {!roles.length && <div className="alert warn">Your account has no role yet. An administrator must grant one before you can use Pamoja.</div>}

      {drafts.length > 0 && (
        <Card title="Saved on this device" sub="Pending sync drafts. Verification, approval, posting and closing always need the server." actions={<button className="btn btn-sm" onClick={sync} disabled={syncing}>{syncing ? 'Syncing…' : 'Sync now'}</button>}>
          <div className="draft-list">
            {drafts.map((d) => (
              <div className="draft-item" key={d.id}>
                <div className="row between">
                  <span><span className="chip pending">Pending sync</span> {d.label || d.type}</span>
                  <button className="link-btn" onClick={() => removeDraft(user.id, d.id)}>Discard</button>
                </div>
                {d.status === 'review' && <div className="error-text mt-1">Needs review: {d.error}</div>}
                {d.error && d.status !== 'review' && <div className="error-text mt-1">{d.error}</div>}
              </div>
            ))}
          </div>
        </Card>
      )}

      {data?.person && (
        <>
          <div className="grid grid-3 mt-2">
            <div className="stat blue"><div className="stat-label">My attendance</div>{data.attendance.length ? data.attendance.map((a) => <div key={a.groupId} className="mt-1"><div className="stat-value">{pct(a.percent)}</div><div className="stat-note">{a.groupName} · {a.attended} of {a.expected}</div></div>) : <div className="stat-note mt-1">No attendance yet.</div>}</div>
            <div className="stat green"><div className="stat-label">Next meeting</div>{data.nextMeeting ? <><div className="stat-value" style={{ fontSize: '1.05rem' }}>{data.nextMeeting.title}</div><div className="stat-note">{data.nextMeeting.group_name}<br />{dateTime(data.nextMeeting.scheduled_for)}</div></> : <div className="stat-note mt-1">No upcoming meeting.</div>}</div>
            <div className="stat"><div className="stat-label">Open tasks</div><div className="stat-value">{data.counts.tasks}</div><div className="stat-note"><Link to="/tasks">View tasks and notifications</Link></div></div>
          </div>

          <Card title="My payments" sub="Each entry shows its own status. Pending entries never change a loan balance." className="mt-2">
            {data.payments.length === 0 ? <Empty title="No payments yet">Your treasurer's entries will appear here.</Empty> : (
              <div className="table-wrap"><table><thead><tr><th>Date</th><th>Group</th><th>Category</th><th className="right">Amount</th><th>Status</th><th /></tr></thead>
                <tbody>{data.payments.map((p) => <tr key={p.id}><td>{date(p.collection_date)}</td><td>{p.group_name}</td><td style={{ textTransform: 'capitalize' }}>{p.category.replace(/_/g, ' ')}</td><td className="right amount">{money(p.amount)}</td><td><StatusChip status={p.chip} /></td><td><button className="link-btn" onClick={() => setDispute(p)}>Dispute</button></td></tr>)}</tbody></table></div>
            )}
          </Card>
          <div className="row mt-2"><Link className="btn btn-secondary" to="/learning">Learning library</Link><Link className="btn btn-secondary" to="/disputes">My disputes</Link></div>
        </>
      )}

      {roleSet.has('treasurer') && (
        <Card title="Treasurer · draft entries" sub="Record entries, then lock them into one submission." className="mt-2" actions={<Link className="btn btn-sm" to="/payments?tab=entry">Record payment</Link>}>
          {data.queues.draftEntries?.length ? <div className="table-wrap"><table><thead><tr><th>Member</th><th>Group</th><th>Category</th><th className="right">Amount</th><th>Date</th></tr></thead><tbody>{data.queues.draftEntries.map((e) => <tr key={e.id}><td>{e.full_name}</td><td>{e.group_name}</td><td style={{ textTransform: 'capitalize' }}>{e.category.replace(/_/g, ' ')}</td><td className="right amount">{money(e.amount)}</td><td>{date(e.collection_date)}</td></tr>)}</tbody></table></div> : <Empty title="No draft entries" />}
        </Card>
      )}

      {roleSet.has('secretary') && (
        <Card title="Secretary · meetings" sub="Open the register, confirm it, then close the meeting." className="mt-2">
          {data.queues.myMeetings?.length ? <div className="table-wrap"><table><thead><tr><th>Meeting</th><th>Group</th><th>When</th><th>Status</th><th /></tr></thead><tbody>{data.queues.myMeetings.map((m) => <tr key={m.id}><td>{m.title}</td><td>{m.group_name}</td><td>{dateTime(m.scheduled_for)}</td><td><StatusChip status={m.status} /></td><td><Link className="link-btn" to={`/meetings/${m.id}`}>Open</Link></td></tr>)}</tbody></table></div> : <Empty title="No open meetings" />}
        </Card>
      )}

      {roleSet.has('field_officer') && (
        <Card title="Officer · groups awaiting review" className="mt-2">
          {data.queues.groups?.length ? <div className="table-wrap"><table><thead><tr><th>Group</th><th>Type</th><th>Status</th><th /></tr></thead><tbody>{data.queues.groups.map((g) => <tr key={g.id}><td>{g.name}</td><td>{g.type}</td><td><StatusChip status={g.status} /></td><td><Link className="link-btn" to={`/groups/${g.id}`}>Review</Link></td></tr>)}</tbody></table></div> : <Empty title="No groups awaiting review" />}
        </Card>
      )}

      {roleSet.has('verifier') && (
        <Card title="Verifier · queues" sub="Independent verification only. IT Admin never verifies or approves." className="mt-2">
          <div className="grid grid-2">
            <div>
              <h3>Submissions</h3>
              {data.queues.verification?.length ? <div className="table-wrap"><table><thead><tr><th>Group</th><th className="right">Submitted</th><th>Status</th></tr></thead><tbody>{data.queues.verification.map((s) => <tr key={s.id}><td>{s.group_name}</td><td className="right amount">{money(s.total_amount)}</td><td><StatusChip status={s.status} /></td></tr>)}</tbody></table></div> : <Empty title="Nothing to verify" />}
              <Link className="btn btn-sm mt-2" to="/payments?tab=verify">Open verification</Link>
            </div>
            <div>
              <h3>Posting exceptions</h3>
              {data.queues.exceptions?.length ? data.queues.exceptions.map((e) => <div className="alert warn" key={e.id}>{e.detail}<br /><span className="small">Owner deadline {date(e.deadline)}</span></div>) : <Empty title="No open exceptions" />}
            </div>
          </div>
        </Card>
      )}

      {roleSet.has('credit_approver') && (
        <Card title="Credit · loan queues" className="mt-2">
          {data.queues.loans?.length ? <div className="table-wrap"><table><thead><tr><th>Group</th><th className="right">Amount</th><th>Status</th><th /></tr></thead><tbody>{data.queues.loans.map((l) => <tr key={l.id}><td>{l.group_name}</td><td className="right amount">{money(l.amount)}</td><td><StatusChip status={l.status} /></td><td><Link className="link-btn" to={`/loans/${l.id}`}>Open</Link></td></tr>)}</tbody></table></div> : <Empty title="No loan applications waiting" />}
        </Card>
      )}

      {isAdmin && (
        <Card title="Administration" className="mt-2">
          <div className="row">
            <Link className="btn btn-secondary" to="/admin/build-progress">Build progress</Link>
            <Link className="btn btn-secondary" to="/admin/checks">System checks</Link>
            <Link className="btn btn-secondary" to="/admin/demo">Demo guide</Link>
            <Link className="btn btn-secondary" to="/admin/assumptions">About and assumptions</Link>
          </div>
          {demoMode && <p className="small muted mt-2">This workspace contains demo data. Disable all demo accounts before go-live.</p>}
        </Card>
      )}

      <Modal open={!!dispute} onClose={() => setDispute(null)} title="Dispute this payment entry">
        {dispute && <><p className="small muted">{money(dispute.amount)} · {dispute.group_name} · {date(dispute.collection_date)}</p>
          <div className="field"><label className="label" htmlFor="dispute-reason">What is wrong?</label><textarea id="dispute-reason" className="input" value={reason} onChange={(e) => setReason(e.target.value)} minLength={10} placeholder="Explain what happened. This is confidential to you, the assigned officer and the reviewer." /></div>
          <div className="row end"><button className="btn btn-secondary" onClick={() => setDispute(null)}>Cancel</button><button className="btn" disabled={busy || reason.trim().length < 10} onClick={raiseDispute}>Send dispute</button></div>
        </>}
      </Modal>
    </div>
  );
}
