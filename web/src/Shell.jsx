import React, { useEffect, useState } from 'react';
import { NavLink, Outlet, useNavigate } from 'react-router-dom';
import { useAuth } from './auth.jsx';
import { Logo } from './components.jsx';

const ICONS = { home: '⌂', groups: '👥', calendar: '▦', coins: '◉', loan: '₴', chart: '▤', trophy: '★', book: '▭', flag: '⚑', check: '✓', shield: '⛨' };

export default function Shell() {
  const { user, nav, roles, isAdmin, logout, demoMode } = useAuth();
  const navigate = useNavigate();
  const [online, setOnline] = useState(navigator.onLine);
  useEffect(() => {
    const up = () => setOnline(true); const down = () => setOnline(false);
    window.addEventListener('online', up); window.addEventListener('offline', down);
    return () => { window.removeEventListener('online', up); window.removeEventListener('offline', down); };
  }, []);
  if (!user) { navigate('/login', { replace: true }); return null; }
  const roleLabel = roles.length ? roles[0].role.replace(/_/g, ' ') + (roles.length > 1 ? ` +${roles.length - 1}` : '') : 'No role yet';
  const initials = (user.full_name || '?').split(' ').map((p) => p[0]).slice(0, 2).join('').toUpperCase();
  const items = nav.length ? nav : [{ to: '/', label: 'Home', icon: 'home' }];
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand"><Logo className="mark" /><div className="brand-name">Yopsa Pamoja<span>Microfinance</span></div></div>
        <nav className="side-nav" aria-label="Main navigation">
          {items.map((n) => <NavLink key={n.to} to={n.to} end={n.to === '/'} className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}><span className="nav-icon" aria-hidden="true">{ICONS[n.icon] || '•'}</span>{n.label}</NavLink>)}
        </nav>
        <div className="sidebar-foot">
          <div className="row between"><span className="small muted">Signed in as</span><button className="link-btn" onClick={async () => { await logout(); navigate('/login'); }}>Sign out</button></div>
          <div className="strong small">{user.full_name}</div>
          <div className="small muted" style={{ textTransform: 'capitalize' }}>{roleLabel}</div>
        </div>
      </aside>
      <div className="main">
        <header className="topbar">
          <div className="brand"><Logo className="mark" /><div className="brand-name">Pamoja<span>Yopsa Microfinance</span></div></div>
          <div className="user-chip">
            <div className="user-meta hide-mobile"><strong>{user.full_name}</strong><span style={{ textTransform: 'capitalize' }}>{roleLabel}</span></div>
            <div className="avatar" aria-hidden="true">{initials}</div>
            <button className="btn btn-secondary btn-sm" onClick={async () => { await logout(); navigate('/login'); }}>Sign out</button>
          </div>
        </header>
        {demoMode && <div className="demo-banner" role="status"><strong>DEMO DATA</strong><span>This workspace contains demo records. {isAdmin ? 'See Admin → Demo guide.' : 'Do not use for real decisions.'}</span></div>}
        {!online && <div className="offline-banner" role="status">You are offline. Verification, approval, posting and closing are unavailable. You can save drafts.</div>}
        <main className="content"><Outlet /></main>
        <nav className="bottom-nav" aria-label="Main navigation">
          {items.map((n) => <NavLink key={n.to} to={n.to} end={n.to === '/'} className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}><span className="nav-icon" aria-hidden="true">{ICONS[n.icon] || '•'}</span>{n.label}</NavLink>)}
        </nav>
      </div>
    </div>
  );
}
