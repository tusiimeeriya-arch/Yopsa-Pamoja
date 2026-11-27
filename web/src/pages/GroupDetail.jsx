import React, { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { get, post } from '../api.js';
import { Card, Empty, ErrorBox, Field, Loading, Modal, PageHead, StatusChip, Summary, Tabs, toast } from '../components.jsx';
import { date, dateTime, money, pct } from '../fmt.js';

const TABS = [
  { id: 'overview', label: 'Overview' }, { id: 'members', label: 'Members' }, { id: 'leadership', label: 'Leadership' },
  { id: 'meetings', label: 'Meetings' }, { id: 'payments', label: 'Payments' }, { id: 'loans', label: 'Loans' },
  { id: 'documents', label: 'Documents' }, { id: 'comments', label: 'Comments' }, { id: 'review', label: 'Review' },
];

function fileToBase64(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve({ filename: file.name, mime: file.type, size: file.size, data: String(reader.result).split(',')[1] });
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });
}

export default function GroupDetail() {
  const { id } = useParams();
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [tab, setTab] = useState('overview');
  const [entries, setEntries] = useState([]);
  const [modal, setModal] = useState(null);
  const [busy, setBusy] = useState(false);
  const [dup, setDup] = useState(null);
  const [memberForm, setMemberForm] = useState({ fullName: '', phone: '', nationalIdRef: '', gender: 'Female', dateOfBirth: '', joinDate: new Date().toISOString().slice(0, 10), consentRecorded: false, acknowledgeDuplicate: false });
  const [leadForm, setLeadForm] = useState({ personId: '', position: 'chairperson', effectiveFrom: new Date().toISOString().slice(0, 10), effectiveTo: '', authorityScope: 'group' });
  const [reviewForm, setReviewForm] = useState({ decision: 'approve', note: '' });
  const [docForm, setDocForm] = useState({ kind: 'registration_evidence' });
  const [editMember, setEditMember] = useState(null);
  const [comments, setComments] = useState(null);
  const [commentBody, setCommentBody] = useState('');
  const [commentVisibility, setCommentVisibility] = useState('group');

  const load = async () => {
    try { setData(await get(`/groups/${id}`)); setError(null); } catch (err) { setError(err); }
  };
  useEffect(() => { load(); }, [id]);
  useEffect(() => { if (tab === 'payments') get(`/payments/entries?groupId=${id}`).then((r) => setEntries(r.entries)).catch(() => {}); }, [tab, id]);
  useEffect(() => { if (tab === 'comments') get(`/comments?groupId=${id}`).then(setComments).catch(() => setComments({ comments: [], canModerate: false })); }, [tab, id]);

  const act = async (fn, success) => {
    setBusy(true);
    try { await fn(); toast(success); setModal(null); setDup(null); await load(); }
    catch (err) {
      if (err.code === 'DUPLICATE_MEMBER') { setDup(err.details); }
      else toast(err.message, 'error');
    } finally { setBusy(false); }
  };

  const addMember = async (e) => {
    e.preventDefault();
    await act(() => post(`/groups/${id}/members`, memberForm), 'Member added.');
    setMemberForm((f) => ({ ...f, fullName: '', phone: '', nationalIdRef: '', dateOfBirth: '', consentRecorded: false, acknowledgeDuplicate: false }));
  };
  const addLeader = async (e) => { e.preventDefault(); await act(() => post(`/groups/${id}/leadership`, leadForm), 'Leadership recorded.'); };
  const uploadDoc = async (e) => {
    e.preventDefault();
    const file = e.target.elements.file.files[0];
    if (!file) return toast('Choose a file.', 'error');
    const payload = await fileToBase64(file);
    await act(() => post(`/groups/${id}/documents`, { ...payload, kind: docForm.kind }), 'Document uploaded privately.');
  };
  const submitReview = async (e) => { e.preventDefault(); await act(() => post(`/groups/${id}/review`, reviewForm), 'Review recorded.'); };

  if (!data && !error) return <Loading rows={4} />;
  if (error) return <ErrorBox error={error} />;
  const { group, members, leadership, documents, meetings, submissions, loans, attendance, permissions } = data;

  return (
    <div>
      <PageHead title={group.name} sub={`${group.type} · ${group.branch_name} · Registration evidence: ${group.registration_evidence_status}`} actions={<><StatusChip status={group.status} /><Link className="btn btn-secondary btn-sm" to="/groups">Back</Link></>} />
      <Tabs tabs={TABS} active={tab} onChange={setTab} />

      {tab === 'overview' && (
        <div className="grid grid-2">
          <Card title="Group record">
            <Summary items={[['Status', <StatusChip status={group.status} />], ['Type', group.type], ['Branch', group.branch_name], ['Registration evidence', group.registration_evidence_status], ['Created', date(group.created_at)], ['Submitted', date(group.submitted_at)], ['Reviewed', date(group.reviewed_at)], ['Review decision', group.review_decision || '—'], ['Review note', group.review_note || '—'], ['Activated', date(group.activated_at)]]} />
          </Card>
          <Card title="Attendance" sub={attendance.group.method}>
            <div className="stat blue"><div className="stat-label">Group attendance</div><div className="stat-value">{pct(attendance.group.percent)}</div><div className="stat-note">{attendance.group.attended} attended of {attendance.group.expected} expected opportunities</div></div>
            <div className="table-wrap mt-2"><table><thead><tr><th>Member</th><th className="right">Attended</th><th className="right">Expected</th><th className="right">%</th></tr></thead><tbody>{attendance.people.map((p) => <tr key={p.personId}><td>{p.fullName}{p.left ? <span className="small muted"> (left {date(p.left)})</span> : null}</td><td className="right">{p.attended}</td><td className="right">{p.expected}</td><td className="right">{pct(p.percent)}</td></tr>)}</tbody></table></div>
          </Card>
        </div>
      )}

      {tab === 'members' && (
        <Card title={`Members (${members.length})`} actions={permissions.canManage && <button className="btn btn-sm" onClick={() => setModal('member')}>Add member</button>}>
          {members.length === 0 ? <Empty title="No members yet" /> : <div className="table-wrap"><table><thead><tr><th>Name</th><th>Phone</th><th>Gender</th><th>National ID</th><th>Joined</th><th>Status</th><th /></tr></thead><tbody>{members.map((m) => <tr key={m.id}><td>{m.full_name}</td><td>{m.phone || '—'}</td><td>{m.gender}</td><td>{m.national_id_ref || '—'}</td><td>{date(m.join_date)}</td><td>{m.leave_date ? <StatusChip status="ended" /> : <StatusChip status="active" />}</td><td className="nowrap">{permissions.canManage && !m.leave_date && <><button className="link-btn" onClick={() => setEditMember(m)}>Edit</button><button className="link-btn" onClick={() => setModal({ type: 'end', member: m })}>End</button></>}</td></tr>)}</tbody></table></div>}
          <p className="small muted mt-2">Memberships are end-dated, never deleted. National ID and phone are masked unless your role allows them.</p>
        </Card>
      )}

      {tab === 'leadership' && (
        <Card title="Leadership and authority" actions={permissions.canManage && <button className="btn btn-sm" onClick={() => setModal('leadership')}>Record leadership</button>}>
          {leadership.length === 0 ? <Empty title="No leadership recorded" /> : <div className="table-wrap"><table><thead><tr><th>Person</th><th>Position</th><th>Authority</th><th>From</th><th>To</th></tr></thead><tbody>{leadership.map((l) => <tr key={l.id}><td>{l.full_name}</td><td style={{ textTransform: 'capitalize' }}>{l.position.replace(/_/g, ' ')}</td><td>{l.authority_scope}</td><td>{date(l.effective_from)}</td><td>{l.effective_to ? date(l.effective_to) : 'Open'}</td></tr>)}</tbody></table></div>}
        </Card>
      )}

      {tab === 'meetings' && (
        <Card title="Meetings" actions={permissions.canManage && <Link className="btn btn-sm" to={`/meetings?group=${id}`}>Schedule</Link>}>
          {meetings.length === 0 ? <Empty title="No meetings yet" /> : <div className="table-wrap"><table><thead><tr><th>Meeting</th><th>When</th><th>Status</th><th /></tr></thead><tbody>{meetings.map((m) => <tr key={m.id}><td>{m.title}</td><td>{dateTime(m.scheduled_for)}</td><td><StatusChip status={m.status} /></td><td><Link className="link-btn" to={`/meetings/${m.id}`}>Open</Link></td></tr>)}</tbody></table></div>}
        </Card>
      )}

      {tab === 'payments' && (
        <>
          <Card title="Submissions" sub="Submitted, verified and posted are separate facts." actions={<Link className="btn btn-sm btn-secondary" to={`/payments?group=${id}`}>Payment workspace</Link>}>
            {submissions.length === 0 ? <Empty title="No submissions yet" /> : <div className="table-wrap"><table><thead><tr><th>Period</th><th className="right">Submitted</th><th className="right">Received</th><th className="right">Difference</th><th>Status</th></tr></thead><tbody>{submissions.map((s) => <tr key={s.id}><td>{date(s.period_start)} – {date(s.period_end)}</td><td className="right amount">{money(s.total_amount)}</td><td className="right amount">{money(s.receipt_total)}</td><td className="right amount">{s.difference === null ? '—' : money(s.difference)}</td><td><StatusChip status={s.status} /></td></tr>)}</tbody></table></div>}
          </Card>
          <Card title="Payment entries" sub="Pending entries never change a loan balance.">
            {entries.length === 0 ? <Empty title="No entries in this group" /> : <div className="table-wrap"><table><thead><tr><th>Member</th><th>Category</th><th className="right">Amount</th><th>Date</th><th>Status</th></tr></thead><tbody>{entries.map((e) => <tr key={e.id}><td>{e.full_name}</td><td style={{ textTransform: 'capitalize' }}>{e.category.replace(/_/g, ' ')}</td><td className="right amount">{money(e.amount)}</td><td>{date(e.collection_date)}</td><td><StatusChip status={e.status} /></td></tr>)}</tbody></table></div>}
          </Card>
        </>
      )}

      {tab === 'loans' && (
        <Card title="Loan applications">
          {loans.length === 0 ? <Empty title="No loan applications" /> : <div className="table-wrap"><table><thead><tr><th>Product</th><th className="right">Amount</th><th>Term</th><th>Status</th><th /></tr></thead><tbody>{loans.map((l) => <tr key={l.id}><td>{l.product}</td><td className="right amount">{money(l.amount)}</td><td>{l.term_months} months</td><td><StatusChip status={l.status} /></td><td><Link className="link-btn" to={`/loans/${l.id}`}>Open</Link></td></tr>)}</tbody></table></div>}
        </Card>
      )}

      {tab === 'documents' && (
        <Card title="Private documents" sub="Short-lived signed links only. Images and PDF, 5 MB maximum." actions={permissions.canManage && <button className="btn btn-sm" onClick={() => setModal('document')}>Upload</button>}>
          {documents.length === 0 ? <Empty title="No documents" /> : <div className="table-wrap"><table><thead><tr><th>Kind</th><th>File</th><th>Size</th><th>Uploaded</th><th /></tr></thead><tbody>{documents.map((d) => <tr key={d.id}><td>{d.kind.replace(/_/g, ' ')}</td><td>{d.filename}</td><td>{(d.size / 1024).toFixed(0)} KB</td><td>{date(d.created_at)}</td><td><a className="link-btn" href={d.url} target="_blank" rel="noreferrer">Open</a></td></tr>)}</tbody></table></div>}
        </Card>
      )}

      {tab === 'comments' && (
        <Card title="Group comments" sub="Group-visible or restricted. Removed comments keep the reason and moderator.">
          <form onSubmit={async (e) => { e.preventDefault(); try { await post('/comments', { groupId: id, body: commentBody, visibility: commentVisibility }); setCommentBody(''); toast('Comment posted.'); setComments(await get(`/comments?groupId=${id}`)); } catch (err) { toast(err.message, 'error'); } }}>
            <Field label="Comment" htmlFor="c-body"><textarea id="c-body" className="input" value={commentBody} onChange={(e) => setCommentBody(e.target.value)} minLength={2} /></Field>
            <div className="row"><select className="input" style={{ maxWidth: 200 }} value={commentVisibility} onChange={(e) => setCommentVisibility(e.target.value)}><option value="group">Group visible</option><option value="restricted">Restricted</option></select><button className="btn" disabled={commentBody.trim().length < 2}>Post comment</button></div>
          </form>
          <div className="mt-2">
            {!comments ? <Loading rows={2} /> : comments.comments.length === 0 ? <Empty title="No comments yet" /> : comments.comments.map((c) => <div className="alert info" key={c.id}><div className="row between"><strong>{c.author_name}</strong><span className="small muted">{dateTime(c.created_at)} · {c.visibility}</span></div><div className="small mt-1">{c.body}</div>{c.status === 'removed' ? <div className="small error-text">Removed: {c.moderation_reason}</div> : comments.canModerate && <button className="link-btn mt-1" onClick={async () => { const reason = window.prompt('Moderation reason'); if (!reason) return; try { await post(`/comments/${c.id}/moderate`, { reason }); toast('Comment removed with reason.'); setComments(await get(`/comments?groupId=${id}`)); } catch (err) { toast(err.message, 'error'); } }}>Remove</button>}</div>)}
          </div>
        </Card>
      )}

      {tab === 'review' && (
        <Card title="Registration review" sub="A group is active only after an independent review.">
          <div className="timeline">
            <div className="timeline-item"><span className="timeline-dot" /><div><strong>Created</strong><div className="small muted">{date(group.created_at)} · evidence {group.registration_evidence_status}</div></div></div>
            <div className="timeline-item"><span className="timeline-dot" /><div><strong>Submitted for review</strong><div className="small muted">{date(group.submitted_at)}</div></div></div>
            <div className="timeline-item"><span className="timeline-dot" /><div><strong>Independent review</strong><div className="small muted">{group.reviewed_at ? `${date(group.reviewed_at)} · ${group.review_decision}${group.review_note ? ' · ' + group.review_note : ''}` : 'Not reviewed yet'}</div></div></div>
            <div className="timeline-item"><span className="timeline-dot" /><div><strong>Activation</strong><div className="small muted">{group.activated_at ? date(group.activated_at) : 'Not active'}</div></div></div>
          </div>
          <div className="row mt-2">
            {['Draft', 'Returned'].includes(group.status) && permissions.canManage && <button className="btn" disabled={busy} onClick={() => act(() => post(`/groups/${id}/submit`, {}), 'Submitted for independent review.')}>Submit for review</button>}
            {permissions.canReview && ['Submitted', 'UnderReview'].includes(group.status) && <button className="btn" onClick={() => setModal('review')}>Record review decision</button>}
            {permissions.canActivate && group.status === 'UnderReview' && group.review_decision === 'approve' && <button className="btn btn-green" disabled={busy} onClick={() => act(() => post(`/groups/${id}/activate`, {}), 'Group activated.')}>Activate group</button>}
          </div>
        </Card>
      )}

      <Modal open={modal === 'member'} onClose={() => { setModal(null); setDup(null); }} title="Add member">
        <form onSubmit={addMember}>
          <div className="form-grid">
            <Field label="Full name" htmlFor="m-name"><input id="m-name" className="input" value={memberForm.fullName} onChange={(e) => setMemberForm((f) => ({ ...f, fullName: e.target.value }))} required /></Field>
            <Field label="Phone" htmlFor="m-phone"><input id="m-phone" className="input" value={memberForm.phone} onChange={(e) => setMemberForm((f) => ({ ...f, phone: e.target.value }))} required placeholder="+256…" /></Field>
            <Field label="National ID reference (optional)" htmlFor="m-nid"><input id="m-nid" className="input" value={memberForm.nationalIdRef} onChange={(e) => setMemberForm((f) => ({ ...f, nationalIdRef: e.target.value }))} /></Field>
            <Field label="Gender" htmlFor="m-gender"><select id="m-gender" className="input" value={memberForm.gender} onChange={(e) => setMemberForm((f) => ({ ...f, gender: e.target.value }))}>{['Female', 'Male', 'Other', 'NotStated'].map((g) => <option key={g}>{g}</option>)}</select></Field>
            <Field label="Date of birth (optional)" htmlFor="m-dob"><input id="m-dob" className="input" type="date" value={memberForm.dateOfBirth} onChange={(e) => setMemberForm((f) => ({ ...f, dateOfBirth: e.target.value }))} /></Field>
            <Field label="Join date" htmlFor="m-join"><input id="m-join" className="input" type="date" value={memberForm.joinDate} onChange={(e) => setMemberForm((f) => ({ ...f, joinDate: e.target.value }))} required /></Field>
            <div className="full"><label className="check"><input type="checkbox" checked={memberForm.consentRecorded} onChange={(e) => setMemberForm((f) => ({ ...f, consentRecorded: e.target.checked }))} /> The member has been told how their data is used and has consented.</label></div>
          </div>
          {dup && <div className="alert warn"><strong>Existing membership found.</strong><ul>{dup.existing.map((x) => <li key={x.group_id}>{x.full_name} is active in {x.group_name} ({x.status}).</li>)}</ul><label className="check"><input type="checkbox" checked={memberForm.acknowledgeDuplicate} onChange={(e) => setMemberForm((f) => ({ ...f, acknowledgeDuplicate: e.target.checked }))} /> I reviewed the existing obligations and still want to add this person.</label></div>}
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => { setModal(null); setDup(null); }}>Cancel</button><button className="btn" disabled={busy || !memberForm.consentRecorded || (dup && !memberForm.acknowledgeDuplicate)}>{busy ? 'Saving…' : 'Add member'}</button></div>
        </form>
      </Modal>

      <Modal open={modal === 'leadership'} onClose={() => setModal(null)} title="Record leadership">
        <form onSubmit={addLeader}>
          <Field label="Person" htmlFor="l-person"><select id="l-person" className="input" value={leadForm.personId} onChange={(e) => setLeadForm((f) => ({ ...f, personId: e.target.value }))} required><option value="">Select a member</option>{members.map((m) => <option key={m.person_id} value={m.person_id}>{m.full_name}</option>)}</select></Field>
          <Field label="Position" htmlFor="l-pos"><select id="l-pos" className="input" value={leadForm.position} onChange={(e) => setLeadForm((f) => ({ ...f, position: e.target.value }))}>{['chairperson', 'secretary', 'treasurer', 'committee_member', 'authorised_representative'].map((p) => <option key={p} value={p}>{p.replace(/_/g, ' ')}</option>)}</select></Field>
          <div className="form-grid">
            <Field label="Effective from" htmlFor="l-from"><input id="l-from" className="input" type="date" value={leadForm.effectiveFrom} onChange={(e) => setLeadForm((f) => ({ ...f, effectiveFrom: e.target.value }))} required /></Field>
            <Field label="Effective to (optional)" htmlFor="l-to"><input id="l-to" className="input" type="date" value={leadForm.effectiveTo} onChange={(e) => setLeadForm((f) => ({ ...f, effectiveTo: e.target.value }))} /></Field>
          </div>
          <Field label="Authority scope" htmlFor="l-auth"><select id="l-auth" className="input" value={leadForm.authorityScope} onChange={(e) => setLeadForm((f) => ({ ...f, authorityScope: e.target.value }))}><option value="group">Group</option><option value="borrowing">Borrowing</option><option value="signatory">Signatory</option></select></Field>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy || !leadForm.personId}>{busy ? 'Saving…' : 'Record'}</button></div>
        </form>
      </Modal>

      <Modal open={modal === 'document'} onClose={() => setModal(null)} title="Upload a private document">
        <form onSubmit={uploadDoc}>
          <Field label="Kind" htmlFor="d-kind"><select id="d-kind" className="input" value={docForm.kind} onChange={(e) => setDocForm({ kind: e.target.value })}><option value="registration_evidence">Registration evidence</option><option value="rules">Group rules</option><option value="leadership_evidence">Leadership evidence</option><option value="other">Other</option></select></Field>
          <Field label="File" htmlFor="d-file" hint="Images (PNG, JPEG, WebP) or PDF, up to 5 MB."><input id="d-file" name="file" className="input" type="file" accept="image/png,image/jpeg,image/webp,application/pdf" required /></Field>
          <div className="alert info small">Files are stored privately and opened through a short-lived signed link.</div>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy}>{busy ? 'Uploading…' : 'Upload'}</button></div>
        </form>
      </Modal>

      <Modal open={modal === 'review'} onClose={() => setModal(null)} title="Review this group">
        <form onSubmit={submitReview}>
          <Field label="Decision" htmlFor="r-decision"><select id="r-decision" className="input" value={reviewForm.decision} onChange={(e) => setReviewForm((f) => ({ ...f, decision: e.target.value }))}><option value="approve">Approve (pending activation)</option><option value="return">Return for correction</option><option value="reject">Reject</option></select></Field>
          <Field label="Note" htmlFor="r-note"><textarea id="r-note" className="input" value={reviewForm.note} onChange={(e) => setReviewForm((f) => ({ ...f, note: e.target.value }))} /></Field>
          <div className="alert warn small">You cannot review a group you created. Approval does not activate the group; activation is a separate step.</div>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn" disabled={busy}>{busy ? 'Saving…' : 'Record decision'}</button></div>
        </form>
      </Modal>

      <Modal open={modal?.type === 'end'} onClose={() => setModal(null)} title="End membership">
        {modal?.member && <form onSubmit={async (e) => { e.preventDefault(); const leaveDate = e.target.elements.leave.value; await act(() => post(`/groups/${id}/members/${modal.member.id}/end`, { leaveDate }), 'Membership ended.'); }}>
          <p className="small">End the membership of <strong>{modal.member.full_name}</strong>. The record is kept and end-dated.</p>
          <Field label="Leave date" htmlFor="end-date"><input id="end-date" name="leave" className="input" type="date" defaultValue={new Date().toISOString().slice(0, 10)} required /></Field>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button><button className="btn btn-danger" disabled={busy}>End membership</button></div>
        </form>}
      </Modal>

      <Modal open={!!editMember} onClose={() => setEditMember(null)} title="Edit member details">
        {editMember && <form onSubmit={async (e) => {
          e.preventDefault();
          const f = e.target;
          await act(() => post(`/groups/${id}/members/${editMember.id}/update`, { fullName: f.fullName.value, phone: f.phone.value, nationalIdRef: f.nid.value, gender: f.gender.value, dateOfBirth: f.dob.value || null }), 'Member updated.');
          setEditMember(null);
        }}>
          <Field label="Full name"><input name="fullName" className="input" defaultValue={editMember.full_name} required /></Field>
          <Field label="Phone"><input name="phone" className="input" defaultValue={editMember.phone || ''} required /></Field>
          <Field label="National ID reference"><input name="nid" className="input" defaultValue={editMember.national_id_ref || ''} /></Field>
          <Field label="Gender"><select name="gender" className="input" defaultValue={editMember.gender}>{['Female', 'Male', 'Other', 'NotStated'].map((g) => <option key={g}>{g}</option>)}</select></Field>
          <Field label="Date of birth"><input name="dob" className="input" type="date" defaultValue={editMember.date_of_birth?.slice(0, 10) || ''} /></Field>
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => setEditMember(null)}>Cancel</button><button className="btn" disabled={busy}>Save changes</button></div>
        </form>}
      </Modal>
    </div>
  );
}
