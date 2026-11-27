import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { api, post } from './api.js';
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
  const [stepUp, setStepUp] = useState(null);
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

  const runSensitive = useCallback(async (fn) => {
    try {
      return await fn();
    } catch (err) {
      if (err.code !== 'STEP_UP_REQUIRED') throw err;
      const password = await new Promise((resolve) => {
        resolver.current = resolve;
        setStepUp({ open: true });
      });
      await post('/auth/step-up', { password });
      setStepUp(null);
      return fn();
    }
  }, []);

  const submitStepUp = async (password) => {
    try {
      await post('/auth/step-up', { password });
      setStepUp(null);
      resolver.current?.(password);
    } catch (err) {
      setStepUp((s) => ({ ...s, error: err.message }));
    }
  };

  const cancelStepUp = () => {
    setStepUp(null);
    resolver.current?.(null);
  };

  return (
    <AuthContext.Provider value={{ user, roles, nav, isAdmin, demoMode, loading, idleMessage, login, logout, loadMe, runSensitive, splash, splashFade }}>
      {children}
      {stepUp?.open && <StepUpModal error={stepUp.error} onSubmit={submitStepUp} onCancel={cancelStepUp} />}
    </AuthContext.Provider>
  );
}

function StepUpModal({ onSubmit, onCancel, error }) {
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true" aria-label="Confirm your password">
      <div className="modal" style={{ maxWidth: 420 }}>
        <div className="modal-head"><h2 className="card-title">Confirm your password</h2><button className="modal-close" onClick={onCancel} aria-label="Close">×</button></div>
        <p className="muted small">This is a sensitive action. Re-confirm your password to continue.</p>
        <form onSubmit={async (e) => { e.preventDefault(); setBusy(true); await onSubmit(password); setBusy(false); }}>
          <div className="field"><label className="label" htmlFor="stepup-pw">Password</label><input id="stepup-pw" className="input" type="password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} required /></div>
          {error && <div className="error-text mb-2">{error}</div>}
          <div className="row end"><button type="button" className="btn btn-secondary" onClick={onCancel}>Cancel</button><button className="btn" disabled={busy || !password}>{busy ? 'Confirming…' : 'Confirm'}</button></div>
        </form>
      </div>
    </div>
  );
}

export function useAuth() { return useContext(AuthContext); }
