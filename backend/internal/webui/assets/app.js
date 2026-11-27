/* ==========================================================================
   Yopsa Pamoja — web client
   A dependency-free single-page app over the Pamoja JSON API. All rules live
   on the server; this file renders, navigates and calls the API.
   ========================================================================== */

'use strict';

/* ---------- tiny utilities ------------------------------------------------ */

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

function esc(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

const nf = new Intl.NumberFormat('en-UG', { maximumFractionDigits: 0 });

function num(value) { return nf.format(Number(value || 0)); }
function ugx(value) { return 'UGX ' + num(value); }
function compact(value) {
  const n = Number(value || 0);
  if (n >= 1e9) return 'UGX ' + (n / 1e9).toFixed(n >= 1e10 ? 0 : 1) + 'B';
  if (n >= 1e6) return 'UGX ' + (n / 1e6).toFixed(n >= 1e7 ? 0 : 1) + 'M';
  if (n >= 1e3) return 'UGX ' + (n / 1e3).toFixed(n >= 1e4 ? 0 : 1) + 'K';
  return 'UGX ' + num(n);
}

function parseDate(value) {
  if (!value) return null;
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(value);
  if (m) return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  const d = new Date(value);
  return isNaN(d.getTime()) ? null : d;
}
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
function fmtDate(value) {
  const d = parseDate(value);
  return d ? `${d.getDate()} ${MONTHS[d.getMonth()]} ${d.getFullYear()}` : '—';
}
function fmtDateTime(value) {
  const d = parseDate(value);
  if (!d) return '—';
  const time = d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
  return `${d.getDate()} ${MONTHS[d.getMonth()]} ${d.getFullYear()}, ${time}`;
}
function dateLong(d) {
  return d.toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' });
}
function greeting() {
  const h = new Date().getHours();
  return h < 12 ? 'Good morning' : h < 17 ? 'Good afternoon' : 'Good evening';
}
function initials(name) {
  return String(name || '?').trim().split(/\s+/).slice(0, 2).map(w => w[0]).join('').toUpperCase();
}
function firstName(name) { return String(name || '').trim().split(/\s+/)[0] || 'there'; }
function todayISO() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}
function plural(n, word) { return `${num(n)} ${word}${Number(n) === 1 ? '' : 's'}`; }

const ICONS = {
  dashboard: '<rect x="3" y="3" width="7" height="9" rx="1.5"/><rect x="14" y="3" width="7" height="5" rx="1.5"/><rect x="14" y="12" width="7" height="9" rx="1.5"/><rect x="3" y="16" width="7" height="5" rx="1.5"/>',
  groups: '<path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>',
  loans: '<path d="M12 2v20"/><path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/>',
  tasks: '<path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>',
  reports: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/><path d="M8 13h8M8 17h5"/>',
  audit: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><path d="M9 12l2 2 4-4"/>',
  users: '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="M21 21l-4.3-4.3"/>',
  refresh: '<path d="M21 12a9 9 0 1 1-2.64-6.36"/><path d="M21 3v6h-6"/>',
  logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="M16 17l5-5-5-5"/><path d="M21 12H9"/>',
  menu: '<path d="M3 6h18M3 12h18M3 18h18"/>',
  close: '<path d="M18 6L6 18M6 6l12 12"/>',
  chevron: '<path d="M9 18l6-6-6-6"/>',
  arrow: '<path d="M5 12h14M13 6l6 6-6 6"/>',
  check: '<path d="M20 6L9 17l-5-5"/>',
  checkCircle: '<circle cx="12" cy="12" r="9"/><path d="M8 12l3 3 5-6"/>',
  x: '<path d="M18 6L6 18M6 6l12 12"/>',
  download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="M7 10l5 5 5-5"/><path d="M12 15V3"/>',
  calendar: '<rect x="3" y="4" width="18" height="18" rx="2"/><path d="M16 2v4M8 2v4M3 10h18"/>',
  mapPin: '<path d="M21 10c0 7-9 12-9 12S3 17 3 10a9 9 0 0 1 18 0z"/><circle cx="12" cy="10" r="3"/>',
  alert: '<path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><path d="M12 9v4M12 17h.01"/>',
  wallet: '<path d="M21 12V7H5a2 2 0 0 1 0-4h14v4"/><path d="M3 5v14a2 2 0 0 0 2 2h16v-5"/><path d="M18 12a2 2 0 0 0 0 4h4v-4z"/>',
  trend: '<path d="M23 6l-9.5 9.5-5-5L1 18"/><path d="M17 6h6v6"/>',
  shield: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>',
  lock: '<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
  user: '<path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 16v-4M12 8h.01"/>',
  building: '<rect x="4" y="2" width="16" height="20" rx="2"/><path d="M9 22v-4h6v4"/><path d="M8 6h.01M12 6h.01M16 6h.01M8 10h.01M12 10h.01M16 10h.01M8 14h.01M12 14h.01M16 14h.01"/>',
  phone: '<path d="M22 16.92v3a2 2 0 0 1-2.18 2 19.79 19.79 0 0 1-8.63-3.07 19.5 19.5 0 0 1-6-6A19.79 19.79 0 0 1 2.09 4.18 2 2 0 0 1 4.11 2h3a2 2 0 0 1 2 1.72c.13.96.36 1.9.7 2.81a2 2 0 0 1-.45 2.11L8.09 9.91a16 16 0 0 0 6 6l1.27-1.27a2 2 0 0 1 2.11-.45c.9.34 1.85.57 2.81.7A2 2 0 0 1 22 16.92z"/>',
  print: '<path d="M6 9V2h12v7"/><path d="M6 18H4a2 2 0 0 1-2-2v-5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2h-2"/><rect x="6" y="14" width="12" height="8"/>',
};

function icon(name, cls = '') {
  const body = ICONS[name] || ICONS.info;
  return `<svg class="${cls}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${body}</svg>`;
}

/* ---------- vocabulary ---------------------------------------------------- */

const GROUP_TYPES = {
  vsla: 'VSLA', investment_club: 'Investment club', association: 'Association',
  cooperative: 'Cooperative', sacco: 'SACCO',
};
const KIND_LABELS = {
  savings: 'Savings', shares: 'Shares', social_fund: 'Social fund',
  loan_repayment: 'Loan repayment', fine: 'Fine',
};
const METHOD_LABELS = { cash: 'Cash', mobile_money: 'Mobile money', bank: 'Bank' };

const STATUS = {
  active: ['green', 'Active'], pending_activation: ['amber', 'Pending activation'],
  suspended: ['red', 'Suspended'], closed: ['grey', 'Closed'], draft: ['grey', 'Draft'],
  submitted: ['amber', 'Submitted'], verified: ['green', 'Verified'], returned: ['red', 'Returned'],
  appraised: ['amber', 'Appraised'], approved: ['green', 'Approved'], declined: ['red', 'Declined'],
  disbursed: ['green', 'Disbursed'], operations_reviewed: ['amber', 'Operations reviewed'],
  offered: ['amber', 'Offer made'], accepted: ['green', 'Offer accepted'],
  open: ['blue', 'Open'], done: ['green', 'Done'], cancelled: ['grey', 'Cancelled'],
  present: ['green', 'Present'], absent: ['red', 'Absent'], excused: ['amber', 'Excused'], late: ['amber', 'Late'],
  inactive: ['grey', 'Inactive'], exited: ['grey', 'Exited'],
};

function statusBadge(status, labelOverride) {
  const [tone, label] = STATUS[status] || ['grey', status];
  const cls = tone === 'blue' ? '' : tone;
  return `<span class="badge ${cls}">${esc(labelOverride || label)}</span>`;
}
function typeLabel(t) { return GROUP_TYPES[t] || t; }
function kindLabel(k) { return KIND_LABELS[k] || k; }
function methodLabel(m) { return METHOD_LABELS[m] || m; }
function roleLabel(r) { return String(r || '').replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase()); }
function scopeLabel(s) {
  return { legal_entity: 'whole entity', branch: 'branch', group: 'group', platform: 'platform', self: 'self' }[s] || s;
}

/* ---------- state + api --------------------------------------------------- */

const state = {
  access: localStorage.getItem('pamoja.access') || '',
  refresh: localStorage.getItem('pamoja.refresh') || '',
  me: null,
  groups: [],
  groupDetail: null,
  groupTab: 'overview',
  auditOffset: 0,
  shell: false,
  loginBusy: false,
};

function setTokens(data) {
  state.access = data.access_token || '';
  state.refresh = data.refresh_token || '';
  localStorage.setItem('pamoja.access', state.access);
  localStorage.setItem('pamoja.refresh', state.refresh);
}
function clearTokens() {
  state.access = ''; state.refresh = ''; state.me = null;
  localStorage.removeItem('pamoja.access');
  localStorage.removeItem('pamoja.refresh');
}
function can(permission) { return !!(state.me && state.me.can && state.me.can[permission]); }

async function refreshTokens() {
  if (!state.refresh) return false;
  try {
    const res = await fetch('/api/v1/auth/refresh', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: state.refresh, device_id: 'web' }),
    });
    if (!res.ok) return false;
    setTokens(await res.json());
    return true;
  } catch (_) { return false; }
}

async function api(path, options = {}) {
  const opts = Object.assign({ method: 'GET' }, options);
  const send = async () => {
    const headers = Object.assign({}, opts.headers);
    if (state.access) headers.Authorization = 'Bearer ' + state.access;
    if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
    return fetch('/api/v1' + path, {
      method: opts.method,
      headers,
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
      cache: 'no-store',
    });
  };

  let res = await send();
  if (res.status === 401 && state.refresh && !opts.noRetry && path !== '/auth/refresh') {
    if (await refreshTokens()) res = await send();
  }
  if (!res.ok) {
    let payload = {};
    try { payload = await res.json(); } catch (_) { /* not JSON */ }
    const err = new Error(payload.message || friendlyError(res.status, payload.error));
    err.status = res.status;
    err.code = payload.error || '';
    if (res.status === 401 && !opts.noRetry && path !== '/auth/login') {
      clearTokens();
    }
    throw err;
  }
  if (opts.raw) return res;
  const text = await res.text();
  return text ? JSON.parse(text) : null;
}

function friendlyError(status, code) {
  if (status === 403) return 'Your role does not allow this action.';
  if (status === 404) return 'That record could not be found.';
  if (status === 429) return 'Too many attempts. Please wait a moment.';
  if (status >= 500) return 'The server hit a problem. Please try again.';
  if (code === 'invalid_credentials') return 'Those sign-in details did not match.';
  return 'Something went wrong. Please try again.';
}

/* ---------- toast + modal ------------------------------------------------- */

function toast(message, type = 'success') {
  const root = document.getElementById('toast-root');
  if (!root) return;
  const el = document.createElement('div');
  el.className = 'toast ' + type;
  el.innerHTML = `${icon(type === 'error' ? 'alert' : 'checkCircle')}<span>${esc(message)}</span>`;
  root.appendChild(el);
  setTimeout(() => {
    el.style.opacity = '0';
    el.style.transform = 'translateY(6px)';
    el.style.transition = 'all .2s';
    setTimeout(() => el.remove(), 220);
  }, 3800);
}

