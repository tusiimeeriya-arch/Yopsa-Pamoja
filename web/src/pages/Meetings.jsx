import React, { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { get, post } from '../api.js';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, toast } from '../components.jsx';
import { dateTime } from '../fmt.js';

export default function Meetings() {
  const [params] = useSearchParams();
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState({ groupId: params.get('group') || '', title: 'Weekly meeting', scheduledFor: '', locationText: 'Community hall' });
  const [busy, setBusy] = useState(false);

  const load = () => get('/meetings').then((r) => { setData(r); if (!form.groupId && r.groups[0]) setForm((f) => ({ ...f, groupId: r.groups[0].id })); }).catch(setError);
  useEffect(() => { load(); }, []);

  const create = async (e) => {
    e.preventDefault(); setBusy(true);
    try { const r = await post('/meetings', form); setOpen(false); toast('Meeting scheduled.'); window.location.assign(`/meetings/${r.meeting.id}`); }
    catch (err) { toast(err.message, 'error'); }
    finally { setBusy(false); }
  };

  if (!data && !error) return <Loading rows={4} />;
  return (
    <div>
      <PageHead title="Meetings" sub="Attendance requires an active membership on the meeting date. Closed registers change only through amendments." actions={<button className="btn" onClick={() => setOpen(true)} disabled={!data?.groups?.length}>Schedule meeting</button>} />
      <ErrorBox error={error} />
      <Card>
        {data?.meetings?.length ? <div className="table-wrap"><table><thead><tr><th>Meeting</th><th>Group</th><th>When</th><th>Status</th><th /></tr></thead><tbody>{data.meetings.map((m) => <tr key={m.id}><td>{m.title}</td><td>{m.group_name}</td><td>{dateTime(m.scheduled_for)}</td><td><StatusChip status={m.status} /></td><td><Link className="link-btn" to={`/meetings/${m.id}`}>Open</Link></td></tr>)}</tbody></table></div> : <Empty title="No meetings" />}
      </Card>
      <Modal open={open} onClose={() => setOpen(false)} title="Schedule a meeting">
        <form onSubmit={create}>
          <Field label="Group" htmlFor="mt-group"><select id="mt-group" className="input" value={form.groupId} onChange={(e) => setForm((f) => ({ ...f, groupId: e.target.value }))} required>{data?.groups?.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}</select></Field>
          <Field label="Title" htmlFor="mt-title"><input id="mt-title" className="input" value={form.title} onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))} required /></Field>
          <Field label="Date and time (Africa/Kampala)" htmlFor="mt-when"><input id="mt-when" className="input" type="datetime-local" value={form.scheduledFor} onChange={(e) => setForm((f) => ({ ...f, scheduledFor: e.target.value }))} required /></Field>
          <Field label="Location (typed text only, no tracking)" htmlFor="mt-loc"><input id="mt-loc" className="input" value={form.locationText} onChange={(e) => setForm((f) => ({ ...f, locationText: e.target.value }))} /></Field>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setOpen(false)}>Cancel</button><button className="btn" disabled={busy}>{busy ? 'Scheduling…' : 'Schedule'}</button></div>
        </form>
      </Modal>
    </div>
  );
}
