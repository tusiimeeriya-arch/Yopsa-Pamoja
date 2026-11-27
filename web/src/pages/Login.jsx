import React, { useEffect, useState } from 'react';
import { Link, Navigate, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth.jsx';
import { post } from '../api.js';
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
  const [resetEmail, setResetEmail] = useState('');
  const [resetMessage, setResetMessage] = useState('');
  const [resetBusy, setResetBusy] = useState(false);

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

  const requestReset = async (e) => {
    e.preventDefault();
    setResetBusy(true);
    try {
      const r = await post('/auth/password-reset', { email: resetEmail.trim() });
      setResetMessage(r.message);
    } catch (err) { setResetMessage(err.message); }
    finally { setResetBusy(false); }
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
            <Link className="link-btn" to="/privacy">Privacy notice</Link>
          </div>
          {install && <button className="btn btn-secondary btn-block mt-2" onClick={async () => { install.prompt(); await install.userChoice; setInstall(null); }}>Install app</button>}
          {!install && isIos && <div className="install-hint">To install: tap Share, then “Add to Home Screen”.</div>}
          <div className="login-foot">© Yopsa Microfinance<br />Pamoja complements the Yopsa core ledger and never replaces it.</div>
        </div>
      </div>
      <Modal open={help === 'forgot'} onClose={() => { setHelp(null); setResetMessage(''); }} title="Forgot password">
        <form onSubmit={requestReset} className="small">
          <p>Enter the email you use for Pamoja. An administrator will verify who you are and help you reset the password.</p>
          <div className="field">
            <label className="label" htmlFor="reset-email">Email</label>
            <input id="reset-email" className="input" type="email" value={resetEmail} onChange={(e) => setResetEmail(e.target.value)} required />
          </div>
          {resetMessage && <div className="alert info" role="status">{resetMessage}</div>}
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={() => { setHelp(null); setResetMessage(''); }}>Close</button><button className="btn" disabled={resetBusy || !resetEmail.trim()}>{resetBusy ? 'Sending…' : 'Request reset'}</button></div>
          <p className="muted">For security, this page never reveals whether an account exists.</p>
        </form>
      </Modal>
    </div>
  );
}