function openModal({ title, subtitle = '', body, submitLabel = 'Save', submitClass = 'btn-primary', wide = false, onSubmit }) {
  const root = document.getElementById('modal-root');
  root.innerHTML = `
    <div class="modal-backdrop">
      <div class="modal ${wide ? 'wide' : ''}" role="dialog" aria-modal="true" aria-label="${esc(title)}">
        <div class="modal-head">
          <div><h2>${esc(title)}</h2>${subtitle ? `<p>${esc(subtitle)}</p>` : ''}</div>
          <button class="icon-btn" type="button" data-action="close-modal" aria-label="Close">${icon('close')}</button>
        </div>
        <form id="modal-form" novalidate>
          <div class="modal-body">${body}</div>
          <div class="modal-foot">
            <button type="button" class="btn btn-secondary" data-action="close-modal">Cancel</button>
            ${submitLabel ? `<button type="submit" class="btn ${submitClass}">${esc(submitLabel)}</button>` : ''}
          </div>
        </form>
      </div>
    </div>`;

  const form = document.getElementById('modal-form');
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const btn = form.querySelector('button[type="submit"]');
    if (btn) { btn.disabled = true; btn.dataset.label = btn.textContent; btn.textContent = 'Working…'; }
    try {
      await onSubmit(new FormData(form), form, event.submitter);
    } catch (err) {
      toast(err.message || 'Something went wrong', 'error');
    } finally {
      if (btn) { btn.disabled = false; btn.textContent = btn.dataset.label || submitLabel; }
    }
  });

  const backdrop = $('.modal-backdrop', root);
  backdrop.addEventListener('mousedown', (event) => {
    if (event.target === backdrop) closeModal();
  });
  const first = form.querySelector('input:not([type=hidden]), select, textarea');
  if (first) setTimeout(() => first.focus(), 40);
}

function closeModal() { document.getElementById('modal-root').innerHTML = ''; }

/* ---------- login --------------------------------------------------------- */

const DEMO_ACCOUNTS = [
  ['Grace Namusoke', 'Management', '+256700000001'],
  ['Daniel Kato', 'Field officer', '+256700000002'],
  ['Sarah Auma', 'Verifier', '+256700000003'],
  ['Robert Mugisha', 'Credit approver', '+256700000004'],
  ['Esther Nabirye', 'Treasurer', '+256700000005'],
  ['Joan Kembabazi', 'Secretary', '+256700000008'],
  ['Peter Byaruhanga', 'Chairperson', '+256700000009'],
  ['Moses Okello', 'IT admin', '+256700000006'],
  ['Patricia Atim', 'Auditor', '+256700000007'],
];

function renderLogin() {
  document.getElementById('root').innerHTML = `
  <div class="login">
    <section class="login-brand">
      <div class="brand">
        <div class="brand-mark">Y</div>
        <div class="brand-text"><strong>Yopsa</strong><span>Pamoja</span></div>
      </div>
      <div class="login-copy">
        <h1>Stronger groups.<br><em>Informed decisions.</em></h1>
        <p>One clear view of every VSLA, SACCO and investment club: meetings, savings, borrowing and the evidence behind every number.</p>
        <div class="login-points">
          <div class="login-point"><i>${icon('calendar')}</i> Meeting registers and attendance</div>
          <div class="login-point"><i>${icon('shield')}</i> Savings verified under separation of duties</div>
          <div class="login-point"><i>${icon('trend')}</i> Group lending from appraisal to disbursement</div>
        </div>
      </div>
      <div class="login-foot">Yopsa Microfinance Uganda · Internal test build</div>
    </section>

    <section class="login-panel">
      <div class="login-card">
        <h2>Welcome back</h2>
        <p>Sign in with the phone number registered with Yopsa.</p>
        <div class="login-error" id="login-error">${icon('alert')}<span></span></div>
        <form id="login-form" novalidate>
          <div class="field">
            <label for="phone">Phone number</label>
            <input class="input" id="phone" name="phone" autocomplete="username" inputmode="tel" placeholder="+256 700 000 001" required>
          </div>
          <div class="field">
            <label for="password">Password</label>
            <input class="input" id="password" name="password" type="password" autocomplete="current-password" placeholder="Your password" required>
          </div>
          <div class="field" id="totp-field" style="display:none">
            <label for="totp">Authenticator code</label>
            <input class="input" id="totp" name="totp" inputmode="numeric" autocomplete="one-time-code" placeholder="6-digit code">
          </div>
          <button class="btn btn-primary" type="submit">Sign in</button>
        </form>

        <div class="demo-block">
          <h3>Test accounts</h3>
          <div class="demo-grid">
            ${DEMO_ACCOUNTS.map(([name, role, phone]) => `
              <button type="button" class="demo-btn" data-action="demo" data-phone="${esc(phone)}">
                <strong>${esc(role)}</strong><span>${esc(name)}</span>
              </button>`).join('')}
          </div>
          <p class="demo-note">Password for every test account: <code>Pamoja#2026</code></p>
        </div>
      </div>
    </section>
  </div>`;

  const form = document.getElementById('login-form');
  form.addEventListener('submit', submitLogin);
  form.phone.focus();
}

function showLoginError(message) {
  const box = document.getElementById('login-error');
  if (!box) return;
  box.classList.add('show');
  box.querySelector('span').textContent = message;
}

async function submitLogin(event) {
  event.preventDefault();
  if (state.loginBusy) return;
  const form = event.currentTarget;
  const btn = form.querySelector('button[type="submit"]');
  const phone = form.phone.value.trim();
  const password = form.password.value;
  if (!phone || !password) { showLoginError('Enter your phone number and password.'); return; }

  state.loginBusy = true;
  btn.disabled = true;
  btn.textContent = 'Signing in…';
  $('#login-error').classList.remove('show');

  try {
    const data = await api('/auth/login', {
      method: 'POST',
      noRetry: true,
      body: { phone, password, totp_code: form.totp ? form.totp.value.trim() : '', device_id: 'web' },
    });
    setTokens(data);
    state.me = await api('/me');
    location.hash = defaultRoute();
    showApp();
  } catch (err) {
    if (err.code === 'mfa_required') {
      document.getElementById('totp-field').style.display = 'flex';
      showLoginError('Enter the 6-digit code from your authenticator app.');
      form.totp.focus();
    } else if (err.code === 'too_many_requests') {
      showLoginError('Too many attempts from this device. Wait a minute and try again.');
    } else if (err.code === 'invalid_credentials') {
      showLoginError('That phone number and password did not match.');
    } else {
      showLoginError(err.message || 'Sign-in failed. Please try again.');
    }
  } finally {
    state.loginBusy = false;
    btn.disabled = false;
    btn.textContent = 'Sign in';
  }
}

async function doLogout() {
  try { await api('/auth/logout', { method: 'POST', noRetry: true }); } catch (_) { /* session may be gone */ }
  clearTokens();
  state.shell = false;
  closeModal();
  document.getElementById('root').innerHTML = '';
  renderLogin();
  if (location.hash) location.hash = '';
}

/* ---------- app shell ----------------------------------------------------- */

const NAV = [
  { label: 'Overview', items: [{ id: 'dashboard', label: 'Dashboard', icon: 'dashboard', perm: 'dashboard.view' }] },
  {
    label: 'Operations', items: [
      { id: 'groups', label: 'Groups', icon: 'groups', permAny: ['registration.view', 'meetings.view', 'payments.view', 'loans.view', 'tasks.view'] },
      { id: 'loans', label: 'Loans', icon: 'loans', perm: 'loans.view' },
      { id: 'tasks', label: 'Tasks', icon: 'tasks', perm: 'tasks.view' },
    ],
  },
  {
    label: 'Insight', items: [
      { id: 'reports', label: 'Reports', icon: 'reports', perm: 'reports.view' },
      { id: 'audit', label: 'Audit log', icon: 'audit', perm: 'audit.view' },
      { id: 'users', label: 'Users & roles', icon: 'users', perm: 'users_roles.view' },
    ],
  },
];

function navItems() {
  return NAV.map(group => {
    const items = group.items.filter(item => (item.permAny ? item.permAny.some(can) : can(item.perm)));
    if (!items.length) return '';
    return `<p class="nav-label">${group.label}</p>` + items.map(item => `
      <a href="#/${item.id}" data-nav="${item.id}">${icon(item.icon)}<span>${item.label}</span></a>`).join('');
  }).join('');
}

function showApp() {
  if (!state.shell) {
    document.getElementById('root').innerHTML = `
      <div class="app">
        <aside class="sidebar" id="sidebar">
          <div class="brand">
            <div class="brand-mark">Y</div>
            <div class="brand-text"><strong>Yopsa</strong><span>Pamoja</span></div>
          </div>
          <nav class="nav">${navItems()}</nav>
          <div class="sidebar-foot">
            <a class="user-chip" href="#/profile">
              <span class="avatar">${esc(initials(state.me.full_name))}</span>
              <span class="user-chip-text"><strong>${esc(state.me.full_name)}</strong><span>${esc(primaryRole())}</span></span>
            </a>
            <button class="btn btn-ghost btn-sm" style="width:100%;margin-top:6px" data-action="logout">${icon('logout')} Sign out</button>
          </div>
        </aside>
        <div class="main">
          <header class="topbar" id="topbar">
            <button class="icon-btn menu-btn" data-action="open-nav" aria-label="Open navigation">${icon('menu')}</button>
            <div class="crumb" id="crumb"></div>
            <div class="topbar-actions">
              <span class="env-chip">Test build</span>
              <a class="avatar" href="#/profile" title="${esc(state.me.full_name)}">${esc(initials(state.me.full_name))}</a>
            </div>
          </header>
          <main id="view" class="view"></main>
        </div>
      </div>`;
    state.shell = true;
    window.addEventListener('scroll', () => {
      const bar = document.getElementById('topbar');
      if (bar) bar.classList.toggle('scrolled', window.scrollY > 6);
    }, { passive: true });
  }
  route();
}

function primaryRole() {
  const roles = (state.me && state.me.roles) || [];
  return roles.length ? roleLabel(roles[0].role) : 'Member';
}

function defaultRoute() {
  if (can('dashboard.view')) return '#/dashboard';
  if (can('registration.view')) return '#/groups';
  if (can('tasks.view')) return '#/tasks';
  return '#/profile';
}

/* ---------- router -------------------------------------------------------- */

