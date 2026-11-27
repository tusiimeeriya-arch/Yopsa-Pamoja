import React from 'react';
import { Link } from 'react-router-dom';

export default function Privacy() {
  return (
    <div className="login-wrap">
      <div style={{ width: 'min(720px, 100%)' }}>
        <div className="login-card" style={{ textAlign: 'left' }}>
          <h1 className="login-title">Privacy notice</h1>
          <p className="muted small">Yopsa Microfinance · Pamoja</p>
          <div className="small">
            <p><strong>What we collect.</strong> Pamoja records only what is needed to manage group membership, meetings, savings, loans and the audit trail for Yopsa Microfinance: your name, phone number, gender, date of birth where provided, national ID reference, group membership, attendance, payment entries and loan records.</p>
            <p><strong>What we do not do.</strong> We do not use facial recognition, we do not track your location continuously, and we never sell your data.</p>
            <p><strong>Consent.</strong> Consent is recorded when a member is registered. You can ask your group officers or Yopsa branch about the consent recorded for you.</p>
            <p><strong>Who can see your data.</strong> Only Yopsa staff and group officers whose role and scope allow it. Phone numbers and national ID references are masked unless your role requires the full value. Every sensitive action is written to an append-only audit log.</p>
            <p><strong>How long we keep it.</strong> Financial and audit records are retained as required by Yopsa policy and applicable law. Audit records are never edited or deleted.</p>
            <p><strong>Your rights.</strong> Contact your Yopsa branch to ask for access to, or correction of, your information, or to raise a complaint.</p>
            <p><strong>Security.</strong> Data is stored in PostgreSQL with row-level security, least-privilege database roles and a hash-chained audit log. Sensitive actions require a one-time confirmation code.</p>
          </div>
          <div className="row mt-2"><Link className="btn btn-secondary" to="/login">Back to sign in</Link></div>
        </div>
      </div>
    </div>
  );
}
