import { q } from '../db.js';
import { policy } from '../config.js';

/**
 * Attendance facts:
 *  - eligible meetings = closed meetings of the group inside the period,
 *    on/after the member joined and on/before they left; cancelled excluded.
 *  - excused meetings are removed from expected (default policy).
 *  - late counts as attended (default policy).
 *  - individual % = attended / expected, or "Not applicable" when expected = 0.
 *  - group % = total attended opportunities / total expected opportunities
 *    (never the average of individual percentages).
 */
export async function attendanceSummary(groupId, { from = null, to = null, client = { query: q } } = {}) {
  const [lateCounts, excusedExcluded] = await Promise.all([
    policy('attendance.late_counts_as_present'),
    policy('attendance.excused_counts_as_present'),
  ]);
  const meetings = (await client.query(
    `SELECT id, scheduled_for::date AS meeting_date FROM meetings
     WHERE group_id=$1 AND status='closed'
       AND ($2::date IS NULL OR scheduled_for::date >= $2)
       AND ($3::date IS NULL OR scheduled_for::date <= $3)
     ORDER BY scheduled_for`,
    [groupId, from, to],
  )).rows;
  const members = (await client.query(
    `SELECT m.id AS membership_id, m.join_date, m.leave_date, p.id AS person_id, p.full_name
     FROM memberships m JOIN persons p ON p.id=m.person_id
     WHERE m.group_id=$1 ORDER BY p.full_name`,
    [groupId],
  )).rows;
  const attendance = (await client.query(
    `SELECT a.meeting_id, a.person_id, a.status FROM attendance_records a
     JOIN meetings mt ON mt.id=a.meeting_id WHERE mt.group_id=$1`,
    [groupId],
  )).rows;
  const byKey = new Map(attendance.map((a) => [`${a.meeting_id}:${a.person_id}`, a.status]));

  let totalExpected = 0;
  let totalAttended = 0;
  const people = members.map((m) => {
    const eligible = meetings.filter((mt) => mt.meeting_date >= m.join_date && (!m.leave_date || mt.meeting_date <= m.leave_date));
    let excused = 0;
    let attended = 0;
    for (const mt of eligible) {
      const status = byKey.get(`${mt.id}:${m.person_id}`);
      if (status === 'excused') { if (excusedExcluded) attended += 1; else excused += 1; }
      if (status === 'present' || (status === 'late' && lateCounts)) attended += 1;
    }
    const expected = eligible.length - excused;
    totalExpected += expected;
    totalAttended += attended;
    return {
      personId: m.person_id,
      fullName: m.full_name,
      joined: m.join_date,
      left: m.leave_date,
      expected,
      attended,
      percent: expected === 0 ? null : Math.round((attended / expected) * 1000) / 10,
      display: expected === 0 ? 'Not applicable' : `${Math.round((attended / expected) * 1000) / 10}%`,
    };
  });
  return {
    group: {
      expected: totalExpected,
      attended: totalAttended,
      percent: totalExpected === 0 ? null : Math.round((totalAttended / totalExpected) * 1000) / 10,
      display: totalExpected === 0 ? 'Not applicable' : `${Math.round((totalAttended / totalExpected) * 1000) / 10}%`,
      method: 'total attended opportunities / total expected opportunities',
    },
    people,
    policy: { lateCountsAsPresent: lateCounts, excusedCountsAsPresent: excusedExcluded },
  };
}

export async function memberAttendance(groupId, personId, period = {}) {
  const summary = await attendanceSummary(groupId, period);
  return summary.people.find((p) => p.personId === personId) || null;
}