function currentRoute() {
  const hash = location.hash.replace(/^#/, '');
  const parts = hash.split('/').filter(Boolean);
  return { page: parts[0] || 'dashboard', parts };
}

function route() {
  if (!state.me) return;
  const { page, parts } = currentRoute();

  if (page === 'dashboard' && !can('dashboard.view')) { location.hash = defaultRoute(); return; }

  $$('[data-nav]').forEach(a => a.classList.toggle('active', a.dataset.nav === page));
  const crumb = document.getElementById('crumb');
  if (crumb) {
    const titles = {
      dashboard: 'Dashboard', groups: 'Groups', loans: 'Loans', tasks: 'Tasks',
      reports: 'Reports', audit: 'Audit log', users: 'Users & roles', profile: 'Your profile',
    };
    crumb.innerHTML = `<span>Pamoja</span>${icon('chevron')}<strong>${titles[page] || 'Not found'}</strong>`;
  }
  document.querySelector('.sidebar')?.classList.remove('open');

  const view = document.getElementById('view');
  view.innerHTML = skeleton();
  window.scrollTo({ top: 0, behavior: 'instant' });

  const run = {
    dashboard: pageDashboard,
    groups: () => (parts[1] ? pageGroup(parts[1]) : pageGroups()),
    loans: pageLoans,
    tasks: pageTasks,
    reports: pageReports,
    audit: pageAudit,
    users: pageUsers,
    profile: pageProfile,
  }[page];

  (run ? run() : Promise.resolve(pageNotFound()))
    .catch(err => {
      if (err.status === 401) { clearTokens(); state.shell = false; renderLogin(); return; }
      view.innerHTML = errorPanel(err);
    });
}

function skeleton() {
  return `<div class="skeleton"><div class="sk-cards">
    <div class="sk-card"></div><div class="sk-card"></div><div class="sk-card"></div><div class="sk-card"></div>
  </div><div class="sk-line" style="width:38%;height:22px"></div><div class="sk-line"></div><div class="sk-line" style="width:74%"></div></div>`;
}

function errorPanel(err) {
  const message = err.status === 403
    ? 'Your role does not include access to this screen.'
    : err.message || 'This screen could not load.';
  return `<div class="error-panel">
    <div class="empty-icon">${icon('alert')}</div>
    <h2>We could not open this screen</h2>
    <p class="muted" style="margin:8px 0 20px">${esc(message)}</p>
    <button class="btn btn-secondary" data-action="refresh">${icon('refresh')} Try again</button>
  </div>`;
}

function pageNotFound() {
  const view = document.getElementById('view');
  view.innerHTML = `<div class="empty">
    <div class="empty-icon">${icon('search')}</div>
    <h3>Page not found</h3>
    <p>That address does not exist in Pamoja.</p>
    <p style="margin-top:18px"><a class="btn btn-secondary" href="#/dashboard">Back to the dashboard</a></p>
  </div>`;
}

/* ---------- shared fragments --------------------------------------------- */

function pageHead(eyebrow, title, sub, actions = '') {
  return `<div class="page-head">
    <div>
      <p class="eyebrow">${esc(eyebrow)}</p>
      <h1>${esc(title)}</h1>
      ${sub ? `<p class="sub">${sub}</p>` : ''}
    </div>
    ${actions ? `<div class="head-actions">${actions}</div>` : ''}
  </div>`;
}

function statCard(iconName, tone, label, value, foot) {
  return `<article class="stat">
    <div class="stat-top"><span class="stat-icon ${tone}">${icon(iconName)}</span><span class="stat-label">${esc(label)}</span></div>
    <div class="stat-value">${value}</div>
    <div class="stat-foot">${foot}</div>
  </article>`;
}

function emptyState(iconName, title, text, action = '') {
  return `<div class="empty">
    <div class="empty-icon">${icon(iconName)}</div>
    <h3>${esc(title)}</h3>
    <p>${esc(text)}</p>
    ${action ? `<p style="margin-top:18px">${action}</p>` : ''}
  </div>`;
}

function miniBar(percent) {
  const p = Math.max(0, Math.min(100, Number(percent) || 0));
  return `<span style="display:inline-flex;align-items:center;gap:8px">
    <span style="width:58px;height:6px;border-radius:999px;background:#EDF1F6;overflow:hidden;display:inline-block">
      <span style="display:block;height:100%;width:${p}%;background:var(--green-600)"></span>
    </span>
    <span class="small mono">${p}%</span></span>`;
}

function avatarCell(name, sub) {
  return `<div class="cell-main">
    <span class="avatar">${esc(initials(name))}</span>
    <div><div class="strong">${esc(name)}</div>${sub ? `<div class="table-sub">${esc(sub)}</div>` : ''}</div>
  </div>`;
}

/* ---------- dashboard ----------------------------------------------------- */

async function pageDashboard() {
  const data = await api('/dashboard');
  const t = data.totals;
  const view = document.getElementById('view');
  const maxSavings = Math.max(1, ...data.monthly_savings.map(p => p.amount_ugx));

  view.innerHTML = `
    ${pageHead(greeting(), `${firstName(state.me.full_name)}, here is the portfolio today`,
      `${dateLong(new Date())} · ${plural(t.groups, 'group')} in view`,
      `${can('registration.create') ? `<button class="btn btn-primary" data-action="new-group">${icon('plus')} New group</button>` : ''}
       <button class="btn btn-secondary" data-action="refresh">${icon('refresh')} Refresh</button>`)}

    <div class="stat-grid">
      ${statCard('groups', '', 'Groups', num(t.groups), `${plural(t.members, 'member')} across the portfolio`)}
      ${statCard('wallet', 'green', 'Verified savings', compact(t.savings_ugx), 'Savings and shares confirmed')}
      ${statCard('loans', 'amber', 'Outstanding loans', compact(t.outstanding_ugx), `${plural(t.active_loans, 'active loan')}`)}
      ${statCard('checkCircle', 'green', 'Attendance', `${t.attendance_rate}%`, 'Average across meeting registers')}
    </div>

    <div class="grid-2">
      <section class="card">
        <div class="card-head">
          <div><h2>Savings momentum</h2><p>Verified savings and shares, last six months</p></div>
          <span class="badge green plain">${compact(data.monthly_savings.reduce((s, p) => s + p.amount_ugx, 0))} total</span>
        </div>
        <div class="chart-wrap">
          <div class="chart">
            ${data.monthly_savings.map((p, i) => `
              <div class="bar-col" title="${ugx(p.amount_ugx)}">
                <div class="bar-track"><div class="bar ${i === data.monthly_savings.length - 1 ? 'green' : ''}" style="height:${Math.max(3, Math.round(p.amount_ugx / maxSavings * 100))}%"></div></div>
                <span>${esc(p.label)}</span>
              </div>`).join('')}
          </div>
        </div>
      </section>

      <section class="card">
        <div class="card-head">
          <div><h2>Needs attention</h2><p>${t.pending_verifications} entries to verify · ${t.pending_loans} loan decisions</p></div>
        </div>
        <div class="attention">
          ${data.attention.length ? data.attention.map(a => `
            <a class="attention-item" href="#${esc(a.link || '/dashboard')}">
              <span class="attention-icon ${a.kind === 'loan' ? 'loan' : ''}">${icon(a.kind === 'loan' ? 'loans' : 'checkCircle')}</span>
              <span class="attention-body"><strong>${esc(a.title)}</strong><span>${esc(a.detail)}</span></span>
              ${icon('chevron')}
            </a>`).join('') : `<div class="empty" style="padding:36px 20px">
              <div class="empty-icon">${icon('checkCircle')}</div>
              <h3>All clear</h3><p>Nothing is waiting for your role right now.</p></div>`}
        </div>
      </section>
    </div>

    <div class="section-title">
      <h2>Group portfolio</h2>
      <a class="link" href="#/groups">All groups ${icon('arrow')}</a>
    </div>
    <section class="card">
      <div class="table-wrap">
        <table class="data">
          <thead><tr><th>Group</th><th class="hide-sm">Branch</th><th class="num">Members</th><th class="num">Savings</th><th class="hide-sm">Attendance</th><th>Status</th></tr></thead>
          <tbody>
            ${data.groups.map(g => `
              <tr class="clickable" data-action="open-group" data-id="${g.id}">
                <td>${avatarCell(g.name, `${g.code} · ${typeLabel(g.group_type)}`)}</td>
                <td class="hide-sm">${esc(g.branch_name)}</td>
                <td class="num">${num(g.members)}</td>
                <td class="num">${ugx(g.savings_ugx)}</td>
                <td class="hide-sm">${miniBar(g.attendance_rate)}</td>
                <td>${statusBadge(g.status)}</td>
              </tr>`).join('') || `<tr><td colspan="6">${emptyState('groups', 'No groups yet', 'Groups you are allowed to see will appear here.')}</td></tr>`}
          </tbody>
        </table>
      </div>
    </section>

    <div class="grid-2-equal">
      <section class="card">
        <div class="card-head"><div><h2>Recent savings entries</h2><p>Latest recorded contributions</p></div></div>
        <div class="list">
          ${data.recent_contributions.length ? data.recent_contributions.map(c => `
            <div class="list-item">
              <span class="avatar">${esc(initials(c.member_name))}</span>
              <div class="grow"><strong>${esc(c.member_name)}</strong><div class="sub">${esc(c.group_name)} · ${esc(kindLabel(c.kind))}</div></div>
              <div class="right"><div class="strong mono">${ugx(c.amount_ugx)}</div><div style="margin-top:4px">${statusBadge(c.status)}</div></div>
            </div>`).join('') : `<div class="empty" style="padding:34px"><p>No savings entries yet.</p></div>`}
        </div>
      </section>

      <section class="card">
        <div class="card-head"><div><h2>Loan pipeline</h2><p>Borrowing applications by stage</p></div><a class="link" href="#/loans">View loans</a></div>
        <div class="pipeline">
          ${data.loans_by_status.length ? (() => {
            const max = Math.max(...data.loans_by_status.map(s => s.count), 1);
            return data.loans_by_status.map(s => `
              <div class="pipeline-row">
                <span class="label">${esc(STATUS[s.status] ? STATUS[s.status][1] : s.status)}</span>
                <span class="pipeline-track"><span class="pipeline-fill" style="width:${Math.round(s.count / max * 100)}%"></span></span>
                <strong>${s.count}</strong>
              </div>`).join('');
          })() : `<div class="empty" style="padding:26px"><p>No borrowing applications yet.</p></div>`}
          <div class="form-note" style="margin-top:6px">${icon('shield')}<span>Recording, verification and approval are separate roles. Pamoja enforces the split on the server.</span></div>
        </div>
      </section>
    </div>`;
}

/* ---------- groups list --------------------------------------------------- */

async function pageGroups() {
  const { groups } = await api('/groups');
  state.groups = groups;
  const view = document.getElementById('view');
  const types = ['all', ...Object.keys(GROUP_TYPES)];
  const present = new Set(groups.map(g => g.group_type));

  view.innerHTML = `
    ${pageHead('Operations', 'Groups', `${plural(groups.length, 'group')} in your scope`,
      `${can('registration.create') ? `<button class="btn btn-primary" data-action="new-group">${icon('plus')} New group</button>` : ''}`)}
    <div class="toolbar">
      <div class="search">${icon('search')}<input class="input" id="group-search" type="search" placeholder="Search by name, code, village or branch" aria-label="Search groups"></div>
      <div class="chip-row" id="type-chips">
        ${types.filter(t => t === 'all' || present.has(t)).map(t => `
          <button class="chip ${t === 'all' ? 'active' : ''}" data-action="filter-type" data-type="${t}">${t === 'all' ? 'All groups' : typeLabel(t)}</button>`).join('')}
      </div>
    </div>
    <div class="group-grid" id="group-grid"></div>`;

  state.groupFilter = { query: '', type: 'all' };
  renderGroupGrid();

  const search = document.getElementById('group-search');
  search.addEventListener('input', () => {
    state.groupFilter.query = search.value.trim().toLowerCase();
    renderGroupGrid();
  });
}

function renderGroupGrid() {
  const grid = document.getElementById('group-grid');
  if (!grid) return;
  const { query, type } = state.groupFilter || { query: '', type: 'all' };
  const groups = state.groups.filter(g => {
    if (type !== 'all' && g.group_type !== type) return false;
    if (!query) return true;
    return [g.name, g.code, g.village, g.district, g.branch_name, typeLabel(g.group_type)]
      .join(' ').toLowerCase().includes(query);
  });

  if (!groups.length) {
    grid.style.display = 'block';
    grid.innerHTML = `<section class="card">${emptyState('search', 'No groups match', 'Try a different name, code or location.',
      `<button class="btn btn-secondary btn-sm" data-action="clear-filters">Clear filters</button>`)}</section>`;
    return;
  }
  grid.style.display = 'grid';
  grid.innerHTML = groups.map(g => `
    <a class="group-card" href="#/groups/${g.id}">
      <div class="group-top">${statusBadge(g.status)}<span class="code">${esc(g.code)}</span></div>
      <div>
        <h3>${esc(g.name)}</h3>
        <p class="loc">${esc([g.village, g.district].filter(Boolean).join(', ') || g.branch_name)} · ${esc(g.branch_name)}</p>
      </div>
      <div class="group-metrics">
        <div><strong>${num(g.members)}</strong><span>members</span></div>
        <div><strong>${compact(g.savings_ugx).replace('UGX ', '')}</strong><span>savings (UGX)</span></div>
        <div class="ring" style="--p:${g.attendance_rate}"><span>${g.attendance_rate}%</span></div>
      </div>
      <div class="group-foot"><span class="tag-type">${esc(typeLabel(g.group_type))}</span><span>${g.last_meeting ? 'Last met ' + fmtDate(g.last_meeting) : 'No meetings yet'}</span></div>
    </a>`).join('');
}

/* ---------- group detail -------------------------------------------------- */

async function pageGroup(id) {
  const detail = await api('/groups/' + encodeURIComponent(id));
  state.groupDetail = detail;
  state.groupTab = 'overview';
  renderGroupPage();
}

function renderGroupPage() {
  const d = state.groupDetail;
  if (!d) return;
  const g = d.group;
  const view = document.getElementById('view');
  const tabs = [
    ['overview', 'Overview'],
    can('registration.view') ? ['members', `Members · ${d.members.length}`] : null,
    can('meetings.view') ? ['meetings', `Meetings · ${d.meetings.length}`] : null,
    can('payments.view') ? ['savings', 'Savings'] : null,
    can('loans.view') ? ['loans', `Loans · ${d.loans.length}`] : null,
    can('tasks.view') ? ['tasks', 'Tasks'] : null,
  ].filter(Boolean);
  if (!tabs.some(([id]) => id === state.groupTab)) state.groupTab = 'overview';

  view.innerHTML = `
    <div style="padding-top:18px">
      <a class="link small" href="#/groups" style="display:inline-flex;align-items:center;gap:6px;font-weight:600;margin-bottom:14px">
        ${icon('arrow')}<span style="transform:rotate(180deg);display:inline-block">${icon('arrow')}</span> All groups</a>
    </div>
    <section class="detail-head">
      <div>
        <div class="detail-title">
          <h1>${esc(g.name)}</h1>
          ${statusBadge(g.status)}
          <span class="badge grey plain">${esc(g.code)}</span>
        </div>
        <div class="detail-meta">
          <span>${icon('building')} ${esc(typeLabel(g.group_type))} · ${esc(g.branch_name)}</span>
          <span>${icon('mapPin')} ${esc([g.village, g.sub_county, g.district].filter(Boolean).join(', ') || 'Location not recorded')}</span>
          <span>${icon('calendar')} Meets ${esc(g.meeting_day)}s · ${esc(g.meeting_frequency)}</span>
          <span>${icon('clock')} Formed ${fmtDate(g.formed_on)}</span>
        </div>
      </div>
      <div class="head-actions">
        ${g.status === 'pending_activation' && can('registration.review_activate')
          ? `<button class="btn btn-green" data-action="activate-group" data-id="${g.id}">${icon('check')} Activate group</button>` : ''}
        ${can('payments.create') ? `<button class="btn btn-secondary" data-action="record-payment" data-id="${g.id}">${icon('wallet')} Record payment</button>` : ''}
        ${can('meetings.create') ? `<button class="btn btn-secondary" data-action="new-meeting" data-id="${g.id}">${icon('calendar')} New meeting</button>` : ''}
        ${can('loans.create') ? `<button class="btn btn-primary" data-action="new-loan" data-group="${g.id}">${icon('plus')} New application</button>` : ''}
      </div>
    </section>

    <div class="tabs" role="tablist">
      ${tabs.map(([id, label]) => `<button role="tab" data-action="tab" data-tab="${id}" class="${state.groupTab === id ? 'active' : ''}">${esc(label)}</button>`).join('')}
    </div>
    <div id="group-tab"></div>`;

  renderGroupTab();
}

function renderGroupTab() {
  const d = state.groupDetail;
  const host = document.getElementById('group-tab');
  if (!host || !d) return;
  const g = d.group;

  if (state.groupTab === 'members') {
    host.innerHTML = `<section class="card">
      <div class="card-head">
        <div><h2>Members</h2><p>${plural(d.members.length, 'member')} · ${plural(d.members.filter(m => m.status === 'active').length, 'active')}</p></div>
        ${can('registration.create') ? `<button class="btn btn-secondary btn-sm" data-action="add-member" data-id="${g.id}">${icon('plus')} Add member</button>` : ''}
      </div>
      <div class="table-wrap">
        <table class="data">
          <thead><tr><th>Member</th><th class="hide-sm">Phone</th><th>Group role</th><th class="num">Savings</th><th>Status</th><th></th></tr></thead>
          <tbody>${d.members.map(m => `
            <tr>
              <td>${avatarCell(m.full_name, `${m.member_no} · joined ${fmtDate(m.joined_on)}`)}</td>
              <td class="hide-sm mono small">${esc(m.phone_masked || '—')}</td>
              <td><span class="role-chip">${esc(roleLabel(m.role_in_group))}</span></td>
              <td class="num">${ugx(m.savings_ugx)}</td>
              <td>${statusBadge(m.status)}</td>
              <td class="num">${can('registration.edit_draft') ? `<button class="btn btn-ghost btn-sm" data-action="edit-member" data-group="${g.id}" data-id="${m.id}">Manage</button>` : ''}</td>
            </tr>`).join('') || `<tr><td colspan="6">${emptyState('users', 'No members yet', 'Add the group executive and members to start keeping the register.')}</td></tr>`}
          </tbody>
        </table>
      </div>
    </section>`;
    return;
  }

  if (state.groupTab === 'meetings') {
    host.innerHTML = `<section class="card">
      <div class="card-head">
        <div><h2>Meeting register</h2><p>Most recent ${d.meetings.length} meetings</p></div>
        ${can('meetings.create') ? `<button class="btn btn-secondary btn-sm" data-action="new-meeting" data-id="${g.id}">${icon('plus')} New meeting</button>` : ''}
      </div>
      <div class="list">
        ${d.meetings.length ? d.meetings.map(m => `
          <div class="list-item meeting-row">
            <div class="meeting-date"><strong>${parseDate(m.held_on)?.getDate() ?? '—'}</strong><span>${MONTHS[(parseDate(m.held_on)?.getMonth()) ?? 0]}</span></div>
            <div class="grow">
              <strong>Meeting ${m.meeting_no} · ${esc(roleLabel(m.meeting_type))}</strong>
              <div class="sub">${esc(m.location || 'Venue not recorded')} · ${m.status === 'closed' ? 'Register closed' : 'Register open'}</div>
              <div class="mini-stats">
                <span>${icon('check')} ${m.present} present</span>
                <span>${icon('x')} ${m.absent} away</span>
                <span>${icon('wallet')} ${ugx(m.contributions_ugx)} collected</span>
              </div>
            </div>
            <div class="right" style="display:flex;align-items:center;gap:10px">
              ${statusBadge(m.status)}
              <button class="btn btn-secondary btn-sm" data-action="open-meeting" data-id="${m.id}">Open register</button>
            </div>
          </div>`).join('') : `<div class="empty" style="padding:44px"><div class="empty-icon">${icon('calendar')}</div><h3>No meetings yet</h3><p>Open the first meeting to start the register.</p></div>`}
      </div>
    </section>`;
    return;
  }

  if (state.groupTab === 'savings') {
    host.innerHTML = `<section class="card">
      <div class="card-head">
        <div><h2>Savings and payments</h2><p>Every entry keeps its recorder and verifier</p></div>
        ${can('payments.create') ? `<button class="btn btn-secondary btn-sm" data-action="record-payment" data-id="${g.id}">${icon('plus')} Record payment</button>` : ''}
      </div>
      <div class="table-wrap">
        <table class="data">
          <thead><tr><th>Date</th><th>Member</th><th>Type</th><th class="num">Amount</th><th class="hide-sm">Recorded by</th><th>Status</th><th></th></tr></thead>
          <tbody>${d.contributions.map(c => `
            <tr>
              <td class="nowrap small">${fmtDate(c.occurred_on)}</td>
              <td>${esc(c.member_name)}</td>
              <td><span class="badge grey plain">${esc(kindLabel(c.kind))}</span><div class="table-sub">${esc(methodLabel(c.method))}${c.reference ? ' · ' + esc(c.reference) : ''}</div></td>
              <td class="num">${ugx(c.amount_ugx)}</td>
              <td class="hide-sm small muted">${esc(c.recorded_by_name || '—')}</td>
              <td>${statusBadge(c.status)}</td>
              <td class="num nowrap">
                ${c.status === 'submitted' && can('payments.verify') ? `<button class="btn btn-green btn-sm" data-action="verify-contribution" data-id="${c.id}">Verify</button>` : ''}
                ${c.status === 'submitted' && can('payments.return') ? `<button class="btn btn-danger btn-sm" data-action="return-contribution" data-id="${c.id}">Return</button>` : ''}
              </td>
            </tr>`).join('') || `<tr><td colspan="7">${emptyState('wallet', 'No payments yet', 'Recorded savings, shares and repayments will appear here.')}</td></tr>`}
          </tbody>
        </table>
      </div>
    </section>`;
    return;
  }

  if (state.groupTab === 'loans') {
    host.innerHTML = `<section class="card">
      <div class="card-head">
        <div><h2>Borrowing applications</h2><p>Appraisal, decision and disbursement are separate steps</p></div>
        ${can('loans.create') ? `<button class="btn btn-secondary btn-sm" data-action="new-loan" data-group="${g.id}">${icon('plus')} New application</button>` : ''}
      </div>
      <div class="list">
        ${d.loans.length ? d.loans.map(l => loanRow(l)).join('') : `<div class="empty" style="padding:44px"><div class="empty-icon">${icon('loans')}</div><h3>No applications</h3><p>Group borrowing applications will appear here.</p></div>`}
      </div>
    </section>`;
    return;
  }

  if (state.groupTab === 'tasks') {
    host.innerHTML = `<section class="card">
      <div class="card-head">
        <div><h2>Follow-up tasks</h2><p>Actions agreed for this group</p></div>
        ${can('tasks.create') ? `<button class="btn btn-secondary btn-sm" data-action="new-task" data-group="${g.id}">${icon('plus')} New task</button>` : ''}
      </div>
      <div class="list">
        ${d.tasks.length ? d.tasks.map(t => taskRow(t)).join('') : `<div class="empty" style="padding:44px"><div class="empty-icon">${icon('tasks')}</div><h3>No tasks</h3><p>Agreed follow-ups will appear here.</p></div>`}
      </div>
    </section>`;
    return;
  }

  const showProfile = can('registration.view');
  const showMeetings = can('meetings.view');
  const showPayments = can('payments.view');
  const leadership = ['chairperson', 'secretary', 'treasurer'].map(role => ({
    role, member: d.members.find(x => x.role_in_group === role && x.status === 'active'),
  }));
  const latest = d.meetings[0];
  const recent = d.contributions.slice(0, 6);

  const profileCard = showProfile ? `
    <section class="card">
      <div class="card-head"><div><h2>Group profile</h2><p>Leadership and cycle details</p></div></div>
      <div class="card-body">
        <div class="list" style="margin:-8px 0">
          ${leadership.map(({ role, member }) => `
            <div class="list-item" style="padding:12px 0">
              <span class="avatar">${esc(initials(member ? member.full_name : '?'))}</span>
              <div class="grow"><strong>${esc(member ? member.full_name : 'Position vacant')}</strong><div class="sub">${esc(roleLabel(role))}${member ? ' · ' + esc(member.member_no) : ''}</div></div>
              ${member && member.phone_masked ? `<span class="small muted mono">${esc(member.phone_masked)}</span>` : ''}
            </div>`).join('')}
        </div>
        <div class="kpi-inline" style="margin-top:18px;padding-top:18px;border-top:1px solid var(--line)">
          <div><strong>${esc(g.meeting_day)}</strong><span>Meeting day</span></div>
          <div><strong>${esc(roleLabel(g.meeting_frequency))}</strong><span>Frequency</span></div>
          <div><strong>${g.cycle_end ? fmtDate(g.cycle_end) : '—'}</strong><span>Cycle ends</span></div>
        </div>
      </div>
    </section>` : '';

  const meetingCard = showMeetings ? `
    <section class="card">
      <div class="card-head"><div><h2>Latest meeting</h2><p>${latest ? fmtDate(latest.held_on) : 'No meeting held yet'}</p></div>
        ${latest ? `<button class="btn btn-secondary btn-sm" data-action="open-meeting" data-id="${latest.id}">Open</button>` : ''}</div>
      <div class="card-body">
        ${latest ? `
          <div class="kpi-inline">
            <div><strong>${latest.present}</strong><span>Present</span></div>
            <div><strong>${latest.absent}</strong><span>Away</span></div>
            <div><strong>${compact(latest.contributions_ugx)}</strong><span>Collected</span></div>
          </div>
          <p class="small muted" style="margin-top:16px;line-height:1.6">${esc(latest.minutes || latest.agenda || 'No minutes recorded yet.')}</p>
          <div style="margin-top:14px">${statusBadge(latest.status)}</div>
        ` : `<p class="muted small">Open a meeting to start the register and record collections.</p>`}
      </div>
    </section>` : '';

  const recentCard = showPayments ? `
    <div class="section-title"><h2>Recent activity</h2><a class="link" href="#" data-action="tab" data-tab="savings">All savings ${icon('arrow')}</a></div>
    <section class="card">
      <div class="list">
        ${recent.length ? recent.map(c => `
          <div class="list-item">
            <span class="avatar">${esc(initials(c.member_name))}</span>
            <div class="grow"><strong>${esc(c.member_name)}</strong><div class="sub">${esc(kindLabel(c.kind))} · ${fmtDate(c.occurred_on)} · recorded by ${esc(c.recorded_by_name || '—')}</div></div>
            <div class="right"><div class="strong mono">${ugx(c.amount_ugx)}</div><div style="margin-top:4px">${statusBadge(c.status)}</div></div>
          </div>`).join('') : `<div class="empty" style="padding:34px"><p>No activity recorded yet.</p></div>`}
      </div>
    </section>` : '';

  host.innerHTML = `
    <div class="stat-grid">
      ${statCard('users', '', 'Members', num(g.members), `${plural(g.members, 'active member')}`)}
      ${statCard('wallet', 'green', 'Group savings', compact(g.savings_ugx), 'Verified savings and shares')}
      ${statCard('loans', 'amber', 'Outstanding', compact(g.outstanding_ugx), `${plural(g.active_loans, 'active loan')}`)}
      ${statCard('checkCircle', 'green', 'Attendance', `${g.attendance_rate}%`, 'Average across registers')}
    </div>
    ${profileCard || meetingCard ? `<div class="${profileCard && meetingCard ? 'two-col' : ''}" style="margin-top:16px">${profileCard}${meetingCard}</div>` : ''}
    ${recentCard}`;
}

function loanRow(l) {
  const actions = [];
  if (l.status === 'submitted' && can('loans.appraise')) {
    actions.push(`<button class="btn btn-secondary btn-sm" data-action="appraise-loan" data-id="${l.id}">Appraise</button>`);
  }
  if (l.status === 'appraised' && can('loans.approve')) {
    actions.push(`<button class="btn btn-green btn-sm" data-action="approve-loan" data-id="${l.id}">Approve</button>`);
    actions.push(`<button class="btn btn-danger btn-sm" data-action="decline-loan" data-id="${l.id}">Decline</button>`);
  }
  if (l.status === 'approved' && can('loans.disburse')) {
    actions.push(`<button class="btn btn-primary btn-sm" data-action="disburse-loan" data-id="${l.id}">Confirm disbursement</button>`);
  }
  return `<div class="list-item" style="align-items:flex-start">
    <span class="attention-icon loan" style="margin-top:2px">${icon('loans')}</span>
    <div class="grow">
      <strong>${ugx(l.amount_ugx)} · ${esc(l.group_name)}</strong>
      <div class="sub">${esc(l.purpose)} · ${l.term_months} months${l.applicant_name ? ' · ' + esc(l.applicant_name) : ''}</div>
      <div class="mini-stats">
        ${l.submitted_on ? `<span>${icon('calendar')} Submitted ${fmtDate(l.submitted_on)}</span>` : ''}
        ${l.appraised_by_name ? `<span>${icon('user')} Appraised by ${esc(l.appraised_by_name)}</span>` : ''}
        ${l.approved_by_name ? `<span>${icon('shield')} Decided by ${esc(l.approved_by_name)}</span>` : ''}
        ${l.disbursed_on ? `<span>${icon('wallet')} Disbursed ${fmtDate(l.disbursed_on)}</span>` : ''}
      </div>
    </div>
    <div class="right" style="display:flex;flex-direction:column;align-items:flex-end;gap:8px">
      ${statusBadge(l.status)}
      <div style="display:flex;gap:6px;flex-wrap:wrap;justify-content:flex-end">${actions.join('')}</div>
    </div>
  </div>`;
}

function taskRow(t) {
  return `<div class="list-item">
    <span class="attention-icon ${t.status === 'done' ? 'loan' : ''}">${icon(t.status === 'done' ? 'check' : 'tasks')}</span>
    <div class="grow">
      <strong>${esc(t.title)}</strong>
      <div class="sub">${esc(t.detail || 'No further detail')}</div>
      <div class="mini-stats">
        ${t.group_name ? `<span>${icon('groups')} ${esc(t.group_name)}</span>` : '<span>Portfolio-wide</span>'}
        ${t.assignee_name ? `<span>${icon('user')} ${esc(t.assignee_name)}</span>` : ''}
        ${t.due_on ? `<span>${icon('calendar')} Due ${fmtDate(t.due_on)}</span>` : ''}
        <span>${t.priority === 'high' ? 'High priority' : t.priority === 'low' ? 'Low priority' : 'Normal priority'}</span>
      </div>
    </div>
    <div class="right" style="display:flex;align-items:center;gap:10px">
      ${statusBadge(t.status)}
      ${t.status === 'open' && can('tasks.close') ? `<button class="btn btn-secondary btn-sm" data-action="close-task" data-id="${t.id}">Mark done</button>` : ''}
    </div>
  </div>`;
}

/* ---------- loans page ---------------------------------------------------- */

async function pageLoans() {
  const [{ loans }, { groups }] = await Promise.all([api('/loans'), api('/groups')]);
  state.groups = groups;
  state.loans = loans;
  const view = document.getElementById('view');
  const counts = loans.reduce((acc, l) => { acc[l.status] = (acc[l.status] || 0) + 1; return acc; }, {});
  const stages = ['submitted', 'appraised', 'approved', 'disbursed', 'declined'];
  state.loanFilter = state.loanFilter || 'all';

  view.innerHTML = `
    ${pageHead('Operations', 'Loans', `${plural(loans.length, 'application')} across your groups`,
      `${can('loans.create') ? `<button class="btn btn-primary" data-action="new-loan">${icon('plus')} New application</button>` : ''}`)}
    <div class="stat-grid">
      ${statCard('loans', 'amber', 'Awaiting appraisal', num(counts.submitted || 0), 'Submitted and ready for a field visit')}
      ${statCard('checkCircle', 'amber', 'Awaiting decision', num(counts.appraised || 0), 'Appraised, with the credit approver')}
      ${statCard('wallet', '', 'Approved', num(counts.approved || 0), 'Waiting for disbursement confirmation')}
      ${statCard('shield', 'green', 'Disbursed', num(counts.disbursed || 0), 'Money confirmed by Finance')}
    </div>
    <div class="toolbar" style="margin-top:20px">
      <div class="chip-row">
        <button class="chip ${state.loanFilter === 'all' ? 'active' : ''}" data-action="filter-loans" data-status="all">All</button>
        ${stages.map(s => `<button class="chip ${state.loanFilter === s ? 'active' : ''}" data-action="filter-loans" data-status="${s}">${STATUS[s] ? STATUS[s][1] : s}${counts[s] ? ' · ' + counts[s] : ''}</button>`).join('')}
      </div>
    </div>
    <section class="card">
      <div class="list" id="loan-list"></div>
    </section>`;
  renderLoanList();
}

function renderLoanList() {
  const host = document.getElementById('loan-list');
  if (!host) return;
  const filter = state.loanFilter || 'all';
  const loans = (state.loans || []).filter(l => filter === 'all' || l.status === filter);
  host.innerHTML = loans.length ? loans.map(loanRow).join('') : emptyState('loans', 'No applications here', 'Applications in this stage will appear here.');
}

/* ---------- tasks page ---------------------------------------------------- */

async function pageTasks() {
  const [{ tasks }, { groups }] = await Promise.all([api('/tasks'), api('/groups')]);
  state.groups = groups;
  state.tasks = tasks;
  const view = document.getElementById('view');
  const open = tasks.filter(t => t.status === 'open');
  const done = tasks.filter(t => t.status !== 'open');

  view.innerHTML = `
    ${pageHead('Operations', 'Tasks', `${plural(open.length, 'open task')}`,
      `${can('tasks.create') ? `<button class="btn btn-primary" data-action="new-task">${icon('plus')} New task</button>` : ''}`)}
    <div class="grid-2-equal">
      <section class="card">
        <div class="card-head"><div><h2>Open</h2><p>Follow-ups still to close</p></div><span class="badge amber">${open.length}</span></div>
        <div class="list">${open.length ? open.map(taskRow).join('') : `<div class="empty" style="padding:34px"><div class="empty-icon">${icon('checkCircle')}</div><h3>Nothing open</h3><p>Every follow-up is closed.</p></div>`}</div>
      </section>
      <section class="card">
        <div class="card-head"><div><h2>Completed</h2><p>Recently closed</p></div><span class="badge green">${done.length}</span></div>
        <div class="list">${done.length ? done.map(taskRow).join('') : `<div class="empty" style="padding:34px"><p>No completed tasks yet.</p></div>`}</div>
      </section>
    </div>`;
}

/* ---------- reports ------------------------------------------------------- */

async function pageReports() {
  const { groups } = await api('/groups');
  const total = groups.reduce((acc, g) => ({
    members: acc.members + g.members,
    savings: acc.savings + g.savings_ugx,
    outstanding: acc.outstanding + g.outstanding_ugx,
    loans: acc.loans + g.active_loans,
  }), { members: 0, savings: 0, outstanding: 0, loans: 0 });
  const view = document.getElementById('view');

  view.innerHTML = `
    ${pageHead('Insight', 'Reports', 'A portfolio view you can print or take into a spreadsheet',
      `<button class="btn btn-secondary" data-action="print">${icon('print')} Print</button>
       ${can('reports.export') ? `<button class="btn btn-primary" data-action="download-csv">${icon('download')} Download CSV</button>` : ''}`)}

    <div class="stat-grid">
      ${statCard('groups', '', 'Groups', num(groups.length), 'In your reporting scope')}
      ${statCard('users', '', 'Members', num(total.members), 'Active members')}
      ${statCard('wallet', 'green', 'Savings', compact(total.savings), 'Verified savings and shares')}
      ${statCard('loans', 'amber', 'Outstanding loans', compact(total.outstanding), `${plural(total.loans, 'active loan')}`)}
    </div>

    <section class="card" style="margin-top:18px">
      <div class="card-head"><div><h2>Portfolio by group</h2><p>Figures as at ${dateLong(new Date())}</p></div></div>
      <div class="table-wrap">
        <table class="data">
          <thead><tr><th>Group</th><th>Type</th><th class="hide-sm">Branch</th><th class="num">Members</th><th class="num">Savings</th><th class="num">Outstanding</th><th class="num">Attendance</th></tr></thead>
          <tbody>${groups.map(g => `
            <tr>
              <td><div class="strong">${esc(g.name)}</div><div class="table-sub">${esc(g.code)}</div></td>
              <td>${esc(typeLabel(g.group_type))}</td>
              <td class="hide-sm">${esc(g.branch_name)}</td>
              <td class="num">${num(g.members)}</td>
              <td class="num">${ugx(g.savings_ugx)}</td>
              <td class="num">${ugx(g.outstanding_ugx)}</td>
              <td class="num">${g.attendance_rate}%</td>
            </tr>`).join('') || `<tr><td colspan="7">${emptyState('reports', 'No data yet', 'Groups in your scope will appear here.')}</td></tr>`}
          </tbody>
        </table>
      </div>
    </section>

    <div class="grid-2-equal">
      <section class="card card-pad">
        <h2>Monthly board pack</h2>
        <p class="muted small" style="margin:6px 0 16px">Export the portfolio table as a CSV for the monthly board pack. The file contains only the groups your role can see.</p>
        ${can('reports.export') ? `<button class="btn btn-secondary" data-action="download-csv">${icon('download')} Download portfolio CSV</button>` : '<p class="small muted">Your role does not include export rights.</p>'}
      </section>
      <section class="card card-pad">
        <h2>Data integrity</h2>
        <p class="muted small" style="margin:6px 0 16px">Every change to a group, member, meeting, payment or loan is written to an append-only, hash-chained audit log. Open the audit log to check the chain.</p>
        ${can('audit.view') ? `<a class="btn btn-secondary" href="#/audit">${icon('shield')} Open audit log</a>` : ''}
      </section>
    </div>`;
}

async function downloadCSV() {
  try {
    const res = await api('/reports/portfolio.csv', { raw: true });
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `yopsa-portfolio-${todayISO()}.csv`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1500);
    toast('Portfolio CSV downloaded');
  } catch (err) {
    toast(err.message || 'The report could not be downloaded', 'error');
  }
}

