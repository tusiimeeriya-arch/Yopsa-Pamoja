import React, { useEffect, useState } from 'react';
import { Navigate, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth.jsx';
import { Modal } from '../components.jsx';

export default function Login() {
  const { user, login, idleMessage } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [help, setHelp] = useState(null);
  const [install, setInstall] = useState(null);

  useEffect(() => {
    const handler = (e) => { e.preventDefault(); setInstall(e); };
    window.addEventListener('beforeinstallprompt', handler);
    return () => window.removeEventListener('beforeinstallprompt', handler);
  }, []);

  if (user) return <Navigate to="/" replace />;

  const submit = async (e) => {
    e.preventDefault();
    setError('');
    if (!email.trim() || !password) { setError('Enter your email and password.'); return; }
    setBusy(true);
    try { await login(email.trim(), password); navigate('/', { replace: true }); }
    catch (err) { setError(err.message || 'Email or password is incorrect.'); }
    finally { setBusy(false); }
  };

  const isIos = /iPhone|iPad|iPod/.test(navigator.userAgent);

  return (
    <div className="login-wrap">
      <div style={{ width: 'min(430px, 100%)' }}>
        <div className="login-card">
          <img className="login-logo" src="/brand/logo.png" alt="Yopsa Microfinance" />
          <h1 className="login-title">Sign in to Pamoja</h1>
          <p className="login-sub">Stronger groups. Informed decisions.</p>
          {idleMessage && <div className="alert warn" role="status">{idleMessage}</div>}
          <form onSubmit={submit} noValidate>
            <div className="field">
              <label className="label" htmlFor="email">Email</label>
              <input id="email" className="input" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="name@yopsa.co.ug" required />
            </div>
            <div className="field">
              <label className="label" htmlFor="password">Password</label>
              <div className="pw-wrap">
                <input id="password" className="input" type={show ? 'text' : 'password'} autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
                <button type="button" className="pw-toggle" onClick={() => setShow((s) => !s)} aria-label={show ? 'Hide password' : 'Show password'}>{show ? 'Hide' : 'Show'}</button>
              </div>
            </div>
            {error && <div className="alert danger" role="alert">{error}</div>}
            <button className="btn btn-block" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
          </form>
          <div className="row between mt-2">
            <button className="link-btn" type="button" onClick={() => setHelp('forgot')}>Forgot password?</button>
            <button className="link-btn" type="button" onClick={() => setHelp('privacy')}>Privacy notice</button>
          </div>
          {install && <button className="btn btn-secondary btn-block mt-2" onClick={async () => { install.prompt(); await install.userChoice; setInstall(null); }}>Install app</button>}
          {!install && isIos && <div className="install-hint">To install: tap Share, then “Add to Home Screen”.</div>}
          <div className="login-foot">© Yopsa Microfinance<br />Pamoja complements the Yopsa core ledger and never replaces it.</div>
        </div>
      </div>
      <Modal open={!!help} onClose={() => setHelp(null)} title={help === 'privacy' ? 'Privacy notice' : 'Forgot password'}>
        {help === 'privacy' ? (
          <div className="small">
            <p>Pamoja collects only what is needed to manage group membership, meetings, savings and loan records for Yopsa Microfinance.</p>
            <p>We do not use facial recognition, continuous location tracking or sell your data. Consent is recorded when a member is registered. Financial and audit records are retained as required by policy.</p>
            <p>Your data is visible only to the Yopsa staff and group officers whose role and scope allow it. Contact your Yopsa branch for access or correction requests.</p>
          </div>
        ) : (
          <div className="small">
            <p>Password reset is handled by your Pamoja administrator. Contact your Yopsa branch or IT administrator to reset your password.</p>
            <p>For security, this page never reveals whether an account exists.</p>
          </div>
        )}
      </Modal>
    </div>
  );
}
