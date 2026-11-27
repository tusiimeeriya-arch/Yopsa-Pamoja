import React, { useEffect, useState } from 'react';
import { money } from './fmt.js';

export function Logo({ full = false, className = '' }) {
  return <img className={className} src={full ? '/brand/logo.png' : '/brand/mark.png'} alt="Yopsa Microfinance" />;
}

const CHIP_LABELS = {
  register_confirmed: 'Register confirmed', posting_pending: 'Posting pending', partially_verified: 'Partially verified',
  underreview: 'Under review', under_review: 'Under review', groupauthorised: 'Group authorised', officerappraisal: 'Officer appraisal',
  operationsreview: 'Operations review', offerissued: 'Offer issued', groupaccepted: 'Group accepted', notstated: 'Not stated',
};
export function StatusChip({ status }) {
  if (!status) return null;
  const raw = String(status);
  const key = raw.replace(/\s+/g, '').toLowerCase();
  const label = CHIP_LABELS[key] || raw.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
  let cls = 'info';
  if (['pending', 'draft', 'submitted', 'scheduled', 'open', 'underreview', 'posting', 'postingpending', 'partiallyverified'].includes(key)) cls = key === 'pending' || key === 'draft' ? 'pending' : key === 'posting' || key === 'postingpending' ? 'posting' : 'info';
  if (['verified', 'posted', 'active', 'approved', 'resolved', 'closed', 'disbursed'].includes(key)) cls = 'verified';
  if (['disputed', 'rejected', 'failed', 'difference', 'declined', 'suspended'].includes(key)) cls = 'disputed';
  if (['returned', 'cancelled', 'ended', 'withdrawn', 'revoked', 'dismissed', 'notstarted'].includes(key)) cls = 'returned';
  if (['registerconfirmed', 'groupauthorised', 'officerappraisal', 'operationsreview', 'offerissued', 'groupaccepted'].includes(key)) cls = 'posting';
  return <span className={`chip ${cls}`}>{label}</span>;
}

export function PageHead({ title, sub, actions }) {
  return <div className="page-head"><div><h1 className="page-title">{title}</h1>{sub && <div className="page-sub">{sub}</div>}</div>{actions && <div className="row">{actions}</div>}</div>;
}

export function Card({ title, sub, actions, children, className = '' }) {
  return <section className={`card ${className}`}>{(title || actions) && <div className="card-head"><div>{title && <h2 className="card-title">{title}</h2>}{sub && <div className="card-sub">{sub}</div>}</div>{actions && <div className="row">{actions}</div>}</div>}{children}</section>;
}

export function Field({ label, children, hint, error, htmlFor }) {
  return <div className="field">{label && <label className="label" htmlFor={htmlFor}>{label}</label>}{children}{hint && <span className="hint">{hint}</span>}{error && <span className="error-text">{error}</span>}</div>;
}

export function Empty({ title = 'Nothing here yet', children }) {
  return <div className="empty"><h3>{title}</h3>{children || <p>When there is something to show, it will appear here.</p>}</div>;
}

export function Loading({ rows = 3 }) {
  return <div className="grid" aria-label="Loading">{Array.from({ length: rows }).map((_, i) => <div className="skeleton" key={i} style={{ height: 64 }} />)}</div>;
}

export function ErrorBox({ error }) {
  if (!error) return null;
  return <div className="alert danger">{error.message || String(error)}{error.details?.existing?.length ? <ul>{error.details.existing.map((e) => <li key={e.group_id}>{e.full_name} is active in {e.group_name} ({e.status}).</li>)}</ul> : null}</div>;
}

export function Money({ value }) { return <span className="amount">{money(value)}</span>; }

export function Tabs({ tabs, active, onChange }) {
  return <div className="tabs" role="tablist">{tabs.map((t) => <button key={t.id} role="tab" aria-selected={active === t.id} className={`tab ${active === t.id ? 'active' : ''}`} onClick={() => onChange(t.id)}>{t.label}</button>)}</div>;
}

export function Modal({ open, onClose, title, children, wide = false }) {
  useEffect(() => {
    if (!open) return undefined;
    const onKey = (e) => { if (e.key === 'Escape') onClose(); };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open, onClose]);
  if (!open) return null;
  return <div className="modal-backdrop" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}><div className={`modal ${wide ? 'wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}><div className="modal-head"><h2 className="card-title">{title}</h2><button className="modal-close" onClick={onClose} aria-label="Close">×</button></div>{children}</div></div>;
}

export function useToast() {
  const [msg, setMsg] = useState(null);
  useEffect(() => {
    const on = (e) => { setMsg(e.detail); const t = setTimeout(() => setMsg(null), 4200); return () => clearTimeout(t); };
    window.addEventListener('pamoja:toast', on);
    return () => window.removeEventListener('pamoja:toast', on);
  }, []);
  return msg;
}
export function toast(message, type = 'ok') { window.dispatchEvent(new CustomEvent('pamoja:toast', { detail: { message, type } })); }
export function Toaster() {
  const msg = useToast();
  if (!msg) return null;
  return <div className={`toast ${msg.type === 'error' ? 'error' : ''}`} role="status">{msg.message}</div>;
}

export function Summary({ items }) {
  return <dl className="kv">{items.map(([k, v]) => <React.Fragment key={k}><dt>{k}</dt><dd>{v ?? '—'}</dd></React.Fragment>)}</dl>;
}