/* ---------- audit --------------------------------------------------------- */

async function pageAudit() {
  const page = await api(`/audit?limit=50&offset=${state.auditOffset}`);
  const view = document.getElementById('view');
  const from = page.entries.length ? page.total - state.auditOffset - page.entries.length + 1 : 0;
  const to = page.total - state.auditOffset;

  view.innerHTML = `
    ${pageHead('Insight', 'Audit log', 'Append-only record of every change, newest first',
      `<button class="btn btn-secondary" data-action="refresh">${icon('refresh')} Refresh</button>`)}
    <div class="integrity ${page.verified ? '' : 'bad'}" style="margin-bottom:18px">
      ${icon(page.verified ? 'shield' : 'alert')}
      <div><strong>${page.verified ? 'Hash chain intact' : 'Chain break detected'}</strong>
      <div class="small" style="opacity:.8">${page.verified
        ? `${num(page.checked)} entries verified from the first record.`
        : `First break at entry ${num(page.first_bad_seq)}. ${esc(page.reason || '')}`}</div></div>
    </div>
    <section class="card">
      <div class="card-head">
        <div><h2>Entries</h2><p>${num(page.total)} total${from ? ` · showing ${num(from)}–${num(to)}` : ''}</p></div>
      </div>
      <div class="table-wrap">
        <table class="data">
          <thead><tr><th class="hide-sm">#</th><th>When</th><th>Actor</th><th>Action</th><th class="hide-sm">Entity</th><th class="hide-sm">Channel</th></tr></thead>
          <tbody>${page.entries.map(e => `
            <tr>
              <td class="hide-sm mono small muted">${e.seq}</td>
              <td class="nowrap small">${fmtDateTime(e.occurred_at)}</td>
              <td><div class="strong">${esc(e.actor_name)}</div><div class="table-sub">${esc(roleLabel(e.actor_role || 'system'))}</div></td>
              <td><span class="badge grey plain">${esc(e.action)}</span></td>
              <td class="hide-sm small">${esc(e.entity_type || '—')}${e.entity_id ? `<div class="table-sub mono">${esc(e.entity_id.slice(0, 8))}…</div>` : ''}</td>
              <td class="hide-sm small muted">${esc(e.channel || '—')}</td>
            </tr>`).join('') || `<tr><td colspan="6">${emptyState('audit', 'No entries yet', 'Audited changes will appear here.')}</td></tr>`}
          </tbody>
        </table>
      </div>
      <div class="card-head" style="border-top:1px solid var(--line);border-bottom:0">
        <span class="small muted">Page ${Math.floor(state.auditOffset / 50) + 1}</span>
        <div style="display:flex;gap:8px">
          <button class="btn btn-secondary btn-sm" data-action="audit-prev" ${state.auditOffset === 0 ? 'disabled' : ''}>Previous</button>
          <button class="btn btn-secondary btn-sm" data-action="audit-next" ${to <= 0 || page.entries.length < 50 ? 'disabled' : ''}>Next</button>
        </div>
      </div>
    </section>`;
}

