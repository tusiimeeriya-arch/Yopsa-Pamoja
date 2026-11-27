import React, { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { get, post } from '../api.js';
import { useAuth } from '../auth.jsx';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, Summary, toast } from '../components.jsx';
import { date, dateTime, time } from '../fmt.js';
import { saveDraft } from '../drafts.js';

export default function MeetingDetail() {
  const { id } = useParams();
  const { user } = useAuth();
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);
  const [minutes, setMinutes] = useState('');
  const [resolution, setResolution] = useState('');
  const [action, setAction] = useState({ title: '', ownerPersonId: '', deadline: '' });
  const [amend, setAmend] = useState({ field: 'minutes', newValue: '', reason: '' });
  const [modal, setModal] = useState(null);
  const [doc, setDoc] = useState({ consentFlag: false, photoPolicyAck: false });
  const [dispute, setDispute] = useState(null);
  const [reason, setReason] = useState('');
  const online = navigator.onLine;

  const load = async () => {
    try { const r = await get(`/meetings/${id}`); setData(r); setMinutes(r.minutes?.body || ''); setError(null); }
    catch (err) { setError(err); }
  };
  useEffect(() => { load(); }, [id]);

  const act = async (fn, msg) => {
    setBusy(true);
    try { await fn(); toast(msg); await load(); setModal(null); }
    catch (err) { toast(err.message, 'error'); }
    finally { setBusy(false); }
  };

  const checkIn = async (personId, status) => {
    if (!online) {
      saveDraft(user.id, { type: 'attendance', label: `Attendance · ${data.meeting.title}`, payload: { meetingId: id, personId, status, confirmationMethod: 'Secretary recorded in person' } });
      toast('Saved as a pending-sync draft. Check-in needs the server.');
      return;
    }
    await act(() => post(`/meetings/${id}/check-in`, personId ? { personId, status, confirmationMethod: 'Secretary recorded in person' } : { status }), 'Checked in.');
  };

  const saveMinutes = async () => act(() => post(`/meetings/${id}/minutes`, { body: minutes }), 'Minutes saved.');
  const addResolution = async (e) => { e.preventDefault(); await act(() => post(`/meetings/${id}/resolutions`, { body: resolution }), 'Resolution recorded.'); setResolution(''); };
  const addAction = async (e) => { e.preventDefault(); await act(() => post(`/meetings/${id}/action-items`, action), 'Action item created with an owner and deadline.'); setAction({ title: '', ownerPersonId: '', deadline: '' }); };
  const requestAmend = async (e) => { e.preventDefault(); await act(() => post(`/meetings/${id}/amendments`, amend), 'Amendment sent for independent approval.'); setAmend({ field: 'minutes', newValue: '', reason: '' }); };
  const approveAmend = async (aid) => act(() => post(`/meetings/${id}/amendments/${aid}/approve`, {}), 'Amendment approved and applied.');
  const uploadDoc = async (e) => {
    e.preventDefault();
    const file = e.target.elements.file.files[0];
    if (!file) return toast('Choose a file.', 'error');
    const reader = new FileReader();
    reader.onload = async () => {
      const payload = { filename: file.name, mime: file.type, size: file.size, data: String(reader.result).split(',')[1], ...doc };
      await act(() => post(`/meetings/${id}/documents`, payload), 'Document uploaded. A photo never proves attendance.');
    };
    reader.readAsDataURL(file);
  };
  const raise = async () => {
    setBusy(true);
    try { await post('/disputes', { kind: 'attendance', entityId: dispute.id, reason }); toast('Attendance dispute sent.'); setDispute(null); setReason(''); load(); }
    catch (err) { toast(err.message, 'error'); } finally { setBusy(false); }
  };

  if (!data && !error) return <Loading rows={4} />;
  if (error) return <ErrorBox error={error} />;
  const { meeting, group, register, minutes: mins, resolutions, actionItems, documents, amendments, permissions } = data;
  const myRow = register.find((r) => r.person_id === permissions.myPersonId);

  return (
    <div>
      <PageHead title={meeting.title} sub={`${group.name} · ${dateTime(meeting.scheduled_for)} · ${meeting.location_text || 'No location text'}`} actions={<><StatusChip status={meeting.status} /><Link className="btn btn-secondary btn-sm" to="/meetings">Back</Link></>} />
      {!online && <div className="alert warn">You are offline. Check-in, register confirmation and closing need the server. Attendance can be saved as a draft.</div>}
      <div className="grid grid-2">
        <Card title="Meeting actions">
          <Summary items={[['Status', <StatusChip status={meeting.status} />], ['Opened', dateTime(meeting.opened_at)], ['Register confirmed', dateTime(meeting.register_confirmed_at)], ['Closed', dateTime(meeting.closed_at)], ['Cancelled reason', meeting.cancel_reason || '—']]} />
          <div className="row mt-2">
            {meeting.status === 'scheduled' && permissions.isSecretary && <button className="btn" disabled={busy} onClick={() => act(() => post(`/meetings/${id}/open`, {}), 'Meeting opened.')}>Open meeting</button>}
            {meeting.status === 'open' && permissions.isSecretary && <button className="btn btn-green" disabled={busy || !online} onClick={() => act(() => post(`/meetings/${id}/confirm-register`, {}), 'Register confirmed and locked.')}>Confirm register</button>}
            {meeting.status === 'register_confirmed' && permissions.isSecretary && <button className="btn" disabled={busy || !online} onClick={() => act(() => post(`/meetings/${id}/close`, {}), 'Meeting closed.')}>Close meeting</button>}
            {['scheduled', 'open'].includes(meeting.status) && permissions.isSecretary && <button className="btn btn-danger" onClick={() => setModal('cancel')}>Cancel meeting</button>}
            {meeting.status === 'closed' && permissions.isSecretary && <button className="btn btn-secondary" onClick={() => setModal('amend')}>Request amendment</button>}
            {permissions.canCheckIn && meeting.status === 'open' && !myRow?.attendance && <button className="btn btn-green" disabled={!online} onClick={() => checkIn(null, 'present')}>Check me in</button>}
          </div>
          {!permissions.isSecretary && <p className="small muted mt-2">Only the secretary of this group can open, confirm or close the meeting.</p>}
        </Card>

        <Card title="Register" sub={`${register.length} member${register.length === 1 ? '' : 's'} in scope`}>
          {register.length === 0 ? <Empty title="No members active on the meeting date" /> : (
            <div className="table-wrap"><table><thead><tr><th>Member</th><th>Status</th><th /></tr></thead><tbody>
              {register.map((r) => <tr key={r.person_id}><td>{r.full_name}</td><td>{r.attendance ? <><StatusChip status={r.attendance.status} /><div className="small muted">{time(r.attendance.checked_in_at)} · {r.attendance.method}</div></> : <span className="small muted">Not checked in</span>}</td>
                <td className="nowrap">{meeting.status === 'open' && permissions.canManage && !r.attendance && <><button className="link-btn" disabled={!online} onClick={() => checkIn(r.person_id, 'present')}>Present</button><button className="link-btn" disabled={!online} onClick={() => checkIn(r.person_id, 'late')}>Late</button><button className="link-btn" disabled={!online} onClick={() => checkIn(r.person_id, 'excused')}>Excused</button></>}
                  {r.attendance && r.person_id === permissions.myPersonId && <button className="link-btn" onClick={() => setDispute(r.attendance)}>Dispute</button>}</td></tr>)}
            </tbody></table></div>
          )}
          <p className="small muted mt-2">Late counts as attended. Excused is excluded from expected, per the versioned attendance policy.</p>
        </Card>
      </div>

      <Card title="Minutes and resolutions" className="mt-2">
        {mins ? <div className="alert info"><strong>Recorded minutes</strong><p className="mt-1" style={{ whiteSpace: 'pre-wrap' }}>{mins.body}</p><div className="small muted">Recorded {dateTime(mins.recorded_at)}</div></div> : <p className="muted small">No minutes recorded.</p>}
        {permissions.isSecretary && ['open', 'register_confirmed'].includes(meeting.status) && (
          <div className="mt-2"><Field label="Minutes" htmlFor="mins"><textarea id="mins" className="input" value={minutes} onChange={(e) => setMinutes(e.target.value)} placeholder="What was discussed, decided and agreed." /></Field><button className="btn" disabled={busy || minutes.trim().length < 10} onClick={saveMinutes}>Save minutes</button></div>
        )}
        {meeting.status === 'closed' && <p className="small muted">Closed minutes can only be changed through an approved amendment.</p>}
        <h3 className="mt-3">Resolutions</h3>
        {resolutions.length === 0 ? <p className="muted small">No resolutions.</p> : resolutions.map((r) => <div className="alert info" key={r.id}>{r.body}<div className="small muted">{dateTime(r.created_at)}</div></div>)}
        {permissions.isSecretary && meeting.status !== 'cancelled' && <form onSubmit={addResolution} className="mt-2"><Field label="New resolution" htmlFor="res"><textarea id="res" className="input" value={resolution} onChange={(e) => setResolution(e.target.value)} /></Field><button className="btn btn-secondary" disabled={busy || resolution.trim().length < 5}>Record resolution</button></form>}
      </Card>

      <Card title="Action items" sub="Every action needs an owner and a deadline." className="mt-2">
        {actionItems.length === 0 ? <p className="muted small">No action items.</p> : <div className="table-wrap"><table><thead><tr><th>Action</th><th>Owner</th><th>Deadline</th><th>Status</th></tr></thead><tbody>{actionItems.map((a) => <tr key={a.id}><td>{a.title}</td><td>{register.find((r) => r.person_id === a.owner_person_id)?.full_name || '—'}</td><td>{date(a.deadline)}</td><td><StatusChip status={a.status} /></td></tr>)}</tbody></table></div>}
        {permissions.canManage && <form onSubmit={addAction} className="form-grid mt-2"><Field label="Action" htmlFor="ai-title"><input id="ai-title" className="input" value={action.title} onChange={(e) => setAction((f) => ({ ...f, title: e.target.value }))} required /></Field><Field label="Owner" htmlFor="ai-owner"><select id="ai-owner" className="input" value={action.ownerPersonId} onChange={(e) => setAction((f) => ({ ...f, ownerPersonId: e.target.value }))} required><option value="">Select</option>{register.map((r) => <option key={r.person_id} value={r.person_id}>{r.full_name}</option>)}</select></Field><Field label="Deadline" htmlFor="ai-deadline"><input id="ai-deadline" className="input" type="date" value={action.deadline} onChange={(e) => setAction((f) => ({ ...f, deadline: e.target.value }))} required /></Field><div className="row end" style={{ alignSelf: 'end' }}><button className="btn btn-secondary" disabled={busy || !action.title || !action.ownerPersonId || !action.deadline}>Add action</button></div></form>}
      </Card>

      <Card title="Meeting documents" sub="Consent and the group photo policy are required. A photo never proves attendance." className="mt-2" actions={permissions.isSecretary && <button className="btn btn-sm" onClick={() => setModal('document')}>Upload</button>}>
        {documents.length === 0 ? <p className="muted small">No documents.</p> : <div className="table-wrap"><table><thead><tr><th>File</th><th>Consent</th><th>Photo policy</th><th /></tr></thead><tbody>{documents.map((d) => <tr key={d.id}><td>{d.filename || d.file_id}</td><td>{d.consent_flag ? 'Recorded' : '—'}</td><td>{d.photo_policy_ack ? 'Acknowledged' : '—'}</td><td><a className="link-btn" href={d.url} target="_blank" rel="noreferrer">Open</a></td></tr>)}</tbody></table></div>}
      </Card>

      {amendments.length > 0 && <Card title="Amendments" className="mt-2">{amendments.map((a) => <div className="alert info" key={a.id}><div className="row between"><strong>{a.field}</strong><StatusChip status={a.status} /></div><div className="small">Original: {a.original_value || '—'}<br />New: {a.new_value}<br />Reason: {a.reason}</div>{a.status === 'pending' && permissions.canManage && <button className="btn btn-sm mt-2" disabled={busy} onClick={() => approveAmend(a.id)}>Approve amendment</button>}</div>)}</Card>}

      <Modal open={modal === 'cancel'} onClose={() => setModal(null)} title="Cancel meeting">
        <form onSubmit={async (e) => { e.preventDefault(); await act(() => post(`/meetings/${id}/cancel`, { reason: e.target.reason.value }), 'Meeting cancelled.'); }}>
          <Field label="Reason" htmlFor="c-reason"><input id="c-reason" name="reason" className="input" required minLength={3} /></Field>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Back</button><button className="btn btn-danger" disabled={busy}>Cancel meeting</button></div>
        </form>
      </Modal>

      <Modal open={modal === 'amend'} onClose={() => setModal(null)} title="Request an amendment">
        <form onSubmit={requestAmend}>
          <Field label="Field" htmlFor="am-field"><select id="am-field" className="input" value={amend.field} onChange={(e) => setAmend((f) => ({ ...f, field: e.target.value }))}><option value="minutes">Minutes</option>{register.map((r) => <option key={r.person_id} value={`attendance:${r.person_id}`}>Attendance · {r.full_name}</option>)}</select></Field>
          <Field label="New value" htmlFor="am-value"><textarea id="am-value" className="input" value={amend.newValue} onChange={(e) => setAmend((f) => ({ ...f, newValue: e.target.value }))} required /></Field>
          <Field label="Reason" htmlFor="am-reason"><textarea id="am-reason" className="input" value={amend.reason} onChange={(e) => setAmend((f) => ({ ...f, reason: e.target.value }))} required minLength={10} /></Field>
          <div className="alert warn small">The reviewer must be a different person from the requester.</div>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy || amend.reason.length < 10 || !amend.newValue}>Send for approval</button></div>
        </form>
      </Modal>

      <Modal open={modal === 'document'} onClose={() => setModal(null)} title="Upload meeting document">
        <form onSubmit={uploadDoc}>
          <Field label="File" htmlFor="md-file"><input id="md-file" name="file" className="input" type="file" accept="image/png,image/jpeg,image/webp,application/pdf" required /></Field>
          <label className="check mb-2"><input type="checkbox" checked={doc.consentFlag} onChange={(e) => setDoc((d) => ({ ...d, consentFlag: e.target.checked }))} /> Consent to store this document has been recorded.</label>
          <label className="check mb-2"><input type="checkbox" checked={doc.photoPolicyAck} onChange={(e) => setDoc((d) => ({ ...d, photoPolicyAck: e.target.checked }))} /> I acknowledge the group photo policy. A photo never proves attendance.</label>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy || !doc.consentFlag || !doc.photoPolicyAck}>Upload</button></div>
        </form>
      </Modal>

      <Modal open={!!dispute} onClose={() => setDispute(null)} title="Dispute my attendance">
        <div className="field"><label className="label" htmlFor="d-reason">What is wrong?</label><textarea id="d-reason" className="input" value={reason} onChange={(e) => setReason(e.target.value)} minLength={10} /></div>
        <div className="row end"><button className="btn btn-secondary" onClick={() => setDispute(null)}>Cancel</button><button className="btn" disabled={busy || reason.trim().length < 10} onClick={raise}>Send dispute</button></div>
      </Modal>
    </div>
  );
}
