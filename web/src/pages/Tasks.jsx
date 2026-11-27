import React, { useEffect, useState } from 'react';
import { get, post } from '../api.js';
import { useAuth } from '../auth.jsx';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, toast } from '../components.jsx';
import { date, dateTime } from '../fmt.js';

export default function Tasks() {
  const { isAdmin } = useAuth();
  const [data, setData] = useState(null);
  const [notifications, setNotifications] = useState([]);
  const [users, setUsers] = useState([]);
  const [error, setError] = useState(null);
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState({ title: '', ownerUserId: '', deadline: '', linkedEntity: '', linkedId: '' });
  const [busy, setBusy] = useState(false);

  const load = async () => {
    try { setData(await get('/tasks')); setError(null); } catch (err) { setError(err); }
    try { setNotifications((await get('/me/notifications')).notifications); } catch { /* ignore */ }
    try { setUsers((await get('/directory/users')).users); } catch { /* not permitted */ }
  };
  useEffect(() => { load(); }, []);

  const complete = async (id) => { try { await post(`/tasks/${id}/complete`, {}); toast('Task completed.'); load(); } catch (err) { toast(err.message, 'error'); } };
  const escalate = async () => { setBusy(true); try { const r = await post('/jobs/escalate', {}); toast(`Follow-up sweep ran: ${JSON.stringify(r.result)}`); load(); } catch (err) { toast(err.message, 'error'); } finally { setBusy(false); } };
  const create = async (e) => {
    e.preventDefault(); setBusy(true);
    try { await post('/tasks', form); toast('Task created with owner and deadline.'); setOpen(false); load(); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };

  if (!data && !error) return <Loading rows={3} />;
  return (
    <div>
      <PageHead title="Tasks and notifications" sub="Every alert has an owner and a deadline. Escalation is idempotent." actions={<>{data?.canCreate && <button className="btn" onClick={() => setOpen(true)}>New task</button>}{isAdmin && <button className="btn btn-secondary" onClick={escalate} disabled={busy}>Run follow-up sweep</button>}</>} />
      <ErrorBox error={error} />
      <Card title="Tasks">
        {data?.tasks?.length ? <div className="table-wrap"><table><thead><tr><th>Task</th><th>Owner</th><th>Deadline</th><th>Status</th><th /></tr></thead><tbody>{data.tasks.map((t) => <tr key={t.id}><td>{t.title}</td><td>{t.owner_name}</td><td>{date(t.deadline)}{t.deadline < new Date().toISOString().slice(0, 10) && t.status === 'open' ? <div className="small error-text">Overdue</div> : null}</td><td><StatusChip status={t.status} /></td><td>{t.status === 'open' && <button className="link-btn" onClick={() => complete(t.id)}>Complete</button>}</td></tr>)}</tbody></table></div> : <Empty title="No tasks" />}
      </Card>
      <Card title="Notifications" className="mt-2">
        {notifications.length ? notifications.map((n) => <div className="alert info" key={n.id}><div className="row between"><strong>{n.title}</strong><span className="small muted">{dateTime(n.created_at)}</span></div><div className="small">{n.body} · deadline {date(n.deadline)}</div></div>) : <Empty title="No notifications" />}
      </Card>
      <Modal open={open} onClose={() => setOpen(false)} title="Create a task">
        <form onSubmit={create}>
          <Field label="Task"><input className="input" value={form.title} onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))} required /></Field>
          <Field label="Owner"><select className="input" value={form.ownerUserId} onChange={(e) => setForm((f) => ({ ...f, ownerUserId: e.target.value }))} required><option value="">Select owner</option>{users.map((u) => <option key={u.id} value={u.id}>{u.full_name} ({u.roles.join(', ') || 'no role'})</option>)}</select></Field>
          <Field label="Deadline"><input className="input" type="date" value={form.deadline} onChange={(e) => setForm((f) => ({ ...f, deadline: e.target.value }))} required /></Field>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setOpen(false)}>Cancel</button><button className="btn" disabled={busy || !form.title || !form.ownerUserId || !form.deadline}>Create task</button></div>
        </form>
      </Modal>
    </div>
  );
}