/* ---------- users --------------------------------------------------------- */

async function pageUsers() {
  const { users } = await api('/users');
  const view = document.getElementById('view');
  view.innerHTML = `
    ${pageHead('Insight', 'Users & roles', 'Staff accounts and the roles they hold',
      `<span class="badge grey plain">${plural(users.length, 'account')}</span>`)}
    <div class="form-note" style="margin-bottom:18px">${icon('shield')}
      <span>Role grants and revocations need a second administrator's approval (four-eyes, SD-10). This build shows the directory read-only; grants are provisioned by the seed for testing.</span>
    </div>
    <section class="card">
      <div class="table-wrap">
        <table class="data">
          <thead><tr><th>User</th><th class="hide-sm">Phone</th><th>Roles</th><th class="hide-sm">MFA</th><th>Status</th></tr></thead>
          <tbody>${users.map(u => `
            <tr>
              <td>${avatarCell(u.full_name, u.is_staff ? 'Staff account' : 'Member account')}</td>
              <td class="hide-sm mono small">${esc(u.phone_masked || '—')}</td>
              <td>${u.roles ? u.roles.split(', ').map(r => `<span class="role-chip" style="margin:2px 4px 2px 0">${esc(roleLabel(r))}</span>`).join('') : '<span class="muted small">No active role</span>'}</td>
              <td class="hide-sm">${u.mfa_required ? statusBadge('active', 'Required') : '<span class="small muted">Not enrolled</span>'}</td>
              <td>${statusBadge(u.status)}</td>
            </tr>`).join('') || `<tr><td colspan="5">${emptyState('users', 'No accounts', 'Staff accounts will appear here.')}</td></tr>`}
          </tbody>
        </table>
      </div>
    </section>`;
}

