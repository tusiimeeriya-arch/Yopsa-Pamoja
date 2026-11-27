import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { api, post, setOtpCode, ApiError } from './api.js';
import { clearDrafts } from './drafts.js';

const AuthContext = createContext(null);

export function AuthProvider({ children }) {
  const [user, setUser] = useState(null);
  const [roles, setRoles] = useState([]);
  const [nav, setNav] = useState([]);
  const [isAdmin, setIsAdmin] = useState(false);
  const [demoMode, setDemoMode] = useState(false);
  const [loading, setLoading] = useState(true);
  const [idleMessage, setIdleMessage] = useState('');
  const [otp, setOtp] = useState(null);
  const resolver = useRef(null);
  const [splash, setSplash] = useState(() => !sessionStorage.getItem('pamoja:splash'));
  const [splashFade, setSplashFade] = useState(false);

  const loadMe = useCallback(async () => {
    try {
      const me = await api('/auth/me');
      setUser(me.user); setRoles(me.roles || []); setNav(me.nav || []); setIsAdmin(!!me.isAdmin); setDemoMode(!!me.demoMode);
      return me.user;
    } catch {
      setUser(null); setRoles([]); setNav([]); setIsAdmin(false);
      return null;
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    const started = Date.now();
    const maxTimer = setTimeout(() => { if (!cancelled) setSplashFade(true); }, 2500);
    (async () => {
      await loadMe();
      const elapsed = Date.now() - started;
      await new Promise((r) => setTimeout(r, Math.max(0, 1200 - elapsed)));
      if (cancelled) return;
      setLoading(false);
      if (splash) {
        setSplashFade(true);
        setTimeout(() => { if (!cancelled) { setSplash(false); sessionStorage.setItem('pamoja:splash', '1'); } }, 400);
      }
    })();
    return () => { cancelled = true; clearTimeout(maxTimer); };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const onIdle = () => {
      clearAll();
      setIdleMessage('You were signed out for security after a period of inactivity.');
    };
    window.addEventListener('pamoja:idle', onIdle);
    return () => window.removeEventListener('pamoja:idle', onIdle);
  }, []);

  const clearAll = () => {
    setUser(null); setRoles([]); setNav([]); setIsAdmin(false);
    clearDrafts(user?.id);
  };

  const login = async (email, password) => {
    setIdleMessage('');
    await post('/auth/login', { email, password });
    await loadMe();
  };

  const logout = async () => {
    try { await post('/auth/logout', {}); } catch { /* already gone */ }
    clearDrafts(user?.id);
    setUser(null); setRoles([]); setNav([]); setIsAdmin(false);
  };

  /**
   * Sensitive actions need a one-time code for the exact purpose (verify,
   * resolve, post, correction, approve, disburse, role_change, export, policy,
   * purge, snapshot). The code is requested, confirmed, then sent with the
   * retried request only.
   */
  const runSensitive = useCallback(async (fn, purpose = 'sensitive') => {
    try {
      return await fn();
    } catch (err) {
      if (!['OTP_REQUIRED', 'OTP_INVALID'].includes(err.code)) throw err;
      const req = await post('/auth/otp/request', { purpose });
      const code = await new Promise((resolve) => {
        resolver.current = resolve;
        setOtp({ open: true, purpose, devCode: req.devCode });
      });
      if (!code) throw new ApiError('Cancelled.', 'OTP_CANCELLED', 0);
      setOtpCode(code);
      try {
        return await fn();
      } finally {
        setOtpCode(null);
        setOtp(null);
      }
    }
  }, []);

  const submitOtp = (code) => {
    resolver.current?.(code);
  };
  const cancelOtp = () => {
    setOtp(null);
    resolver.current?.(null);
  };

  return (
    <AuthContext.Provider value={{ user, roles, nav, isAdmin, demoMode, loading, idleMessage, login, logout, loadMe, runSensitive, splash, splashFade }}>
      {children}
      {otp?.open && <OtpModal purpose={otp.purpose} devCode={otp.devCode} onSubmit={submitOtp} onCancel={cancelOtp} />}
    </AuthContext.Provider>
  );
}

function OtpModal({ purpose, devCode, onSubmit, onCancel }) {
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const label = String(purpose).replace(/_/g, ' ');
  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true" aria-label="Confirm with a one-time code">
      <div className="modal" style={{ maxWidth: 430 }}>
        <div className="modal-head"><h2 className="card-title">Confirm with a one-time code</h2><button className="modal-close" onClick={onCancel} aria-label="Close">×</button></div>
        <p className="muted small">A six-digit code was sent to you for this {label} action. Enter it to continue.</p>
        {devCode && <div className="alert info small">This environment has no SMS provider. Your code is <strong>{devCode}</strong>.</div>}
        <form onSubmit={(e) => {
          e.preventDefault();
          if (!/^\d{6}$/.test(code)) { setError('Enter the six-digit code.'); return; }
          onSubmit(code);
        }}>
          <div className="field">
            <label className="label" htmlFor="otp-code">One-time code</label>
            <input id="otp-code" className="input" inputMode="numeric" autoComplete="one-time-code" maxLength={6} autoFocus value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))} />
          </div>
          {error && <div className="error-text mb-2">{error}</div>}
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={onCancel}>Cancel</button><button className="btn" disabled={code.length !== 6}>Confirm</button></div>
        </form>
      </div>
    </div>
  );
}

export function useAuth() { return useContext(AuthContext); }