/* ---------- profile ------------------------------------------------------- */

async function pageProfile() {
  const [{ sessions }, me] = await Promise.all([api('/auth/sessions'), api('/me')]);
  state.me = me;
  const view = document.getElementById('view');
  const roles = me.roles || [];

  view.innerHTML = `
    ${pageHead('Account', 'Your profile', 'Identity, roles and signed-in devices')}
    <div class="two-col">
      <section class="card">
        <div class="card-head"><div><h2>${esc(me.full_name)}</h2><p>${esc(me.phone_masked || 'Phone not recorded')}</p></div><span class="avatar" style="width:44px;height:44px;font-size:14px">${esc(initials(me.full_name))}</span></div>
        <div class="card-body">
          <h3 style="font-size:12px;letter-spacing:.1em;text-transform:uppercase;color:var(--muted-2);margin-bottom:10px">Roles and scopes</h3>
          <div style="display:flex;flex-wrap:wrap;gap:8px">
            ${roles.length ? roles.map(r => `<span class="role-chip">${esc(roleLabel(r.role))} <small>${esc(scopeLabel(r.scope_type))}</small></span>`).join('') : '<span class="muted small">No roles assigned</span>'}
          </div>
          <div class="kpi-inline" style="margin-top:22px;padding-top:18px;border-top:1px solid var(--line)">
            <div><strong>${me.is_staff ? 'Staff' : 'Member'}</strong><span>Account type</span></div>
            <div><strong>${me.mfa_required ? 'Required' : 'Not enrolled'}</strong><span>Second factor</span></div>
          </div>
          <div class="form-note" style="margin-top:20px">${icon('shield')}<span>Every action you take is written to the audit log with your account, role and the request reference.</span></div>
        </div>
      </section>

      <section class="card">
        <div class="card-head"><div><h2>Signed-in devices</h2><p>Sessions for your account</p></div></div>
        <div class="list">
          ${sessions.map(s => `
            <div class="list-item">
              <span class="attention-icon">${icon('lock')}</span>
              <div class="grow"><strong>${esc(s.device_id || 'Unknown device')}${s.current ? ' · this device' : ''}</strong>
                <div class="sub">${esc((s.user_agent || 'Browser').slice(0, 64))}</div>
                <div class="mini-stats"><span>${icon('clock')} Started ${fmtDateTime(s.created_at)}</span></div>
              </div>
              ${s.revoked_at ? statusBadge('returned', 'Revoked') : s.current ? statusBadge('active', 'Current') : `<button class="btn btn-danger btn-sm" data-action="revoke-session" data-id="${s.id}">Revoke</button>`}
            </div>`).join('') || `<div class="empty" style="padding:30px"><p>No other sessions.</p></div>`}
        </div>
        <div class="card-body" style="border-top:1px solid var(--line)">
          <button class="btn btn-secondary" data-action="logout">${icon('logout')} Sign out</button>
        </div>
      </section>
    </div>`;
}

/* ---------- actions ------------------------------------------------------- */

async function refreshGroupDetail() {
  if (!state.groupDetail) return;
  const detail = await api('/groups/' + state.groupDetail.group.id);
  state.groupDetail = detail;
  renderGroupPage();
}

function openNewGroup() {
  openModal({
    title: 'Register a new group',
    subtitle: 'The group starts as pending activation until a reviewer confirms it.',
    submitLabel: 'Create group',
    body: `
      <div class="field"><label for="g-name">Group name</label><input class="input" id="g-name" name="name" required placeholder="e.g. Kyamuhunga Women's Group"></div>
      <div class="field-row">
        <div class="field"><label for="g-type">Group type</label>
          <select class="select" id="g-type" name="group_type">
            ${Object.entries(GROUP_TYPES).map(([v, l]) => `<option value="${v}">${esc(l)}</option>`).join('')}
          </select></div>
        <div class="field"><label for="g-formed">Formed on</label><input class="input" id="g-formed" name="formed_on" type="date" value="${todayISO()}"></div>
      </div>
      <div class="field-row">
        <div class="field"><label for="g-day">Meeting day</label>
          <select class="select" id="g-day" name="meeting_day">
            ${['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'].map(d => `<option ${d === 'Sunday' ? 'selected' : ''}>${d}</option>`).join('')}
          </select></div>
        <div class="field"><label for="g-freq">Frequency</label>
          <select class="select" id="g-freq" name="meeting_frequency">
            <option value="weekly">Weekly</option><option value="fortnightly">Fortnightly</option><option value="monthly">Monthly</option>
          </select></div>
      </div>
      <div class="field-row">
        <div class="field"><label for="g-village">Village</label><input class="input" id="g-village" name="village" placeholder="Village or trading centre"></div>
        <div class="field"><label for="g-sub">Sub-county</label><input class="input" id="g-sub" name="sub_county" placeholder="Sub-county"></div>
      </div>
      <div class="field"><label for="g-district">District</label><input class="input" id="g-district" name="district" placeholder="District"></div>
      <div class="form-note">${icon('info')}<span>The group is created in your branch. A credit approver reviews and activates it.</span></div>`,
    onSubmit: async (fd) => {
      const group = await api('/groups', {
        method: 'POST',
        body: {
          name: fd.get('name'), group_type: fd.get('group_type'), formed_on: fd.get('formed_on'),
          meeting_day: fd.get('meeting_day'), meeting_frequency: fd.get('meeting_frequency'),
          village: fd.get('village'), sub_county: fd.get('sub_county'), district: fd.get('district'),
        },
      });
      closeModal();
      toast('Group created and sent for activation review');
      location.hash = '#/groups/' + group.id;
    },
  });
}

function openAddMember(groupId) {
  openModal({
    title: 'Add a member',
    subtitle: 'The member number is assigned automatically.',
    submitLabel: 'Add member',
    body: `
      <div class="field"><label for="m-name">Full name</label><input class="input" id="m-name" name="full_name" required placeholder="Member's full name"></div>
      <div class="field"><label for="m-phone">Phone number</label><input class="input" id="m-phone" name="phone" inputmode="tel" placeholder="+256 7xx xxx xxx"><span class="hint">Stored masked; the full number is never shown in lists.</span></div>
      <div class="field-row">
        <div class="field"><label for="m-gender">Gender</label>
          <select class="select" id="m-gender" name="gender"><option value="female">Female</option><option value="male">Male</option><option value="other">Other</option></select></div>
        <div class="field"><label for="m-role">Group role</label>
          <select class="select" id="m-role" name="role_in_group"><option value="member">Member</option><option value="chairperson">Chairperson</option><option value="secretary">Secretary</option><option value="treasurer">Treasurer</option></select></div>
      </div>
      <div class="field"><label for="m-joined">Joined on</label><input class="input" id="m-joined" name="joined_on" type="date" value="${todayISO()}"></div>`,
    onSubmit: async (fd) => {
      await api(`/groups/${groupId}/members`, {
        method: 'POST',
        body: {
          full_name: fd.get('full_name'), phone: fd.get('phone'), gender: fd.get('gender'),
          role_in_group: fd.get('role_in_group'), joined_on: fd.get('joined_on'),
        },
      });
      closeModal();
      toast('Member added');
      await refreshGroupDetail();
    },
  });
}

function openEditMember(groupId, memberId) {
  const member = (state.groupDetail?.members || []).find(m => m.id === memberId);
  if (!member) return;
  openModal({
    title: member.full_name,
    subtitle: `${member.member_no} · joined ${fmtDate(member.joined_on)}`,
    submitLabel: 'Save changes',
    body: `
      <div class="field"><label for="em-role">Group role</label>
        <select class="select" id="em-role" name="role_in_group">
          ${['member', 'chairperson', 'secretary', 'treasurer'].map(r => `<option value="${r}" ${member.role_in_group === r ? 'selected' : ''}>${roleLabel(r)}</option>`).join('')}
        </select></div>
      <div class="field"><label for="em-status">Status</label>
        <select class="select" id="em-status" name="status">
          ${['active', 'inactive', 'exited'].map(s => `<option value="${s}" ${member.status === s ? 'selected' : ''}>${roleLabel(s)}</option>`).join('')}
        </select></div>
      <div class="form-note">${icon('info')}<span>Members are never deleted. Setting a member to exited keeps their savings and meeting history intact.</span></div>`,
    onSubmit: async (fd) => {
      await api(`/groups/${groupId}/members/${memberId}`, {
        method: 'PATCH',
        body: { role_in_group: fd.get('role_in_group'), status: fd.get('status') },
      });
      closeModal();
      toast('Member updated');
      await refreshGroupDetail();
    },
  });
}

function openNewMeeting(groupId) {
  openModal({
    title: 'Open a meeting',
    subtitle: 'Every active member starts as present; mark the exceptions in the register.',
    submitLabel: 'Open meeting',
    body: `
      <div class="field-row">
        <div class="field"><label for="mt-date">Date held</label><input class="input" id="mt-date" name="held_on" type="date" value="${todayISO()}"></div>
        <div class="field"><label for="mt-type">Meeting type</label>
          <select class="select" id="mt-type" name="meeting_type"><option value="regular">Regular</option><option value="special">Special</option><option value="annual">Annual</option></select></div>
      </div>
      <div class="field"><label for="mt-loc">Venue</label><input class="input" id="mt-loc" name="location" placeholder="e.g. Community hall"></div>
      <div class="field"><label for="mt-agenda">Agenda</label><textarea class="textarea" id="mt-agenda" name="agenda" placeholder="Opening prayer; savings; loan review; AOB"></textarea></div>`,
    onSubmit: async (fd) => {
      await api(`/groups/${groupId}/meetings`, {
        method: 'POST',
        body: { held_on: fd.get('held_on'), meeting_type: fd.get('meeting_type'), location: fd.get('location'), agenda: fd.get('agenda') },
      });
      closeModal();
      toast('Meeting opened');
      await refreshGroupDetail();
    },
  });
}

function openMeeting(meetingId) {
  const detail = state.groupDetail;
  const meeting = (detail?.meetings || []).find(m => m.id === meetingId);
  if (!meeting) return;
  const editable = meeting.status === 'draft' && can('meetings.edit_draft');
  const attendance = meeting.attendance || [];

  openModal({
    title: `Meeting ${meeting.meeting_no} · ${fmtDate(meeting.held_on)}`,
    subtitle: `${roleLabel(meeting.meeting_type)} meeting · ${meeting.location || 'venue not recorded'}`,
    wide: true,
    submitLabel: editable ? 'Save register' : '',
    body: `
      <div class="register">
        ${attendance.length ? attendance.map(a => `
          <div class="reg-row">
            <span>${esc(a.member_name)}</span>
            ${editable ? `<select class="select" name="att-${a.member_id}" aria-label="Attendance for ${esc(a.member_name)}">
              ${['present', 'late', 'excused', 'absent'].map(s => `<option value="${s}" ${a.status === s ? 'selected' : ''}>${roleLabel(s)}</option>`).join('')}
            </select>` : statusBadge(a.status)}
          </div>`).join('') : '<div class="empty" style="padding:24px"><p>The register has no rows.</p></div>'}
      </div>
      <div class="field">
        <label for="mt-minutes">Minutes</label>
        <textarea class="textarea" id="mt-minutes" name="minutes" ${editable ? '' : 'readonly'} placeholder="What was agreed?">${esc(meeting.minutes || '')}</textarea>
      </div>
      <div class="form-note">${icon('shield')}<span>${editable
        ? 'Closing the register locks it. A closed register can only be amended with a second reviewer.'
        : 'This register is closed and read-only.'}</span></div>`,
    onSubmit: async (fd) => {
      const entries = attendance.map(a => ({ member_id: a.member_id, status: fd.get(`att-${a.member_id}`) || a.status, note: '' }));
      if (entries.length) {
        await api(`/meetings/${meetingId}/attendance`, { method: 'POST', body: { entries } });
      }
      closeModal();
      toast('Register saved');
      await refreshGroupDetail();
    },
  });

  if (editable && can('meetings.close')) {
    const foot = document.querySelector('.modal-foot');
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'btn btn-green';
    btn.textContent = 'Save and close meeting';
    btn.addEventListener('click', async () => {
      const form = document.getElementById('modal-form');
      const fd = new FormData(form);
      btn.disabled = true;
      try {
        const entries = attendance.map(a => ({ member_id: a.member_id, status: fd.get(`att-${a.member_id}`) || a.status, note: '' }));
        if (entries.length) await api(`/meetings/${meetingId}/attendance`, { method: 'POST', body: { entries } });
        await api(`/meetings/${meetingId}/close`, { method: 'POST', body: { minutes: fd.get('minutes') || '' } });
        closeModal();
        toast('Meeting closed and register locked');
        await refreshGroupDetail();
      } catch (err) {
        toast(err.message || 'Could not close the meeting', 'error');
        btn.disabled = false;
      }
    });
    foot.insertBefore(btn, foot.querySelector('button[type="submit"]'));
  }
}

function openRecordPayment(groupId) {
  const members = (state.groupDetail?.members || []).filter(m => m.status === 'active');
  if (!members.length) { toast('Add a member before recording a payment', 'error'); return; }
  const meetings = (state.groupDetail?.meetings || []).filter(m => m.status === 'draft');
  openModal({
    title: 'Record a payment',
    subtitle: 'The entry is submitted for independent verification.',
    submitLabel: 'Record entry',
    body: `
      <div class="field"><label for="p-member">Member</label>
        <select class="select" id="p-member" name="member_id">
          ${members.map(m => `<option value="${m.id}">${esc(m.full_name)} · ${esc(m.member_no)}</option>`).join('')}
        </select></div>
      <div class="field-row">
        <div class="field"><label for="p-kind">Payment type</label>
          <select class="select" id="p-kind" name="kind">
            ${Object.entries(KIND_LABELS).map(([v, l]) => `<option value="${v}">${esc(l)}</option>`).join('')}
          </select></div>
        <div class="field"><label for="p-amount">Amount (UGX)</label><input class="input" id="p-amount" name="amount_ugx" type="number" min="1" step="1" required placeholder="10000"></div>
      </div>
      <div class="field-row">
        <div class="field"><label for="p-method">Method</label>
          <select class="select" id="p-method" name="method">
            ${Object.entries(METHOD_LABELS).map(([v, l]) => `<option value="${v}">${esc(l)}</option>`).join('')}
          </select></div>
        <div class="field"><label for="p-date">Date</label><input class="input" id="p-date" name="occurred_on" type="date" value="${todayISO()}"></div>
      </div>
      ${meetings.length ? `<div class="field"><label for="p-meeting">Meeting (optional)</label>
        <select class="select" id="p-meeting" name="meeting_id"><option value="">Not linked to a meeting</option>
          ${meetings.map(m => `<option value="${m.id}">Meeting ${m.meeting_no} · ${fmtDate(m.held_on)}</option>`).join('')}
        </select></div>` : ''}
      <div class="field"><label for="p-ref">Reference (optional)</label><input class="input" id="p-ref" name="reference" placeholder="Mobile money or receipt reference"></div>
      <div class="field"><label for="p-note">Note (optional)</label><input class="input" id="p-note" name="note" placeholder="Anything the verifier should know"></div>`,
    onSubmit: async (fd) => {
      await api(`/groups/${groupId}/contributions`, {
        method: 'POST',
        body: {
          member_id: fd.get('member_id'), kind: fd.get('kind'), amount_ugx: Number(fd.get('amount_ugx')),
          method: fd.get('method'), occurred_on: fd.get('occurred_on'), reference: fd.get('reference'),
          note: fd.get('note'), meeting_id: fd.get('meeting_id') || '',
        },
      });
      closeModal();
      toast('Payment recorded and submitted for verification');
      await refreshGroupDetail();
    },
  });
}

function openVerify(contributionId) {
  const c = (state.groupDetail?.contributions || []).find(x => x.id === contributionId);
  if (!c) return;
  openModal({
    title: 'Verify this entry?',
    subtitle: 'Verification confirms the money was received and counted.',
    submitLabel: 'Confirm verification',
    submitClass: 'btn-green',
    body: `
      <div class="card" style="box-shadow:none;margin-bottom:16px">
        <div class="card-body">
          <div class="kpi-inline">
            <div><strong>${ugx(c.amount_ugx)}</strong><span>${esc(kindLabel(c.kind))}</span></div>
            <div><strong>${esc(c.member_name)}</strong><span>Member</span></div>
            <div><strong>${fmtDate(c.occurred_on)}</strong><span>Date</span></div>
          </div>
          <p class="small muted" style="margin-top:14px">Recorded by ${esc(c.recorded_by_name || '—')} · ${esc(methodLabel(c.method))}${c.reference ? ' · ' + esc(c.reference) : ''}</p>
        </div>
      </div>
      <div class="form-note">${icon('shield')}<span>You cannot verify an entry you recorded yourself. Pamoja checks this on the server.</span></div>`,
    onSubmit: async () => {
      await api(`/contributions/${contributionId}/verify`, { method: 'POST', body: {} });
      closeModal();
      toast('Entry verified');
      await refreshGroupDetail();
    },
  });
}

function openReturn(contributionId) {
  openModal({
    title: 'Return this entry',
    subtitle: 'The recorder will see your reason and can correct it.',
    submitLabel: 'Return entry',
    submitClass: 'btn-danger',
    body: `<div class="field"><label for="r-note">Reason</label><textarea class="textarea" id="r-note" name="note" required placeholder="What needs correcting?"></textarea></div>`,
    onSubmit: async (fd) => {
      await api(`/contributions/${contributionId}/return`, { method: 'POST', body: { note: fd.get('note') } });
      closeModal();
      toast('Entry returned to the recorder');
      await refreshGroupDetail();
    },
  });
}

function openNewLoan(presetGroup) {
  if (!state.groups.length) {
    api('/groups').then(({ groups }) => { state.groups = groups; openNewLoan(presetGroup); }).catch(() => toast('Could not load groups', 'error'));
    return;
  }
  openModal({
    title: 'New borrowing application',
    subtitle: 'A field officer appraises it before the credit approver decides.',
    submitLabel: 'Submit application',
    body: `
      <div class="field"><label for="l-group">Group</label>
        <select class="select" id="l-group" name="group_id">
          ${state.groups.map(g => `<option value="${g.id}" ${presetGroup === g.id ? 'selected' : ''}>${esc(g.name)} · ${esc(g.branch_name)}</option>`).join('')}
        </select></div>
      <div class="field-row">
        <div class="field"><label for="l-amount">Amount (UGX)</label><input class="input" id="l-amount" name="amount_ugx" type="number" min="1" step="1" required placeholder="5000000"></div>
        <div class="field"><label for="l-term">Term (months)</label><input class="input" id="l-term" name="term_months" type="number" min="1" max="36" value="6"></div>
      </div>
      <div class="field"><label for="l-purpose">Purpose</label><textarea class="textarea" id="l-purpose" name="purpose" required placeholder="What will the loan be used for?"></textarea></div>
      <div class="form-note">${icon('info')}<span>The application is recorded against the group and enters the appraisal queue.</span></div>`,
    onSubmit: async (fd) => {
      await api('/loans', {
        method: 'POST',
        body: {
          group_id: fd.get('group_id'), amount_ugx: Number(fd.get('amount_ugx')),
          purpose: fd.get('purpose'), term_months: Number(fd.get('term_months')),
        },
      });
      closeModal();
      toast('Application submitted for appraisal');
      if (location.hash.startsWith('#/groups/')) await refreshGroupDetail();
      else if (location.hash.startsWith('#/loans')) route();
    },
  });
}

function openLoanNote(loanId, mode) {
  const loan = (state.loans || []).concat(state.groupDetail?.loans || []).find(l => l.id === loanId);
  const titles = { appraise: 'Appraise this application', approve: 'Approve this application', decline: 'Decline this application' };
  const labels = { appraise: 'Record appraisal', approve: 'Approve loan', decline: 'Decline application' };
  const classes = { appraise: 'btn-secondary', approve: 'btn-green', decline: 'btn-danger' };
  openModal({
    title: titles[mode] || 'Decision',
    subtitle: loan ? `${ugx(loan.amount_ugx)} · ${loan.group_name}` : '',
    submitLabel: labels[mode] || 'Save',
    submitClass: classes[mode] || 'btn-primary',
    body: `
      ${loan ? `<div class="form-note" style="margin-bottom:16px">${icon('info')}<span>${esc(loan.purpose)} · ${loan.term_months} months</span></div>` : ''}
      <div class="field"><label for="ln-note">${mode === 'appraise' ? 'Appraisal note' : 'Decision note'}</label>
        <textarea class="textarea" id="ln-note" name="note" placeholder="${mode === 'appraise' ? 'Capacity, savings record, attendance…' : 'Reason for the decision…'}"></textarea></div>
      <div class="form-note">${icon('shield')}<span>${mode === 'approve'
        ? 'The approver must be different from the appraising officer.'
        : mode === 'decline' ? 'The group sees the reason through the field officer.' : 'The appraisal is recorded against your name.'}</span></div>`,
    onSubmit: async (fd) => {
      await api(`/loans/${loanId}/${mode}`, { method: 'POST', body: { note: fd.get('note') || '' } });
      closeModal();
      toast(mode === 'appraise' ? 'Appraisal recorded' : mode === 'approve' ? 'Loan approved' : 'Application declined');
      await afterLoanChange();
    },
  });
}

function openDisburse(loanId) {
  const loan = (state.loans || []).concat(state.groupDetail?.loans || []).find(l => l.id === loanId);
  openModal({
    title: 'Confirm disbursement',
    subtitle: loan ? `${ugx(loan.amount_ugx)} · ${loan.group_name}` : '',
    submitLabel: 'Confirm disbursement',
    submitClass: 'btn-primary',
    body: `<div class="form-note">${icon('wallet')}<span>Confirm only after the money has left Yopsa's account. Disbursement is confirmed by Finance, independent of the approver.</span></div>`,
    onSubmit: async () => {
      await api(`/loans/${loanId}/disburse`, { method: 'POST', body: {} });
      closeModal();
      toast('Disbursement confirmed');
      await afterLoanChange();
    },
  });
}

async function afterLoanChange() {
  if (location.hash.startsWith('#/groups/')) await refreshGroupDetail();
  else route();
}

function openNewTask(presetGroup) {
  if (!state.groups.length) {
    api('/groups').then(({ groups }) => { state.groups = groups; openNewTask(presetGroup); }).catch(() => toast('Could not load groups', 'error'));
    return;
  }
  openModal({
    title: 'New follow-up task',
    subtitle: 'Assigned to you with a due date.',
    submitLabel: 'Create task',
    body: `
      <div class="field"><label for="t-title">Title</label><input class="input" id="t-title" name="title" required placeholder="What needs doing?"></div>
      <div class="field"><label for="t-detail">Detail</label><textarea class="textarea" id="t-detail" name="detail" placeholder="Context for the person picking this up"></textarea></div>
      <div class="field-row">
        <div class="field"><label for="t-group">Group (optional)</label>
          <select class="select" id="t-group" name="group_id"><option value="">Portfolio-wide</option>
            ${state.groups.map(g => `<option value="${g.id}" ${presetGroup === g.id ? 'selected' : ''}>${esc(g.name)}</option>`).join('')}
          </select></div>
        <div class="field"><label for="t-due">Due date</label><input class="input" id="t-due" name="due_on" type="date" value="${todayISO()}"></div>
      </div>
      <div class="field"><label for="t-priority">Priority</label>
        <select class="select" id="t-priority" name="priority"><option value="normal">Normal</option><option value="high">High</option><option value="low">Low</option></select></div>`,
    onSubmit: async (fd) => {
      await api('/tasks', {
        method: 'POST',
        body: { title: fd.get('title'), detail: fd.get('detail'), group_id: fd.get('group_id') || '', due_on: fd.get('due_on'), priority: fd.get('priority') },
      });
      closeModal();
      toast('Task created');
      if (location.hash.startsWith('#/groups/')) await refreshGroupDetail();
      else route();
    },
  });
}

/* ---------- global events ------------------------------------------------- */

document.addEventListener('click', async (event) => {
  const demo = event.target.closest('[data-action="demo"]');
  if (demo) {
    const phone = demo.dataset.phone;
    const form = document.getElementById('login-form');
    if (form) {
      form.phone.value = phone;
      form.password.value = 'Pamoja#2026';
      form.password.focus();
    }
    return;
  }

  const el = event.target.closest('[data-action]');
  if (!el) return;
  const action = el.dataset.action;
  const id = el.dataset.id;

  switch (action) {
    case 'close-modal': closeModal(); break;
    case 'open-nav': document.querySelector('.sidebar')?.classList.add('open'); break;
    case 'logout': await doLogout(); break;
    case 'refresh': route(); break;
    case 'open-group': if (id) location.hash = '#/groups/' + id; break;
    case 'clear-filters': {
      const search = document.getElementById('group-search');
      if (search) search.value = '';
      state.groupFilter = { query: '', type: 'all' };
      $$('#type-chips .chip').forEach(c => c.classList.toggle('active', c.dataset.type === 'all'));
      renderGroupGrid();
      break;
    }
    case 'filter-type':
      state.groupFilter = state.groupFilter || { query: '', type: 'all' };
      state.groupFilter.type = el.dataset.type;
      $$('#type-chips .chip').forEach(c => c.classList.toggle('active', c === el));
      renderGroupGrid();
      break;
    case 'tab':
      event.preventDefault();
      state.groupTab = el.dataset.tab;
      $$('.tabs button').forEach(b => b.classList.toggle('active', b === el));
      renderGroupTab();
      break;
    case 'new-group': openNewGroup(); break;
    case 'activate-group':
      openModal({
        title: 'Activate this group?',
        subtitle: 'Confirm that the constitution, executive list and bank details are on file.',
        submitLabel: 'Activate group',
        submitClass: 'btn-green',
        body: `<div class="form-note">${icon('shield')}<span>The group becomes active and can start recording meetings and savings. The activation is audited against your name.</span></div>`,
        onSubmit: async () => {
          await api(`/groups/${id}/activate`, { method: 'POST', body: {} });
          closeModal();
          toast('Group activated');
          await refreshGroupDetail();
        },
      });
      break;
    case 'add-member': openAddMember(id); break;
    case 'edit-member': openEditMember(el.dataset.group, id); break;
    case 'new-meeting': openNewMeeting(id); break;
    case 'open-meeting': openMeeting(id); break;
    case 'record-payment': openRecordPayment(id); break;
    case 'verify-contribution': openVerify(id); break;
    case 'return-contribution': openReturn(id); break;
    case 'new-loan': openNewLoan(el.dataset.group); break;
    case 'appraise-loan': openLoanNote(id, 'appraise'); break;
    case 'approve-loan': openLoanNote(id, 'approve'); break;
    case 'decline-loan': openLoanNote(id, 'decline'); break;
    case 'disburse-loan': openDisburse(id); break;
    case 'filter-loans':
      state.loanFilter = el.dataset.status;
      $$('.toolbar .chip').forEach(c => c.classList.toggle('active', c === el));
      renderLoanList();
      break;
    case 'new-task': openNewTask(el.dataset.group); break;
    case 'close-task':
      openModal({
        title: 'Mark this task done?',
        submitLabel: 'Mark done',
        submitClass: 'btn-green',
        body: `<p class="small muted">The task stays in the record with a completion timestamp.</p>`,
        onSubmit: async () => {
          await api(`/tasks/${id}/close`, { method: 'POST', body: {} });
          closeModal();
          toast('Task completed');
          if (location.hash.startsWith('#/groups/')) await refreshGroupDetail();
          else route();
        },
      });
      break;
    case 'download-csv': await downloadCSV(); break;
    case 'print': window.print(); break;
    case 'audit-prev': state.auditOffset = Math.max(0, state.auditOffset - 50); route(); break;
    case 'audit-next': state.auditOffset += 50; route(); break;
    case 'revoke-session':
      openModal({
        title: 'Revoke this session?',
        subtitle: 'That device will be signed out immediately.',
        submitLabel: 'Revoke session',
        submitClass: 'btn-danger',
        body: `<div class="form-note">${icon('lock')}<span>The refresh token for that device stops working. You can sign in again from it later.</span></div>`,
        onSubmit: async () => {
          await api(`/auth/sessions/${id}/revoke`, { method: 'POST', body: {} });
          closeModal();
          toast('Session revoked');
          route();
        },
      });
      break;
    default: break;
  }

  if (event.target.closest('.nav a')) document.querySelector('.sidebar')?.classList.remove('open');
});

document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape') closeModal();
});

window.addEventListener('hashchange', () => {
  if (!state.me) return;
  if (!document.getElementById('view')) { showApp(); return; }
  route();
});

/* ---------- boot ---------------------------------------------------------- */

async function boot() {
  if (state.access || state.refresh) {
    try {
      state.me = await api('/me');
      showApp();
      return;
    } catch (_) {
      clearTokens();
    }
  }
  renderLogin();
}

boot();
