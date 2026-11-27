const SORT_KEYS = new Set(['name', 'modified', 'type', 'size']);

function loadSortBy() {
  const v = localStorage.getItem('filehub-sort-by');
  return SORT_KEYS.has(v) ? v : 'name';
}

function loadSortDir() {
  return localStorage.getItem('filehub-sort-dir') === 'desc' ? 'desc' : 'asc';
}

const state = {
  user: null,
  pendingToken: null,
  pendingUsername: null,
  root: 'home',
  path: '',
  perm: 'write',
  canDelete: true,
  roots: [],
  trashCount: 0,
  /** @type {'list'|'icons'} */
  viewMode: (localStorage.getItem('filehub-view') === 'icons' ? 'icons' : 'list'),
  /** @type {'name'|'modified'|'type'|'size'} */
  sortBy: loadSortBy(),
  /** @type {'asc'|'desc'} */
  sortDir: loadSortDir(),
  /** @type {Array|null} */
  entries: null,
  /** @type {Set<string>} expanded tree keys "rootId:path" */
  treeExpanded: new Set(['home:']),
  /** @type {Map<string, Array>} cached dir children for tree */
  treeChildren: new Map(),
  /** @type {Set<string>} selected entry paths in current folder */
  selected: new Set(),
  /** @type {'move'|'copy'|null} */
  transferMode: null,
  transferDestRoot: 'home',
  transferDestPath: '',
  globalQuotaEnabled: false,
  globalQuotaBytes: 0,
  /** pending share target { path, isDir, name } */
  shareTarget: null,
  /** true while POST /api/links is in flight */
  shareCreating: false,
  /** @type {Array} cached admin user list for share grants */
  adminUsers: [],
  /** @type {Array} cached admin share list */
  adminShares: [],
  /** @type {Array} eligible users for the Share dialog */
  shareDirectory: [],
  /** @type {number[]} default (frequent + fill) user IDs, up to 5 */
  shareFrequentIds: [],
  /** @type {Set<number>} */
  shareSelectedIds: new Set(),
  /** @type {Array|null} extra directory matches from keyword search (email, etc.) */
  shareQueryMatches: null,
  /** public link token when on /s/:token */
  publicToken: null,
  publicPath: '',
  publicMeta: null,
  /** password reset token */
  resetToken: null,
  onlyofficeReady: false,
  ooEditorOpen: false,
  isAlbum: false,
  inAlbum: false,
  albumNav: false,
  albumOffKey: null,
  albums: [],
};

const $ = (sel) => document.querySelector(sel);

async function api(path, opts = {}) {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: opts.body && !(opts.body instanceof FormData)
      ? { 'Content-Type': 'application/json', ...(opts.headers || {}) }
      : opts.headers,
    ...opts,
  });
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { raw: text }; }
  // res.ok is true for any 2xx, including 201 Created (e.g. public links).
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
}

function formatTrashCount(n) {
  return n > 99 ? '99+' : String(n);
}

function fmtSize(n) {
  if (n == null) return '';
  const u = ['B','KB','MB','GB','TB'];
  let i = 0; let v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`;
}

function show(view) {
  $('#login-view').hidden = view !== 'login';
  $('#main-view').hidden = view !== 'main';
  $('#settings-view').hidden = view !== 'settings';
  const register = $('#register-view');
  if (register) register.hidden = view !== 'register';
  const forgot = $('#forgot-view');
  if (forgot) forgot.hidden = view !== 'forgot';
  const reset = $('#reset-view');
  if (reset) reset.hidden = view !== 'reset';
  const pub = $('#public-share-view');
  if (pub) pub.hidden = view !== 'public';
  if (view !== 'main') closeMobileSidebar();
  if (view !== 'settings' && view !== 'main' && view !== 'public') stopPreviewMedia();
  if (view !== 'public') clearPublicInlinePreview();
  if (view !== 'settings') stopSystemStatusPoll();
}

async function bootstrap() {
  const m = location.pathname.match(/^\/s\/([a-fA-F0-9]+)\/?$/);
  if (m) {
    state.publicToken = m[1];
    show('public');
    await loadPublicShare();
    return;
  }
  const path = location.pathname.replace(/\/+$/, '') || '/';
  if (path === '/register') {
    show('register');
    await initRegisterPage();
    return;
  }
  if (path === '/forgot') {
    show('forgot');
    await initForgotPage();
    return;
  }
  if (path === '/reset') {
    const token = new URLSearchParams(location.search).get('token') || '';
    show('reset');
    await initResetPage(token);
    return;
  }
  try {
    const me = await api('/api/me');
    state.user = me.user;
    state.onlyofficeReady = !!(me.user && me.user.onlyoffice && me.user.onlyoffice.ready);
    await enterApp();
  } catch {
    show('login');
    await refreshForgotLink();
    const params = new URLSearchParams(location.search);
    const registered = params.get('registered');
    if (registered) {
      const loginUser = $('#login-form')?.username;
      if (loginUser) loginUser.value = registered;
    }
    const oidcErr = params.get('oidc_error');
    if (oidcErr) {
      const el = $('#login-error');
      if (el) {
        el.textContent = oidcErr;
        el.hidden = false;
      }
    }
    if (registered || oidcErr) {
      history.replaceState(null, '', '/');
    }
  }
}

async function enterApp() {
  $('#whoami').textContent = state.user.username + (state.user.isAdmin ? ' (admin)' : '');
  if (state.user.mustChangePassword || state.user.mustEnrollTotp) {
    show('settings');
    await openSettings();
    return;
  }
  show('main');
  applyViewMode();
  renderSidebarQuota();
  await loadRoots();
  await loadList();
}

function renderSidebarQuota() {
  const box = $('#sidebar-quota');
  const text = $('#sidebar-quota-text');
  const bar = $('#sidebar-quota-bar');
  if (!box || !state.user) return;
  box.hidden = false;
  const used = state.user.usedBytes || 0;
  const quota = state.user.quotaBytes || 0;
  if (quota > 0) {
    const pct = Math.min(100, Math.round(used / quota * 100));
    text.textContent = `${fmtSize(used)} / ${fmtQuota(quota)} (${pct}%)`;
    bar.hidden = false;
    bar.classList.toggle('over', used > quota);
    bar.querySelector('span').style.width = pct + '%';
  } else {
    text.textContent = `${fmtSize(used)} / unlimited`;
    bar.hidden = true;
  }
}

async function refreshMe() {
  const me = await api('/api/me');
  state.user = me.user;
  state.onlyofficeReady = !!(me.user && me.user.onlyoffice && me.user.onlyoffice.ready);
  $('#whoami').textContent = state.user.username + (state.user.isAdmin ? ' (admin)' : '');
  renderSidebarQuota();
}

function treeKey(rootId, path) {
  return rootId + ':' + (path || '');
}

async function fetchTreeDirs(rootId, path) {
  const key = treeKey(rootId, path);
  if (rootId === 'trash') {
    state.treeChildren.set(key, []);
    return [];
  }
  const data = await api(`/api/list?root=${encodeURIComponent(rootId)}&path=${encodeURIComponent(path || '')}`);
  const dirs = (data.entries || []).filter((e) => e.isDir).sort((a, b) => a.name.localeCompare(b.name));
  state.treeChildren.set(key, dirs);
  return dirs;
}

function invalidateTreeCache(rootId) {
  for (const k of [...state.treeChildren.keys()]) {
    if (k === rootId + ':' || k.startsWith(rootId + ':')) state.treeChildren.delete(k);
  }
}

function showLoginStep(step) {
  $('#login-form').hidden = step !== 'password';
  $('#totp-form').hidden = step !== 'totp';
  $('#totp-enroll-login').hidden = step !== 'enroll';
}

function applyOidcLoginButton(data) {
  const box = $('#oidc-login');
  const divider = $('#login-divider');
  const link = $('#oidc-start');
  const on = !!(data && data.oidcEnabled);
  if (box) box.hidden = !on;
  if (divider) divider.hidden = !on;
  if (link) {
    link.textContent = (data && data.oidcLabel) ? data.oidcLabel : 'Sign in with SSO';
    const rememberEl = $('#login-form')?.rememberMe;
    const stay = !!(rememberEl && rememberEl.checked);
    link.href = stay ? '/api/oidc/start?rememberMe=1' : '/api/oidc/start';
  }
}

async function refreshAuthFeatures() {
  const forgot = $('#link-forgot');
  const signup = $('#link-signup');
  const rememberLabel = $('#remember-me-label');
  try {
    const data = await api('/api/auth/features');
    if (forgot) forgot.hidden = !data.forgotPassword;
    if (signup) signup.hidden = !data.openRegistration;
    if (rememberLabel) rememberLabel.hidden = !data.allowRememberMe;
    applyOidcLoginButton(data);
    const hint = $('#signup-policy-hint');
    if (hint) {
      hint.textContent = data.passwordPolicyHint
        ? ('Password: ' + data.passwordPolicyHint)
        : '';
    }
    const sf = $('#signup-form');
    if (sf && data.passwordPolicy && data.passwordPolicy.minLength) {
      sf.password.minLength = data.passwordPolicy.minLength;
      sf.confirm.minLength = data.passwordPolicy.minLength;
    }
    return data;
  } catch {
    if (forgot) forgot.hidden = true;
    if (signup) signup.hidden = true;
    if (rememberLabel) rememberLabel.hidden = true;
    applyOidcLoginButton(null);
    return null;
  }
}

async function refreshForgotLink() {
  return refreshAuthFeatures();
}

async function startLoginEnroll(pendingToken) {
  const data = await api('/api/login/totp/setup', {
    method: 'POST',
    body: JSON.stringify({ pendingToken }),
  });
  const img = $('#login-totp-qr');
  if (data.qrDataUrl) {
    img.src = data.qrDataUrl;
    img.hidden = false;
  }
  $('#login-totp-secret').textContent = data.secret || '';
  showLoginStep('enroll');
}

$('#login-form')?.addEventListener('change', () => {
  const link = $('#oidc-start');
  if (!link || $('#oidc-login')?.hidden) return;
  const rememberEl = $('#login-form')?.rememberMe;
  const stay = !!(rememberEl && rememberEl.checked);
  link.href = stay ? '/api/oidc/start?rememberMe=1' : '/api/oidc/start';
});

$('#login-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  $('#login-error').hidden = true;
  try {
    const rememberEl = e.target.rememberMe;
    const data = await api('/api/login', {
      method: 'POST',
      body: JSON.stringify({
        username: fd.get('username'),
        password: fd.get('password'),
        rememberMe: !!(rememberEl && rememberEl.checked),
      }),
    });
    if (data.needTotp) {
      state.pendingToken = data.pendingToken;
      state.pendingUsername = data.username;
      $('#totp-form').reset();
      $('#totp-error').hidden = true;
      showLoginStep('totp');
      return;
    }
    if (data.needTotpSetup) {
      state.pendingToken = data.pendingToken;
      state.pendingUsername = data.username;
      $('#totp-enroll-error').hidden = true;
      await startLoginEnroll(data.pendingToken);
      return;
    }
    state.user = data.user;
    state.onlyofficeReady = !!(data.user && data.user.onlyoffice && data.user.onlyoffice.ready);
    state.pendingToken = null;
    await enterApp();
  } catch (err) {
    $('#login-error').textContent = err.message;
    $('#login-error').hidden = false;
  }
});

$('#totp-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  $('#totp-error').hidden = true;
  try {
    const data = await api('/api/login/totp', {
      method: 'POST',
      body: JSON.stringify({ pendingToken: state.pendingToken, code: fd.get('code') }),
    });
    state.user = data.user;
    state.onlyofficeReady = !!(data.user && data.user.onlyoffice && data.user.onlyoffice.ready);
    state.pendingToken = null;
    showLoginStep('password');
    await enterApp();
  } catch (err) {
    $('#totp-error').textContent = err.message;
    $('#totp-error').hidden = false;
  }
});

$('#btn-totp-back').addEventListener('click', () => {
  state.pendingToken = null;
  showLoginStep('password');
});

$('#totp-enroll-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  $('#totp-enroll-error').hidden = true;
  try {
    const data = await api('/api/login/totp/enable', {
      method: 'POST',
      body: JSON.stringify({ pendingToken: state.pendingToken, code: fd.get('code') }),
    });
    state.user = data.user;
    state.onlyofficeReady = !!(data.user && data.user.onlyoffice && data.user.onlyoffice.ready);
    state.pendingToken = null;
    if (data.backupCodes && data.backupCodes.length) {
      alert('2FA enabled. Save these backup codes:\n\n' + data.backupCodes.join('\n'));
    }
    showLoginStep('password');
    await enterApp();
  } catch (err) {
    $('#totp-enroll-error').textContent = err.message;
    $('#totp-enroll-error').hidden = false;
  }
});

$('#btn-enroll-back').addEventListener('click', () => {
  state.pendingToken = null;
  showLoginStep('password');
});

$('#btn-logout').addEventListener('click', async () => {
  try {
    await api('/api/logout', { method: 'POST', body: '{}' });
  } catch {}
  state.user = null;
  state.pendingToken = null;
  showLoginStep('password');
  show('login');
  // Features (including the SSO button) are loaded on the first unauthenticated
  // paint. After an SSO redirect or a session restore, logout must reload them.
  await refreshAuthFeatures();
});

$('#btn-settings').addEventListener('click', async () => {
  show('settings');
  await openSettings();
});
$('#btn-back-settings').addEventListener('click', () => {
  if (state.user && (state.user.mustChangePassword || state.user.mustEnrollTotp)) {
    return;
  }
  show('main');
});

function setSettingsTab(tab) {
  document.querySelectorAll('.settings-nav-item').forEach((btn) => {
    const on = btn.dataset.tab === tab;
    btn.classList.toggle('active', on);
    btn.setAttribute('aria-current', on ? 'page' : 'false');
  });
  document.querySelectorAll('.settings-panel').forEach((panel) => {
    const on = panel.id === 'tab-' + tab;
    panel.classList.toggle('active', on);
    panel.hidden = !on;
  });
  if (tab === 'system') startSystemStatusPoll();
  else stopSystemStatusPoll();
}

let systemStatusTimer = null;
let systemStatusBusy = false;

function stopSystemStatusPoll() {
  if (systemStatusTimer) {
    clearInterval(systemStatusTimer);
    systemStatusTimer = null;
  }
}

function startSystemStatusPoll() {
  stopSystemStatusPoll();
  loadSystemStatus();
  systemStatusTimer = setInterval(loadSystemStatus, 5000);
}

function setStatusBar(id, pct) {
  const bar = $('#' + id);
  if (!bar) return;
  if (pct == null || Number.isNaN(Number(pct))) {
    bar.hidden = true;
    return;
  }
  const n = Math.max(0, Number(pct));
  bar.hidden = false;
  bar.classList.toggle('over', n > 100);
  const span = bar.querySelector('span');
  if (span) span.style.width = Math.min(100, n) + '%';
}

function load1Decimal(n) {
  const v = Number(n);
  if (Number.isNaN(v)) return '';
  return v.toFixed(1);
}


function renderGPUStatus(gpu) {
  const text = $('#system-gpu-text');
  const flags = $('#system-gpu-flags');
  const list = $('#system-gpu-list');
  const note = $('#system-gpu-note');
  if (!text) return;
  if (!gpu) {
    text.textContent = 'Unavailable';
    if (flags) flags.textContent = '';
    if (list) list.innerHTML = '';
    if (note) note.textContent = '';
    return;
  }
  text.textContent = gpu.summary || 'No GPU acceleration device found';
  const parts = [];
  parts.push('/dev/dri: ' + (gpu.devDri ? 'present' : 'not found'));
  parts.push('renderD128: ' + (gpu.renderD128 ? 'present' : 'not found'));
  const mode = gpu.thumbHWAccel || (gpu.thumbVAAPI ? 'auto' : 'off');
  parts.push('THUMB_HWACCEL=' + mode);
  if (gpu.selectedBackend) {
    parts.push('thumb path: ' + gpu.selectedBackend + (gpu.selectedDevice ? ' (' + gpu.selectedDevice + ')' : ''));
  }
  if (gpu.canUseHardware) {
    parts.push('current thumb path can use the detected GPU (ffmpeg still falls back to software on failure)');
  } else if (mode === 'off') {
    parts.push('hardware thumbnail acceleration is off');
  } else {
    parts.push('current thumb path cannot use a detected GPU (software ffmpeg)');
  }
  if (flags) flags.textContent = parts.join(' · ');
  if (list) {
    list.innerHTML = '';
    (gpu.devices || []).forEach((d) => {
      const li = document.createElement('li');
      const bits = [];
      const vendor = d.vendorName || 'Unknown';
      bits.push(vendor + (d.type && d.type !== 'unknown' ? ' (' + d.type + ')' : ''));
      if (d.name) bits.push(d.name);
      if (d.driver) bits.push('driver ' + d.driver);
      if (d.pciId) bits.push(d.pciId);
      if (d.card) bits.push(d.card);
      if (d.render) bits.push('/dev/dri/' + d.render);
      bits.push(d.vaapiCapable ? 'VAAPI-capable' : 'not used by current VAAPI thumb path');
      li.textContent = bits.join(' · ');
      list.appendChild(li);
    });
  }
  if (note) note.textContent = gpu.thumbNote || '';
}

async function loadSystemStatus() {
  if (systemStatusBusy) return;
  const errEl = $('#system-status-error');
  if (!errEl) return;
  systemStatusBusy = true;
  try {
    const data = await api('/api/admin/system/status');
    errEl.hidden = true;
    const cpu = data.cpu || {};
    const cpuParts = [];
    if (cpu.percent != null) cpuParts.push(load1Decimal(cpu.percent) + '%');
    if (cpu.load1 != null) {
      cpuParts.push('load ' + load1Decimal(cpu.load1) + ' / ' + load1Decimal(cpu.load5) + ' / ' + load1Decimal(cpu.load15));
    }
    if (cpu.cores) cpuParts.push(cpu.cores + (cpu.cores === 1 ? ' core' : ' cores'));
    $('#system-cpu-text').textContent = cpuParts.join(' · ') || 'Unavailable';
    setStatusBar('system-cpu-bar', cpu.percent);

    const mem = data.memory || {};
    if (mem.totalBytes) {
      const pct = mem.percent != null ? load1Decimal(mem.percent) + '%' : '';
      $('#system-ram-text').textContent = fmtSize(mem.usedBytes) + ' / ' + fmtSize(mem.totalBytes) + (pct ? ' (' + pct + ')' : '');
      setStatusBar('system-ram-bar', mem.percent);
    } else {
      $('#system-ram-text').textContent = 'Unavailable';
      setStatusBar('system-ram-bar', null);
    }

    const q = data.quotas || {};
    const used = q.usedBytes || 0;
    const assigned = q.assignedBytes || 0;
    const unlimited = q.unlimitedUsers || 0;
    const totalUsers = q.userCount || 0;
    if (assigned > 0) {
      const pct = q.percent != null ? load1Decimal(q.percent) + '%' : '';
      $('#system-quota-text').textContent = fmtSize(used) + ' used / ' + fmtSize(assigned) + ' assigned' + (pct ? ' (' + pct + ')' : '');
      setStatusBar('system-quota-bar', q.percent);
    } else {
      $('#system-quota-text').textContent = fmtSize(used) + ' used · no assigned quota total';
      setStatusBar('system-quota-bar', null);
    }
    const note = $('#system-quota-note');
    if (unlimited && totalUsers) {
      if (unlimited === totalUsers) {
        note.textContent = 'All ' + totalUsers + ' users have unlimited quota (0), so they are not included in the assigned total.';
      } else {
        note.textContent = unlimited + ' of ' + totalUsers + ' users have unlimited quota (0) and are not included in the assigned total. Homes used still includes everyone.';
      }
    } else {
      note.textContent = totalUsers ? 'Usage includes each user’s home plus files they wrote into shared folders (and items in the recycle bin until purged). Legacy share files with no recorded writer are unbilled.' : '';
    }

    const disk = data.disk || {};
    const diskPath = $('#system-disk-path');
    if (disk.ok) {
      const pct = disk.percent != null ? load1Decimal(disk.percent) + '%' : '';
      $('#system-disk-text').textContent = fmtSize(disk.usedBytes) + ' used / ' + fmtSize(disk.totalBytes) + ' total' + (pct ? ' (' + pct + ')' : '') + ', ' + fmtSize(disk.freeBytes) + ' free';
      setStatusBar('system-disk-bar', disk.percent);
      diskPath.textContent = disk.path ? ('Path: ' + disk.path) : '';
    } else {
      $('#system-disk-text').textContent = 'Unavailable';
      setStatusBar('system-disk-bar', null);
      diskPath.textContent = disk.path ? ('Path: ' + disk.path) : '';
    }
    renderGPUStatus(data.gpu);
    const updated = $('#system-status-updated');
    if (updated) updated.textContent = 'Updated ' + new Date().toLocaleTimeString();
  } catch (err) {
    errEl.textContent = err.message || 'Could not load server status';
    errEl.hidden = false;
  } finally {
    systemStatusBusy = false;
  }
}

function applySettingsGate() {
  const u = state.user || {};
  const mustPw = !!u.mustChangePassword;
  const must2fa = !!u.mustEnrollTotp && !mustPw;
  const banner = $('#settings-gate-banner');
  const back = $('#btn-back-settings');
  if (mustPw) {
    banner.textContent = 'Password change required before continuing';
    banner.hidden = false;
    back.hidden = true;
  } else if (must2fa) {
    banner.textContent = '2FA setup required before continuing';
    banner.hidden = false;
    back.hidden = true;
  } else {
    banner.hidden = true;
    banner.textContent = '';
    back.hidden = false;
  }
  document.querySelectorAll('.settings-nav-item').forEach((btn) => {
    const tab = btn.dataset.tab;
    let locked = false;
    if (mustPw) locked = tab !== 'password';
    else if (must2fa) locked = tab !== '2fa';
    btn.disabled = locked;
    btn.setAttribute('aria-disabled', locked ? 'true' : 'false');
  });
}

function applyPasswordFormMode(u) {
  const has = !!(u && u.hasLocalPassword);
  const nav = $('#nav-password');
  if (nav) nav.textContent = has ? 'Change password' : 'Set password';
  const title = $('#password-tab-title');
  if (title) title.textContent = has ? 'Change password' : 'Set password';
  const note = $('#password-set-note');
  if (note) note.hidden = has;
  const cur = $('#current-password-label');
  if (cur) {
    cur.hidden = !has;
    const input = cur.querySelector('input');
    if (input) {
      input.required = has;
      if (!has) input.value = '';
    }
  }
  const btn = $('#password-submit');
  if (btn) btn.textContent = has ? 'Update password' : 'Set password';
}

function updatePasswordPolicyHints(u) {
  const hint = (u && (u.passwordPolicyHint || (u.passwordPolicy && formatPolicyHint(u.passwordPolicy)))) || 'min 4 characters';
  const el = $('#password-policy-hint');
  if (el) el.textContent = 'Requirements: ' + hint;
  const adminHint = $('#admin-password-policy-hint');
  if (adminHint) adminHint.textContent = 'Policy: ' + hint;
  const form = $('#change-password');
  if (form && u && u.passwordPolicy && u.passwordPolicy.minLength) {
    form.newPassword.minLength = u.passwordPolicy.minLength;
    form.confirmPassword.minLength = u.passwordPolicy.minLength;
  }
}

function formatPolicyHint(p) {
  if (!p) return 'min 4 characters';
  const parts = [`min ${p.minLength || 4} characters`];
  if (p.requireLetters) parts.push('letters');
  if (p.requireDigits) parts.push('digits');
  if (p.requireSymbols) parts.push('symbols');
  return parts.join(', ');
}

document.querySelectorAll('.settings-nav-item').forEach((btn) => {
  btn.addEventListener('click', async () => {
    if (btn.disabled) return;
    const tab = btn.dataset.tab;
    setSettingsTab(tab);
    if (tab === 'users' || tab === 'shares') {
      try { await refreshAdmin(); } catch (err) { console.error(err); }
    }
    if (tab === 'sessions' || tab === 'registration' || tab === 'security' || tab === 'email' || tab === 'storage-quota' || tab === 'sso') {
      try { await loadSystemSettings(); } catch (err) { console.error(err); }
    }
    if (tab === 'links') {
      try { await refreshLinks(); } catch (err) { console.error(err); }
    }
    if (tab === 'my-sessions') {
      try { await refreshMySessions(); } catch (err) { console.error(err); }
    }
    if (tab === 'audit') {
      try { await refreshAudit(0); } catch (err) { console.error(err); }
    }
  });
});

async function openSettings() {
  try { await refreshMe(); } catch { /* keep cached */ }
  const u = state.user;
  document.querySelectorAll('.settings-nav-item.admin-only').forEach((el) => {
    el.hidden = !u.isAdmin;
  });
  if (u.isAdmin) {
    try { await loadSystemSettings(); } catch (err) { console.error(err); }
  }
  applySettingsGate();
  updatePasswordPolicyHints(u);
  applyPasswordFormMode(u);
  let tab = 'account';
  if (u.mustChangePassword) tab = 'password';
  else if (u.mustEnrollTotp) tab = '2fa';
  setSettingsTab(tab);
  const acc = $('#settings-account');
  const badges = [];
  if (u.isAdmin) badges.push('<span class="badge admin">admin</span>');
  const used = u.usedBytes || 0;
  const quota = u.quotaBytes || 0;
  const pct = quota > 0 ? Math.min(100, Math.round(used / quota * 100)) : 0;
  let webdavNote = '';
  if (u.webdav && u.webdav.enabled) {
    webdavNote = `<div class="meta">WebDAV: <code>${window.location.origin + (u.webdav.path || '/dav/')}</code></div>`;
    if (u.hasLocalPassword) {
      webdavNote += `<p class="muted">Mount this URL as a network drive with your Filehub username and password over HTTPS. Do not expose the app port directly. WebDAV uses your account password even if 2FA is enabled for the web sign-in, or if you signed in with SSO.</p>`;
    } else {
      webdavNote += `<p class="muted">WebDAV needs a Filehub password. This account has none yet — set one under Set password before mounting a network drive.</p>`;
    }
  }
  const ssoNote = u.oidcSession
    ? '<p class="muted">You signed in with single sign-on. Filehub 2FA is not required for this session.</p>'
    : (u.oidcLinked ? '<p class="muted">This account is linked to an OpenID Connect identity and can also sign in with SSO.</p>' : '');
  acc.innerHTML = `
    <div><strong>${u.username}</strong> ${badges.join(' ')}</div>
    <div class="meta">Email: ${u.email ? `<code>${u.email}</code>` : '<span class="muted">not set (ask admin)</span>'}</div>
    <div class="meta">Home: <code>${u.homePath || ''}</code></div>
    <div class="meta">Storage: ${fmtSize(used)} / ${fmtQuota(quota)}${quota ? ` (${pct}%)` : ''}</div>
    ${ssoNote}
    ${webdavNote}
  `;
  const bar = $('#settings-quota-bar');
  if (quota > 0) {
    bar.hidden = false;
    bar.classList.toggle('over', used > quota);
    bar.querySelector('span').style.width = pct + '%';
  } else {
    bar.hidden = true;
  }
  $('#change-password').reset();
  $('#change-password-msg').hidden = true;
  renderTotpSettings();
}

$('#change-password').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const msg = $('#change-password-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  if (fd.get('newPassword') !== fd.get('confirmPassword')) {
    msg.textContent = 'New passwords do not match';
    msg.classList.add('error');
    msg.hidden = false;
    return;
  }
  try {
    const data = await api('/api/me/password', {
      method: 'POST',
      body: JSON.stringify({
        currentPassword: fd.get('currentPassword'),
        newPassword: fd.get('newPassword'),
      }),
    });
    if (data.user) state.user = data.user;
    msg.textContent = state.user && state.user.hasLocalPassword ? 'Password updated' : 'Password set';
    msg.hidden = false;
    e.target.reset();
    updatePasswordPolicyHints(state.user);
    applyPasswordFormMode(state.user);
    applySettingsGate();
    if (state.user.mustEnrollTotp) {
      setSettingsTab('2fa');
      renderTotpSettings();
      return;
    }
    if (!state.user.mustChangePassword && !state.user.mustEnrollTotp) {
      await enterApp();
    }
  } catch (err) {
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

const SIDEBAR_MQ = window.matchMedia ? window.matchMedia('(max-width: 800px)') : null;

function syncMainTopbarHeight() {
  const bar = document.querySelector('#main-view > .topbar');
  const h = bar ? Math.round(bar.getBoundingClientRect().height) : 0;
  document.documentElement.style.setProperty('--main-topbar-height', (h || 56) + 'px');
}

function isMobileSidebar() {
  return !!(SIDEBAR_MQ && SIDEBAR_MQ.matches);
}

function setMobileSidebarOpen(open) {
  const want = !!(open && isMobileSidebar());
  document.body.classList.toggle('sidebar-open', want);
  syncMainTopbarHeight();
  const btn = $('#btn-sidebar');
  if (btn) {
    btn.setAttribute('aria-expanded', want ? 'true' : 'false');
    btn.title = want ? 'Close roots' : 'Open roots';
    btn.setAttribute('aria-label', btn.title);
  }
}

function closeMobileSidebar() {
  if (document.body.classList.contains('sidebar-open')) setMobileSidebarOpen(false);
}

function toggleMobileSidebar() {
  setMobileSidebarOpen(!document.body.classList.contains('sidebar-open'));
}

(function setupMobileSidebar() {
  const btn = $('#btn-sidebar');
  const closeBtn = $('#btn-sidebar-close');
  const backdrop = $('#sidebar-backdrop');
  btn?.addEventListener('click', (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    toggleMobileSidebar();
  });
  closeBtn?.addEventListener('click', (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    closeMobileSidebar();
  });
  backdrop?.addEventListener('click', () => closeMobileSidebar());
  document.addEventListener('keydown', (ev) => {
    if (ev.key === 'Escape' && document.body.classList.contains('sidebar-open')) {
      ev.preventDefault();
      closeMobileSidebar();
    }
  });
  const onViewport = () => {
    syncMainTopbarHeight();
    if (!isMobileSidebar()) setMobileSidebarOpen(false);
  };
  if (SIDEBAR_MQ) {
    if (SIDEBAR_MQ.addEventListener) SIDEBAR_MQ.addEventListener('change', onViewport);
    else if (SIDEBAR_MQ.addListener) SIDEBAR_MQ.addListener(onViewport);
  }
  window.addEventListener('resize', syncMainTopbarHeight);
  syncMainTopbarHeight();
})();

async function loadRoots() {
  const data = await api('/api/roots');
  state.roots = data.roots;
  state.trashCount = Number(data.trashCount) || 0;
  const ul = $('#root-list');
  ul.innerHTML = '';
  for (const r of data.roots) {
    const suffix = (r.id === 'incoming') ? '' : (r.perm === 'read' ? ' (ro)' : '');
    const li = await renderTreeNode(r, '', r.name + suffix, 0);
    ul.appendChild(li);
  }
  await loadAlbums();
}

async function loadAlbums() {
  try {
    const data = await api('/api/albums');
    state.albums = data.albums || [];
  } catch {
    state.albums = [];
  }
  renderAlbumList();
}

function albumLabel(a) {
  return (a && (a.name || a.path)) || 'Album';
}

function renderAlbumList() {
  const ul = $('#album-list');
  const empty = $('#album-empty');
  if (!ul) return;
  ul.innerHTML = '';
  const list = state.albums || [];
  if (empty) empty.hidden = list.length > 0;
  for (const a of list) {
    const li = document.createElement('li');
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'album-nav-item';
    b.textContent = albumLabel(a);
    const root = (state.roots || []).find((r) => r.id === a.root);
    const rootName = (root && root.name) || a.root || '';
    b.title = rootName ? (rootName + ' / ' + (a.path || albumLabel(a))) : albumLabel(a);
    if (state.root === a.root && (state.path || '') === (a.path || '')) b.classList.add('active');
    b.onclick = async () => {
      state.root = a.root;
      state.path = a.path || '';
      state.albumNav = true;
      state.albumOffKey = null;
      closeMobileSidebar();
      await loadRoots();
      await loadList();
    };
    li.appendChild(b);
    ul.appendChild(li);
  }
}

async function renderTreeNode(root, path, label, depth) {
  const li = document.createElement('li');
  li.className = 'tree-node';
  li.style.setProperty('--depth', depth);
  const key = treeKey(root.id, path);
  const row = document.createElement('div');
  row.className = 'tree-row';
  if (root.id === state.root && (path || '') === (state.path || '')) row.classList.add('active');

  const twisty = document.createElement('button');
  twisty.type = 'button';
  twisty.className = 'tree-twisty ghost';
  const expanded = state.treeExpanded.has(key);
  twisty.textContent = expanded ? '▾' : '▸';
  twisty.title = expanded ? 'Collapse' : 'Expand';
  twisty.onclick = async (ev) => {
    ev.stopPropagation();
    if (state.treeExpanded.has(key)) {
      state.treeExpanded.delete(key);
    } else {
      state.treeExpanded.add(key);
      try { await fetchTreeDirs(root.id, path); } catch { /* ignore */ }
    }
    await loadRoots();
  };

  const b = document.createElement('button');
  b.type = 'button';
  b.className = 'tree-label';
  b.textContent = label;
  b.title = label;
  b.onclick = async () => {
    state.root = root.id;
    state.path = path || '';
    state.treeExpanded.add(key);
    closeMobileSidebar();
    try { await fetchTreeDirs(root.id, path); } catch { /* ignore */ }
    await loadRoots();
    await loadList();
  };

  row.appendChild(twisty);
  row.appendChild(b);
  if (root.id === 'trash' && !path) {
    const n = state.trashCount || 0;
    if (n > 0) {
      const badge = document.createElement('span');
      badge.className = 'tree-count-badge';
      const shown = formatTrashCount(n);
      badge.textContent = shown;
      badge.setAttribute('aria-label', n === 1 ? '1 item in Recycle bin' : shown + ' items in Recycle bin');
      row.appendChild(badge);
    }
  }
  li.appendChild(row);

  if (expanded) {
    let kids = state.treeChildren.get(key);
    if (!kids) {
      try { kids = await fetchTreeDirs(root.id, path); }
      catch { kids = []; }
    }
    if (kids && kids.length) {
      const childUl = document.createElement('ul');
      childUl.className = 'tree-children';
      for (const d of kids) {
        const child = await renderTreeNode(root, d.path, d.name, depth + 1);
        childUl.appendChild(child);
      }
      li.appendChild(childUl);
    }
  }
  return li;
}

function renderBreadcrumbs() {
  const nav = $('#breadcrumbs');
  nav.innerHTML = '';
  const parts = state.path ? state.path.split('/') : [];
  const add = (label, path) => {
    const b = document.createElement('button');
    b.textContent = label;
    b.onclick = async () => { state.path = path; await loadList(); };
    nav.appendChild(b);
    nav.appendChild(document.createTextNode(' / '));
  };
  const root = (state.roots || []).find((r) => r.id === state.root);
  add((root && root.name) || 'root', '');
  let acc = '';
  for (const p of parts) {
    acc = acc ? acc + '/' + p : p;
    let label = p;
    if (state.root === 'incoming' && state.incomingNames && state.incomingNames[p]) {
      label = state.incomingNames[p];
    }
    add(label, acc);
  }
}

function fileExt(name) {
  const i = String(name || '').lastIndexOf('.');
  if (i <= 0) return '';
  return name.slice(i + 1).toLowerCase();
}

function sortEntries(entries) {
  const dir = state.sortDir === 'desc' ? -1 : 1;
  const by = state.sortBy;
  return entries.slice().sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    let cmp = 0;
    if (by === 'size') {
      cmp = (a.isDir ? 0 : Number(a.size) || 0) - (b.isDir ? 0 : Number(b.size) || 0);
    } else if (by === 'modified') {
      cmp = new Date(a.modTime).getTime() - new Date(b.modTime).getTime();
      if (Number.isNaN(cmp)) cmp = 0;
    } else if (by === 'type') {
      const ta = a.isDir ? '' : (fileExt(a.name) || a.kind || '');
      const tb = b.isDir ? '' : (fileExt(b.name) || b.kind || '');
      cmp = ta.localeCompare(tb, undefined, { sensitivity: 'base' });
    } else {
      cmp = a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true });
    }
    if (cmp === 0 && by !== 'name') {
      cmp = a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true });
    }
    return cmp * dir;
  });
}

function applySortControls() {
  const sel = $('#sort-by');
  const btn = $('#btn-sort-dir');
  if (sel) sel.value = state.sortBy;
  if (btn) {
    const asc = state.sortDir !== 'desc';
    btn.textContent = asc ? '↑ Asc' : '↓ Desc';
    btn.setAttribute('aria-label', asc ? 'Ascending' : 'Descending');
    btn.title = asc ? 'Ascending — click for descending' : 'Descending — click for ascending';
  }
  document.querySelectorAll('.th-sort').forEach((el) => {
    const key = el.dataset.sort;
    const active = key === state.sortBy;
    el.classList.toggle('active', active);
    el.setAttribute('aria-sort', active ? (state.sortDir === 'desc' ? 'descending' : 'ascending') : 'none');
    const arrow = active ? (state.sortDir === 'desc' ? ' ↓' : ' ↑') : '';
    const labels = { name: 'Name', size: 'Size', modified: 'Modified' };
    el.textContent = (labels[key] || key) + arrow;
  });
}

function persistSort() {
  try {
    localStorage.setItem('filehub-sort-by', state.sortBy);
    localStorage.setItem('filehub-sort-dir', state.sortDir);
  } catch { /* ignore */ }
}

function setSort(by, dir) {
  if (SORT_KEYS.has(by)) state.sortBy = by;
  if (dir === 'asc' || dir === 'desc') state.sortDir = dir;
  persistSort();
  applySortControls();
syncUploadPanelVisibility();
  if (state.entries) {
    renderEntries(sortEntries(state.entries), state.perm === 'write');
  }
}

function cycleSortHeader(by) {
  if (!SORT_KEYS.has(by)) return;
  if (state.sortBy === by) {
    setSort(by, state.sortDir === 'asc' ? 'desc' : 'asc');
  } else {
    setSort(by, 'asc');
  }
}

function setCreateActionsVisible(visible) {
  const mkdir = $('#btn-mkdir');
  if (mkdir) {
    mkdir.hidden = !visible;
    mkdir.disabled = !visible;
  }
  const fileInput = $('#file-input');
  const fileLabel = fileInput && fileInput.closest('label');
  if (fileInput) fileInput.disabled = !visible;
  if (fileLabel) fileLabel.hidden = !visible;
  const folderInput = $('#folder-input');
  const folderLabel = folderInput && folderInput.closest('label');
  if (folderInput) folderInput.disabled = !visible;
  if (folderLabel) folderLabel.hidden = !visible;
  updateOfficeCreateButtons(visible);
}

function setOfficeNewMenuOpen(open) {
  const menu = $('#oo-new-menu');
  const btn = $('#btn-new-office');
  if (menu) menu.hidden = !open;
  if (btn) btn.setAttribute('aria-expanded', open ? 'true' : 'false');
}

function updateOfficeCreateButtons(writeVisible) {
  const show = !!writeVisible && !!state.onlyofficeReady && state.root !== 'trash' && state.perm === 'write';
  const wrap = $('#oo-new-wrap');
  if (wrap) wrap.hidden = !show;
  const btn = $('#btn-new-office');
  if (btn) btn.disabled = !show;
  if (!show) setOfficeNewMenuOpen(false);
  const hint = $('#oo-create-hint');
  if (hint) hint.hidden = !show;
}

async function loadList() {
  if (state.searchQuery) {
    await runFileSearch(state.searchQuery, false);
    return;
  }
  const data = await api(`/api/list?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(state.path)}`);
  state.perm = data.perm;
  state.canDelete = !!data.canDelete;
  const canWrite = state.perm === 'write';
  const inTrash = state.root === 'trash';
  setCreateActionsVisible(canWrite && !inTrash);
  const trashBar = $('#trash-actions');
  if (trashBar) trashBar.hidden = !inTrash;
  state.entries = data.entries || [];
  state.isAlbum = !!data.album;
  state.inAlbum = !!data.inAlbum;
  const folderKey = treeKey(state.root, state.path);
  if (state.isAlbum && state.albumOffKey !== folderKey) {
    state.albumNav = true;
  }
  if ((state.isAlbum || state.inAlbum) && state.albumNav && !state.searchQuery) {
    state.viewMode = 'album';
  } else if (state.viewMode === 'album') {
    state.viewMode = (localStorage.getItem('filehub-view') === 'icons' ? 'icons' : 'list');
  }
  if (state.root === 'incoming' && !(state.path || '')) {
    state.incomingNames = {};
    for (const e of state.entries) {
      if (e && e.path) state.incomingNames[String(e.path).split('/')[0]] = e.name;
    }
  }
  renderBreadcrumbs();
  state.selected.clear();
  applyViewMode();
  applySortControls();
  updateFolderAlbumButton();
  renderEntries(sortEntries(state.entries), canWrite && !inTrash);
  updateBulkBar();
}

async function runFileSearch(q, updateInput) {
  q = (q || '').trim();
  state.searchQuery = q;
  const trashBar = $('#trash-actions');
  if (trashBar) trashBar.hidden = true;
  if (!q) {
    await loadList();
    return;
  }
  const data = await api('/api/search?q=' + encodeURIComponent(q));
  const results = (data.results || []).map((r) => Object.assign({}, r, {
    searchRoot: r.root,
    searchSource: r.source,
  }));
  state.perm = 'read';
  state.canDelete = false;
  state.isAlbum = false;
  state.inAlbum = false;
  if (state.viewMode === 'album') {
    state.viewMode = (localStorage.getItem('filehub-view') === 'icons' ? 'icons' : 'list');
  }
  setCreateActionsVisible(false);
  updateFolderAlbumButton();
  renderBreadcrumbs();
  const nav = $('#breadcrumbs');
  if (nav) {
    const mark = document.createElement('span');
    mark.className = 'muted';
    mark.textContent = 'Search: ' + q;
    nav.appendChild(mark);
  }
  state.entries = results;
  state.selected.clear();
  applyViewMode();
  applySortControls();
  renderEntries(sortEntries(state.entries), false);
  updateBulkBar();
}

function applyViewMode() {
  const listBtn = $('#btn-view-list');
  const iconsBtn = $('#btn-view-icons');
  const albumBtn = $('#btn-view-album');
  const table = $('#files-table');
  const grid = $('#files-grid');
  const albumView = $('#album-view');
  const isAlbum = state.viewMode === 'album';
  const isIcons = state.viewMode === 'icons';
  listBtn.classList.toggle('active', !isIcons && !isAlbum);
  iconsBtn.classList.toggle('active', isIcons);
  listBtn.setAttribute('aria-pressed', (!isIcons && !isAlbum) ? 'true' : 'false');
  iconsBtn.setAttribute('aria-pressed', isIcons ? 'true' : 'false');
  if (albumBtn) {
    const show = !!(state.isAlbum || state.inAlbum) && !state.searchQuery;
    albumBtn.hidden = !show;
    albumBtn.classList.toggle('active', isAlbum);
    albumBtn.setAttribute('aria-pressed', isAlbum ? 'true' : 'false');
  }
  table.hidden = isIcons || isAlbum;
  grid.hidden = !isIcons || isAlbum;
  if (albumView) albumView.hidden = !isAlbum;
}

function setViewMode(mode) {
  if (mode === 'album') {
    state.albumNav = true;
    state.albumOffKey = null;
    state.viewMode = 'album';
  } else {
    if (state.isAlbum || state.inAlbum) {
      state.albumNav = false;
      state.albumOffKey = treeKey(state.root, state.path);
    }
    state.viewMode = mode === 'icons' ? 'icons' : 'list';
    try { localStorage.setItem('filehub-view', state.viewMode); } catch { /* ignore */ }
  }
  applyViewMode();
  if (state.entries) {
    renderEntries(sortEntries(state.entries), state.perm === 'write' && state.root !== 'trash');
  } else {
    loadList().catch((err) => console.error(err));
  }
}

$('#btn-view-list').addEventListener('click', () => setViewMode('list'));
$('#btn-view-icons').addEventListener('click', () => setViewMode('icons'));
$('#btn-view-album')?.addEventListener('click', () => setViewMode('album'));

$('#sort-by').addEventListener('change', (ev) => {
  setSort(ev.target.value, state.sortDir);
});
$('#btn-sort-dir').addEventListener('click', () => {
  setSort(state.sortBy, state.sortDir === 'asc' ? 'desc' : 'asc');
});
document.querySelectorAll('.th-sort').forEach((el) => {
  el.addEventListener('click', () => cycleSortHeader(el.dataset.sort));
});
applySortControls();

function entryIcon(e) {
  if (e.kind === 'dir') return '📁';
  if (e.kind === 'image') return '🖼️';
  if (e.kind === 'video') return '🎬';
  if (e.kind === 'pdf') return '📕';
  if (e.kind === 'office') return '📝';
  return '📄';
}

function isPreviewable(e) {
  return !e.isDir && (e.kind === 'image' || e.kind === 'video' || e.kind === 'pdf');
}

function isOfficeFile(e) {
  return !e.isDir && e.kind === 'office';
}

/** Effective write access for one row. Incoming root items carry their own grant perm. */
function entryCanWrite(e, listCanWrite) {
  if (e && (e.perm === 'write' || e.perm === 'read')) {
    return e.perm === 'write';
  }
  return !!listCanWrite;
}

function isIncomingShareRoot(e) {
  const root = (e && e.searchRoot) || state.root;
  if (root !== 'incoming') return false;
  const p = String((e && e.path) || '');
  return p !== '' && p.indexOf('/') === -1;
}

function entryPermLabel(e) {
  const p = (e && (e.perm === 'write' || e.perm === 'read')) ? e.perm : state.perm;
  return p === 'write' ? 'Read/Write' : 'Read only';
}

function useCompactRowActions() {
  return window.matchMedia && window.matchMedia('(max-width: 720px)').matches;
}

function closeOfficeEditor(opts) {
  const overlay = $('#oo-editor-overlay');
  const frame = $('#oo-editor-frame');
  if (frame) frame.src = 'about:blank';
  if (overlay) overlay.hidden = true;
  document.body.classList.remove('oo-editor-open');
  const opened = state.ooEditorOpen;
  state.ooEditorOpen = false;
  if (opened && !(opts && opts.fromPopstate) && history.state && history.state.filehubOoEditor) {
    history.back();
  }
}

function filehubAssetVersion() {
  const v = document.documentElement.getAttribute('data-filehub-version') || '';
  if (!v || v === '__FILEHUB_VERSION__') return '';
  return v;
}

function openOfficeEditor(e) {
  const itemRoot = e.searchRoot || state.root;
  if (itemRoot === 'trash' || !state.onlyofficeReady) return;
  const overlay = $('#oo-editor-overlay');
  const frame = $('#oo-editor-frame');
  const title = $('#oo-editor-title');
  if (!overlay || !frame) return;
  if (title) title.textContent = e.name || (e.path || '').split('/').pop() || 'Office editor';
  const hint = $('#oo-editor-coedit-hint');
  if (hint) {
    const canEdit = entryCanWrite(e, state.perm === 'write');
    hint.hidden = !canEdit;
    if (canEdit) hint.textContent = 'Others with Read/Write can edit this file at the same time.';
  }
  const assetVersion = filehubAssetVersion();
  const url = '/oo-editor.html?' + (assetVersion ? 'v=' + encodeURIComponent(assetVersion) + '&' : '') + 'embedded=1&root=' + encodeURIComponent(itemRoot) + '&path=' + encodeURIComponent(e.path);
  frame.src = url;
  overlay.hidden = false;
  document.body.classList.add('oo-editor-open');
  if (!state.ooEditorOpen) {
    history.pushState({ filehubOoEditor: true }, '', location.pathname + location.search + location.hash);
  }
  state.ooEditorOpen = true;
}

$('#oo-editor-back')?.addEventListener('click', () => closeOfficeEditor());
window.addEventListener('popstate', () => {
  if (state.ooEditorOpen) closeOfficeEditor({ fromPopstate: true });
});

function ensureOfficeExt(name, ext) {
  name = (name || '').trim();
  if (!name) return '';
  const lower = name.toLowerCase();
  if (!lower.endsWith(ext)) name += ext;
  return name.replace(/[\\/]/g, '-');
}

async function createOfficeFile(kind) {
  if (!state.onlyofficeReady || state.perm !== 'write' || state.root === 'trash') return;
  const kinds = {
    spreadsheet: { ext: '.xlsx', def: 'Spreadsheet.xlsx', prompt: 'Spreadsheet name', kind: 'spreadsheet' },
    presentation: { ext: '.pptx', def: 'Presentation.pptx', prompt: 'Presentation name', kind: 'presentation' },
    document: { ext: '.docx', def: 'Document.docx', prompt: 'Document name', kind: 'document' },
  };
  const meta = kinds[kind] || kinds.document;
  let name = prompt(meta.prompt, meta.def);
  if (name == null) return;
  name = ensureOfficeExt(name, meta.ext);
  if (!name) return;
  try {
    const data = await api('/api/onlyoffice/create', {
      method: 'POST',
      body: JSON.stringify({ root: state.root, path: state.path || '', name, kind: meta.kind }),
    });
    invalidateTreeCache(state.root);
    await loadRoots();
    await loadList();
    try { await refreshMe(); } catch { /* ignore */ }
    openOfficeEditor({ name: data.name || name, path: data.path || (state.path ? state.path + '/' + name : name), kind: 'office', isDir: false });
  } catch (err) {
    alert(err.message || String(err));
  }
}

$('#btn-new-office')?.addEventListener('click', (e) => {
  e.stopPropagation();
  const menu = $('#oo-new-menu');
  if (!menu) return;
  setOfficeNewMenuOpen(!!menu.hidden);
});
$('#btn-new-doc')?.addEventListener('click', () => {
  setOfficeNewMenuOpen(false);
  createOfficeFile('document');
});
$('#btn-new-sheet')?.addEventListener('click', () => {
  setOfficeNewMenuOpen(false);
  createOfficeFile('spreadsheet');
});
$('#btn-new-slides')?.addEventListener('click', () => {
  setOfficeNewMenuOpen(false);
  createOfficeFile('presentation');
});
document.addEventListener('click', (e) => {
  const wrap = $('#oo-new-wrap');
  if (!wrap || wrap.hidden) return;
  if (wrap.contains(e.target)) return;
  setOfficeNewMenuOpen(false);
});
document.addEventListener('click', (e) => {
  document.querySelectorAll('.row-overflow-menu').forEach((m) => {
    if (!m.hidden && !(m.parentElement && m.parentElement.contains(e.target))) m.hidden = true;
  });
});
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') setOfficeNewMenuOpen(false);
});

function revokePdfObjectUrls(root) {
  if (!root) return;
  root.querySelectorAll('iframe.pdf-frame, embed.pdf-frame, object.pdf-frame').forEach((el) => {
    const u = el.dataset.objectUrl || '';
    if (u.startsWith('blob:')) {
      try { URL.revokeObjectURL(u); } catch { /* ignore */ }
    }
    el.removeAttribute('src');
    el.removeAttribute('data');
  });
}

function appendPdfFrame(container, url, title) {
  const frame = document.createElement('iframe');
  frame.className = 'pdf-frame';
  frame.title = title || 'PDF';
  frame.setAttribute('loading', 'lazy');
  // Placeholder until blob is ready; avoids flashing a framed /api/raw that may be DENY'd.
  frame.src = 'about:blank';
  container.appendChild(frame);
  const tip = document.createElement('p');
  tip.className = 'muted pdf-loading';
  tip.textContent = 'Loading PDF…';
  container.appendChild(tip);
  fetch(url, { credentials: 'same-origin' })
    .then(async (res) => {
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const buf = await res.arrayBuffer();
      const blob = new Blob([buf], { type: 'application/pdf' });
      const obj = URL.createObjectURL(blob);
      frame.dataset.objectUrl = obj;
      frame.src = obj;
      tip.remove();
    })
    .catch((err) => {
      tip.textContent = 'Could not load PDF preview. Try Download.';
      tip.classList.add('error');
      // Fallback: direct URL (works when server allows SAMEORIGIN framing).
      frame.src = url;
      console.warn('pdf preview fetch failed', err);
    });
}

function joinPath(base, name) {
  return base ? base + '/' + name : name;
}

async function openEntry(e) {
  if (state.root === 'trash') {
    return;
  }
  if (e.searchRoot) {
    state.searchQuery = '';
    const input = $('#file-search');
    if (input) input.value = '';
    state.root = e.searchRoot;
    if (e.isDir) {
      state.path = e.path;
    } else {
      const parts = (e.path || '').split('/');
      parts.pop();
      state.path = parts.join('/');
    }
    await loadRoots();
    await loadList();
    return;
  }
  if (e.isDir) {
    if (e.album) {
      state.albumNav = true;
      state.albumOffKey = null;
    }
    state.path = e.path;
    const parts = e.path.split('/');
    let acc = '';
    state.treeExpanded.add(treeKey(state.root, ''));
    for (const p of parts) {
      acc = acc ? acc + '/' + p : p;
      state.treeExpanded.add(treeKey(state.root, acc));
    }
    invalidateTreeCache(state.root);
    await loadRoots();
    await loadList();
  } else if (isPreviewable(e)) {
    openPreview(e);
  } else if (
    isOfficeFile(e) &&
    state.onlyofficeReady &&
    state.root !== 'trash' &&
    entryCanWrite(e, state.perm === 'write')
  ) {
    openOfficeEditor(e);
  } else {
    window.open(`/api/download?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(e.path)}`, '_blank');
  }
}

function appendEntryActions(container, e, canWrite, compact) {
  const itemRoot = e.searchRoot || state.root;
  if (state.root === 'trash') {
    const rst = document.createElement('button');
    rst.type = 'button';
    rst.textContent = '↩';
    rst.className = 'ghost';
    rst.title = 'Restore';
    rst.setAttribute('aria-label', 'Restore');
    rst.onclick = async (ev) => {
      ev.stopPropagation();
      await api('/api/trash/restore', { method: 'POST', body: JSON.stringify({ id: e.path }) });
      await loadRoots();
      await loadList();
    };
    container.appendChild(rst);
  } else {
    const rowWrite = entryCanWrite(e, canWrite);
    if (isOfficeFile(e) && state.onlyofficeReady && itemRoot !== 'trash' && rowWrite) {
      const ed = document.createElement('button');
      ed.type = 'button';
      ed.textContent = '📝';
      ed.className = 'ghost';
      ed.title = 'Edit in ONLYOFFICE (others with Read/Write can edit at the same time)';
      ed.setAttribute('aria-label', 'Edit in ONLYOFFICE');
      ed.onclick = (ev) => {
        ev.stopPropagation();
        openOfficeEditor(e);
      };
      container.appendChild(ed);
    }
    const dl = document.createElement('button');
    dl.type = 'button';
    dl.textContent = '↓';
    dl.className = 'ghost';
    dl.title = e.isDir ? 'Download folder as zip' : 'Download';
    dl.setAttribute('aria-label', dl.title);
    dl.onclick = (ev) => {
      ev.stopPropagation();
      const ep = e.isDir ? '/api/zip' : '/api/download';
      window.location = `${ep}?root=${encodeURIComponent(itemRoot)}&path=${encodeURIComponent(e.path)}`;
    };
    container.appendChild(dl);
  }
  const extras = [];
  if (!(state.root === 'trash' || state.root === 'incoming' || e.searchRoot)) {
    const share = document.createElement('button');
    share.type = 'button';
    share.textContent = '🔗';
    share.className = 'ghost';
    share.title = 'Share';
    share.setAttribute('aria-label', 'Share');
    share.onclick = (ev) => {
      ev.stopPropagation();
      openShareDialog({ path: e.path, isDir: e.isDir, name: e.name });
    };
    extras.push(share);
  }
  if (e.isDir && entryCanWrite(e, canWrite) && state.root !== 'trash' && !e.searchRoot) {
    const alb = document.createElement('button');
    alb.type = 'button';
    alb.className = 'ghost';
    alb.textContent = e.album ? 'Remove album' : 'Set as album';
    alb.title = alb.textContent;
    alb.setAttribute('aria-label', alb.textContent);
    alb.onclick = async (ev) => {
      ev.stopPropagation();
      try {
        await api('/api/albums', { method: 'POST', body: JSON.stringify({ root: itemRoot, path: e.path, album: !e.album }) });
        if (!e.album) {
          state.albumNav = true;
          state.albumOffKey = null;
        }
        invalidateTreeCache(state.root);
        await loadRoots();
        await loadList();
      } catch (err) {
        alert(err.message || String(err));
      }
    };
    extras.push(alb);
  }
  const allowRename = entryCanWrite(e, canWrite) && state.root !== 'trash' && !e.searchRoot && !isIncomingShareRoot(e);
  if (allowRename) {
    const ren = document.createElement('button');
    ren.type = 'button';
    ren.textContent = '✎';
    ren.className = 'ghost';
    ren.title = 'Rename';
    ren.setAttribute('aria-label', 'Rename');
    ren.onclick = async (ev) => {
      ev.stopPropagation();
      const next = prompt('New path relative to root', e.path);
      if (!next || next === e.path) return;
      await api('/api/rename', { method: 'POST', body: JSON.stringify({ root: state.root, from: e.path, to: next }) });
      invalidateTreeCache(state.root);
      await loadRoots();
      await loadList();
    };
    extras.push(ren);
  }
  if (state.canDelete) {
    const del = document.createElement('button');
    del.type = 'button';
    del.textContent = '🗑';
    del.className = 'ghost danger';
    del.title = 'Delete';
    del.setAttribute('aria-label', 'Delete');
    del.onclick = async (ev) => {
      ev.stopPropagation();
      if (!confirm('Delete ' + e.name + '?')) return;
      await api('/api/delete', { method: 'POST', body: JSON.stringify({ root: state.root, path: e.path }) });
      invalidateTreeCache(state.root);
      await loadRoots();
      await loadList();
      try { await refreshMe(); } catch { /* ignore */ }
    };
    extras.push(del);
  }
  const overflow = useCompactRowActions() && extras.length > 0;
  if (overflow) {
    const wrap = document.createElement('div');
    wrap.className = 'row-overflow';
    const more = document.createElement('button');
    more.type = 'button';
    more.textContent = '⋯';
    more.className = 'ghost';
    more.title = 'More actions';
    more.setAttribute('aria-label', 'More actions');
    more.setAttribute('aria-haspopup', 'menu');
    const menu = document.createElement('div');
    menu.className = 'row-overflow-menu';
    menu.hidden = true;
    menu.setAttribute('role', 'menu');
    extras.forEach((btn) => menu.appendChild(btn));
    more.onclick = (ev) => {
      ev.stopPropagation();
      const open = menu.hidden;
      document.querySelectorAll('.row-overflow-menu').forEach((m) => { m.hidden = true; });
      menu.hidden = !open;
    };
    wrap.appendChild(more);
    wrap.appendChild(menu);
    container.appendChild(wrap);
  } else {
    extras.forEach((btn) => container.appendChild(btn));
  }
}

function markFolderDropTarget(el, folderPath) {
  el.dataset.dropPath = folderPath;
  el.classList.add('is-folder-drop');
}

function toggleSelect(path, on) {
  if (on) state.selected.add(path);
  else state.selected.delete(path);
  updateBulkBar();
}

function updateBulkBar() {
  const bar = $('#bulk-bar');
  const n = state.selected.size;
  if (!bar) return;
  bar.hidden = n === 0;
  const count = $('#bulk-count');
  if (count) count.textContent = n + ' selected';
  const inTrash = state.root === 'trash';
  const inIncoming = state.root === 'incoming';
  const shareBtn = $('#btn-bulk-share');
  if (shareBtn) {
    shareBtn.hidden = inTrash || inIncoming;
    shareBtn.disabled = inTrash || inIncoming || n !== 1;
  }
  const canDelete = !!state.canDelete;

  ['btn-bulk-delete', 'btn-bulk-move'].forEach((id) => {
    const el = $('#' + id);
    if (!el) return;
    if (id === 'btn-bulk-move') {
      el.hidden = inTrash || !canDelete;
      el.disabled = inTrash || !canDelete;
    } else {
      el.hidden = !canDelete;
      el.disabled = !canDelete;
    }
  });
  const copyBtn = $('#btn-bulk-copy');
  // Copy only needs write on the destination, which the picker lists.
  // Recycle bin is restore/purge only — no copy or share link.
  if (copyBtn) {
    copyBtn.hidden = inTrash;
    copyBtn.disabled = inTrash;
  }
  const sa = $('#select-all');
  if (sa && state.entries) {
    const total = state.entries.length;
    sa.checked = total > 0 && n === total;
    sa.indeterminate = n > 0 && n < total;
  }
}

function makeCheckbox(e) {
  const cb = document.createElement('input');
  cb.type = 'checkbox';
  cb.className = 'row-check';
  cb.checked = state.selected.has(e.path);
  cb.title = 'Select';
  cb.setAttribute('aria-label', 'Select ' + e.name);
  cb.onclick = (ev) => ev.stopPropagation();
  cb.onchange = () => toggleSelect(e.path, cb.checked);
  return cb;
}

function thumbUrl(path, e) {
  let url = `/api/thumb?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(path)}`;
  if (e && e.modTime) url += '&t=' + encodeURIComponent(e.modTime);
  return url;
}

function appendMediaThumb(thumb, e, path) {
  const img = document.createElement('img');
  img.loading = 'lazy';
  img.alt = e.name || '';
  img.src = thumbUrl(path, e);
  img.onerror = () => {
    img.remove();
    if (!thumb.querySelector('img')) thumb.textContent = entryIcon(e);
  };
  thumb.appendChild(img);
  if (e.kind === 'video') {
    const badge = document.createElement('span');
    badge.className = 'play-badge';
    badge.textContent = '▶';
    thumb.appendChild(badge);
  }
}

function updateFolderAlbumButton() {
  const btn = $('#btn-folder-album');
  if (!btn) return;
  const atIncomingRoot = state.root === 'incoming' && !(state.path || '');
  const can = state.perm === 'write' && state.root !== 'trash' && !atIncomingRoot && !state.searchQuery;
  btn.hidden = !can;
  const setLabel = btn.dataset.setLabel || 'Set as album';
  const removeLabel = btn.dataset.removeLabel || 'Remove album';
  btn.textContent = state.isAlbum ? removeLabel : setLabel;
  btn.title = btn.textContent;
}

async function toggleCurrentFolderAlbum() {
  const on = !state.isAlbum;
  try {
    await api('/api/albums', { method: 'POST', body: JSON.stringify({ root: state.root, path: state.path || '', album: on }) });
    if (on) {
      state.albumNav = true;
      state.albumOffKey = null;
    }
    invalidateTreeCache(state.root);
    await loadRoots();
    await loadList();
  } catch (err) {
    alert(err.message || String(err));
  }
}

$('#btn-folder-album')?.addEventListener('click', () => { toggleCurrentFolderAlbum(); });

function renderAlbumView(entries, canWrite) {
  const box = $('#album-view');
  if (!box) return;
  box.innerHTML = '';
  const folders = entries.filter((e) => e.isDir);
  const media = entries.filter((e) => !e.isDir && (e.kind === 'image' || e.kind === 'video'));
  const hiddenCount = entries.length - folders.length - media.length;

  if (!folders.length && !media.length) {
    const empty = document.createElement('div');
    empty.className = 'album-empty muted';
    empty.textContent = hiddenCount > 0
      ? 'No photos in this album — switch to List to see other files'
      : (canWrite ? 'Empty album — add a folder or upload photos' : 'Empty album');
    box.appendChild(empty);
    updateBulkBar();
    return;
  }

  if (folders.length) {
    const h = document.createElement('h3');
    h.className = 'album-section-title';
    h.textContent = 'Folders';
    box.appendChild(h);
    const grid = document.createElement('div');
    grid.className = 'album-folders';
    for (const e of folders) {
      const tile = document.createElement('div');
      tile.className = 'album-tile';
      tile.title = e.name;
      if (e.isDir) markFolderDropTarget(tile, e.path);
      const thumb = document.createElement('div');
      thumb.className = 'thumb';
      if (e.coverPath) {
        appendMediaThumb(thumb, { name: e.name, kind: 'image' }, e.coverPath);
      } else {
        thumb.textContent = '📁';
      }
      const name = document.createElement('div');
      name.className = 'tile-name';
      name.textContent = e.name;
      if (e.album) {
        const badge = document.createElement('span');
        badge.className = 'album-badge';
        badge.textContent = 'Album';
        name.appendChild(badge);
      }
      const actions = document.createElement('div');
      actions.className = 'tile-actions';
      actions.onclick = (ev) => ev.stopPropagation();
      appendEntryActions(actions, e, canWrite, true);
      tile.appendChild(thumb);
      tile.appendChild(name);
      tile.appendChild(actions);
      tile.onclick = () => openEntry(e);
      grid.appendChild(tile);
    }
    box.appendChild(grid);
  }

  if (media.length) {
    const h = document.createElement('h3');
    h.className = 'album-section-title';
    h.textContent = 'Photos';
    box.appendChild(h);
    const grid = document.createElement('div');
    grid.className = 'album-media';
    for (const e of media) {
      const card = document.createElement('div');
      card.className = 'album-media-card' + (state.selected.has(e.path) ? ' selected' : '');
      card.title = e.name;
      const thumb = document.createElement('div');
      thumb.className = 'thumb';
      appendMediaThumb(thumb, e, e.path);
      const name = document.createElement('div');
      name.className = 'tile-name';
      name.textContent = e.name;
      const actions = document.createElement('div');
      actions.className = 'tile-actions';
      actions.onclick = (ev) => ev.stopPropagation();
      appendEntryActions(actions, e, canWrite, true);
      card.appendChild(thumb);
      card.appendChild(name);
      card.appendChild(actions);
      card.onclick = () => openEntry(e);
      grid.appendChild(card);
    }
    box.appendChild(grid);
  }

  if (hiddenCount > 0) {
    const hint = document.createElement('p');
    hint.className = 'muted album-hint';
    hint.textContent = hiddenCount === 1
      ? '1 other file is hidden — switch to List to see it'
      : hiddenCount + ' other files are hidden — switch to List to see them';
    box.appendChild(hint);
  }
  updateBulkBar();
}

function renderEntries(entries, canWrite) {
  const body = $('#file-body');
  const grid = $('#files-grid');
  const albumView = $('#album-view');
  body.innerHTML = '';
  grid.innerHTML = '';
  if (albumView && state.viewMode !== 'album') albumView.innerHTML = '';

  if (state.viewMode === 'album') {
    renderAlbumView(entries, canWrite);
    return;
  }

  if (!entries.length) {
    if (state.viewMode === 'icons') {
      const empty = document.createElement('div');
      empty.className = 'files-empty';
      empty.textContent = canWrite ? 'Empty folder — drop files or folders here to upload' : 'Empty folder';
      grid.appendChild(empty);
    } else {
      const tr = document.createElement('tr');
      tr.innerHTML = `<td colspan="6" class="empty-row muted">${canWrite ? 'Empty folder — drop files or folders here to upload' : 'Empty folder'}</td>`;
      body.appendChild(tr);
    }
    updateBulkBar();
    return;
  }

  if (state.viewMode === 'icons') {
    for (const e of entries) {
      const card = document.createElement('div');
      card.className = 'file-card' + (state.selected.has(e.path) ? ' selected' : '');
      card.title = e.name;
      if (e.isDir) markFolderDropTarget(card, e.path);

      const checkWrap = document.createElement('label');
      checkWrap.className = 'card-check';
      checkWrap.onclick = (ev) => ev.stopPropagation();
      checkWrap.appendChild(makeCheckbox(e));

      const thumb = document.createElement('div');
      thumb.className = 'thumb';
      if (!e.isDir && (e.kind === 'image' || e.kind === 'video')) {
        const img = document.createElement('img');
        img.loading = 'lazy';
        img.alt = e.name;
        img.src = thumbUrl(e.path, e);
        img.onerror = () => {
          img.remove();
          thumb.textContent = entryIcon(e);
        };
        thumb.appendChild(img);
        if (e.kind === 'video') {
          const badge = document.createElement('span');
          badge.className = 'play-badge';
          badge.textContent = '▶';
          thumb.appendChild(badge);
        }
      } else {
        thumb.textContent = entryIcon(e);
      }

      const name = document.createElement('div');
      name.className = 'card-name';
      name.textContent = e.sharedBy ? (e.name + '  ·  ' + e.sharedBy) : e.name;
      if (e.isDir && e.album) {
        const badge = document.createElement('span');
        badge.className = 'album-badge';
        badge.textContent = 'Album';
        name.appendChild(badge);
      }
      if (state.root === 'incoming') {
        const badge = document.createElement('div');
        badge.className = 'perm-badge muted';
        badge.textContent = entryPermLabel(e);
        name.appendChild(document.createElement('br'));
        name.appendChild(badge);
      }

      const actions = document.createElement('div');
      actions.className = 'card-actions';
      actions.onclick = (ev) => ev.stopPropagation();
      appendEntryActions(actions, e, canWrite, true);

      card.appendChild(checkWrap);
      card.appendChild(thumb);
      card.appendChild(name);
      card.appendChild(actions);
      card.onclick = () => openEntry(e);
      grid.appendChild(card);
    }
    updateBulkBar();
    return;
  }

  for (const e of entries) {
    const tr = document.createElement('tr');
    if (e.isDir) markFolderDropTarget(tr, e.path);
    if (state.selected.has(e.path)) tr.classList.add('selected');
    tr.innerHTML = `<td class="check-cell"></td><td class="icon">${entryIcon(e)}</td><td class="name-cell"><div class="name-wrap"></div></td><td class="size-cell">${e.isDir ? '—' : fmtSize(e.size)}</td><td class="mod-cell">${new Date(e.modTime).toLocaleString()}</td><td class="row-actions"></td>`;
    tr.querySelector('.check-cell').appendChild(makeCheckbox(e));
    const nameWrap = tr.querySelector('.name-wrap');
    const nameBtn = document.createElement('button');
    nameBtn.className = 'name-btn';
    nameBtn.textContent = e.sharedBy ? (e.name + '  ·  ' + e.sharedBy) : e.name;
    nameBtn.title = e.sharedBy ? (e.name + ' (from ' + e.sharedBy + ')') : e.name;
    nameBtn.onclick = () => openEntry(e);
    nameWrap.appendChild(nameBtn);
    if (e.isDir && e.album) {
      const badge = document.createElement('span');
      badge.className = 'album-badge';
      badge.textContent = 'Album';
      nameWrap.appendChild(badge);
    }
    if (state.root === 'incoming') {
      const badge = document.createElement('span');
      badge.className = 'perm-badge muted';
      badge.textContent = entryPermLabel(e);
      nameWrap.appendChild(badge);
    }
    appendEntryActions(tr.querySelector('.row-actions'), e, canWrite, false);
    body.appendChild(tr);
  }
  updateBulkBar();
}

function stopPreviewMedia() {
  const body = $('#preview-body');
  if (!body) return;
  body.querySelectorAll('video, audio').forEach((el) => {
    try {
      el.pause();
      el.removeAttribute('src');
      el.src = '';
      el.load();
    } catch { /* ignore */ }
    el.remove();
  });
  revokePdfObjectUrls(body);
  body.classList.remove('pdf-mode');
  body.innerHTML = '';
}

function openPreview(e, opts) {
  stopPreviewMedia();
  const body = $('#preview-body');
  const toolbar = $('#preview-toolbar');
  const dlg = $('#preview');
  state.previewEntry = e;
  const bustBits = [];
  if (opts && opts.cacheBust) bustBits.push(String(opts.cacheBust));
  if (e && e.modTime) bustBits.push(String(e.modTime));
  if (e && e.size != null) bustBits.push(String(e.size));
  const bust = bustBits.length ? '&t=' + encodeURIComponent(bustBits.join('-')) : '';
  const url = `/api/raw?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(e.path)}` + bust;
  if (e.kind === 'image') {
    const img = document.createElement('img');
    img.src = url;
    img.alt = e.name;
    body.appendChild(img);
    if (toolbar) toolbar.hidden = false;
    dlg.classList.add('preview-has-edit');
  } else if (e.kind === 'pdf') {
    if (toolbar) toolbar.hidden = true;
    dlg.classList.remove('preview-has-edit');
    body.classList.add('pdf-mode');
    appendPdfFrame(body, url, e.name);
  } else {
    if (toolbar) toolbar.hidden = true;
    dlg.classList.remove('preview-has-edit');
    const v = document.createElement('video');
    v.controls = true;
    v.autoplay = true;
    v.src = url;
    body.appendChild(v);
  }
  if (!dlg.dataset.closeBound) {
    dlg.dataset.closeBound = '1';
    const onClose = () => {
      stopPreviewMedia();
      if (toolbar) toolbar.hidden = true;
      dlg.classList.remove('preview-has-edit');
      state.previewEntry = null;
    };
    dlg.addEventListener('close', onClose);
    dlg.addEventListener('cancel', onClose);
  }
  syncPhotoEditorViewport();
  if (!dlg.open) dlg.showModal();
}

const PHOTO_EDIT_MAX_BYTES = 80 * 1024 * 1024;
const PHOTO_EDIT_MAX_PIXELS = 40 * 1000 * 1000;

const photoEditor = {
  entry: null,
  original: null,
  working: null,
  brightness: 100,
  contrast: 100,
  cropMode: false,
  cropNorm: null,
  cropDrag: null,
  spec: null,
  canWrite: false,
  canOverwrite: false,
  saving: false,
  _reopen: null,
  _savedBust: null,
};

function asciiFrom(bytes, start, len) {
  let s = '';
  const end = Math.min(bytes.length, start + len);
  for (let i = start; i < end; i++) s += String.fromCharCode(bytes[i]);
  return s;
}

function isGifHeader(bytes) {
  const sig = asciiFrom(bytes, 0, 6);
  return sig === 'GIF87a' || sig === 'GIF89a';
}

function isAnimatedGif(bytes) {
  if (!isGifHeader(bytes) || bytes.length < 14) return false;
  let i = 13;
  const packed = bytes[10];
  if (packed & 0x80) i += 3 * (1 << ((packed & 7) + 1));
  let frames = 0;
  while (i < bytes.length) {
    const b = bytes[i];
    if (b === 0x3B) break;
    if (b === 0x21) {
      i += 2;
      while (i < bytes.length && bytes[i] !== 0) i += 1 + bytes[i];
      i += 1;
      continue;
    }
    if (b === 0x2C) {
      frames += 1;
      if (frames > 1) return true;
      if (i + 10 >= bytes.length) break;
      const ipacked = bytes[i + 9];
      i += 10;
      if (ipacked & 0x80) i += 3 * (1 << ((ipacked & 7) + 1));
      i += 1;
      while (i < bytes.length && bytes[i] !== 0) i += 1 + bytes[i];
      i += 1;
      continue;
    }
    break;
  }
  return frames > 1;
}

function isHeicLike(bytes, name, type) {
  const ext = fileExt(name);
  if (ext === 'heic' || ext === 'heif') return true;
  const t = String(type || '').toLowerCase();
  if (t.includes('heic') || t.includes('heif')) return true;
  if (bytes.length >= 12 && asciiFrom(bytes, 4, 4) === 'ftyp') {
    const brand = asciiFrom(bytes, 8, 4).toLowerCase();
    return ['heic', 'heix', 'hevc', 'hevx', 'heim', 'heis', 'hevm', 'hevs', 'mif1', 'msf1', 'heif'].includes(brand);
  }
  return false;
}

function looksLikeSvg(bytes, name, type) {
  const ext = fileExt(name);
  if (ext === 'svg') return true;
  if (String(type || '').toLowerCase().includes('svg')) return true;
  const head = asciiFrom(bytes, 0, Math.min(bytes.length, 256)).trim().toLowerCase();
  return head.startsWith('<?xml') && head.includes('<svg') || head.startsWith('<svg');
}

function photoEditBlockReason(name, bytes, type) {
  if (looksLikeSvg(bytes, name, type)) {
    return 'SVG images cannot be edited (the file would be rasterized).';
  }
  if (isHeicLike(bytes, name, type)) {
    return 'HEIC/HEIF photos cannot be edited in the browser.';
  }
  if (isAnimatedGif(bytes)) {
    return 'Animated GIFs cannot be edited without flattening the animation.';
  }
  return '';
}

function photoEditorExportSpec(name) {
  const ext = fileExt(name);
  if (ext === 'jpg' || ext === 'jpeg') return { mime: 'image/jpeg', quality: 0.92, ext: '.' + ext, converted: false };
  if (ext === 'png') return { mime: 'image/png', quality: undefined, ext: '.png', converted: false };
  if (ext === 'webp') return { mime: 'image/webp', quality: 0.92, ext: '.webp', converted: false };
  return { mime: 'image/png', quality: undefined, ext: '.png', converted: true };
}

function editedPhotoName(name, exportExt) {
  const base = String(name || 'photo');
  const dot = base.lastIndexOf('.');
  const stem = dot > 0 ? base.slice(0, dot) : base;
  return stem + '-edited' + exportExt;
}

function cloneCanvas(src) {
  const c = document.createElement('canvas');
  c.width = src.width;
  c.height = src.height;
  c.getContext('2d').drawImage(src, 0, 0);
  return c;
}

function imageToCanvas(img) {
  const c = document.createElement('canvas');
  c.width = img.naturalWidth || img.width;
  c.height = img.naturalHeight || img.height;
  c.getContext('2d').drawImage(img, 0, 0);
  return c;
}

function decodeImageBlob(blob) {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(blob);
    const img = new Image();
    img.onload = () => {
      URL.revokeObjectURL(url);
      resolve(img);
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error('This image format cannot be edited in the browser.'));
    };
    img.src = url;
  });
}

function transformCanvas(src, fn) {
  const dest = document.createElement('canvas');
  fn(src, dest);
  return dest;
}

function setPhotoEditorMessage(kind, text) {
  const err = $('#photo-editor-error');
  const status = $('#photo-editor-status');
  if (err) {
    err.hidden = kind !== 'error';
    err.textContent = kind === 'error' ? (text || '') : '';
  }
  if (status) {
    status.hidden = kind !== 'status';
    status.textContent = kind === 'status' ? (text || '') : '';
  }
}

function syncPhotoEditorSaveButtons() {
  const copyBtn = $('#photo-editor-save-copy');
  const overBtn = $('#photo-editor-overwrite');
  const ready = !photoEditor.saving && !!photoEditor.working;
  if (copyBtn) copyBtn.disabled = !ready || !photoEditor.canWrite;
  if (overBtn) {
    overBtn.disabled = !ready || !photoEditor.canOverwrite;
    overBtn.hidden = !photoEditor.canWrite && !photoEditor.canOverwrite;
  }
}

function photoEditorToolsDisabled(disabled) {
  ['photo-editor-rotate', 'photo-editor-flip-h', 'photo-editor-flip-v', 'photo-editor-crop-toggle',
    'photo-editor-crop-apply', 'photo-editor-crop-cancel', 'photo-editor-reset',
    'photo-editor-brightness', 'photo-editor-contrast'].forEach((id) => {
    const el = document.getElementById(id);
    if (el) el.disabled = disabled;
  });
}

function canvasDisplayRect() {
  const canvas = $('#photo-editor-canvas');
  const stage = $('#photo-editor-stage');
  if (!canvas || !stage) return null;
  const cr = canvas.getBoundingClientRect();
  const sr = stage.getBoundingClientRect();
  if (cr.width < 1 || cr.height < 1) return null;
  return { left: cr.left - sr.left, top: cr.top - sr.top, width: cr.width, height: cr.height };
}

function setCropMode(on) {
  photoEditor.cropMode = !!on;
  const box = $('#photo-editor-crop');
  const toggle = $('#photo-editor-crop-toggle');
  const apply = $('#photo-editor-crop-apply');
  const cancel = $('#photo-editor-crop-cancel');
  if (toggle) toggle.hidden = !!on;
  if (apply) apply.hidden = !on;
  if (cancel) cancel.hidden = !on;
  if (!on) {
    photoEditor.cropNorm = null;
    photoEditor.cropDrag = null;
    if (box) box.hidden = true;
    return;
  }
  photoEditor.cropNorm = { x: 0.1, y: 0.1, w: 0.8, h: 0.8 };
  if (box && !box.dataset.ready) {
    box.dataset.ready = '1';
    ['nw', 'n', 'ne', 'w', 'e', 'sw', 's', 'se'].forEach((pos) => {
      const h = document.createElement('div');
      h.className = 'crop-handle ' + pos;
      h.dataset.handle = pos;
      box.appendChild(h);
    });
    box.addEventListener('pointerdown', onCropPointerDown);
  }
  layoutCropBox();
}

function layoutCropBox() {
  const box = $('#photo-editor-crop');
  const disp = canvasDisplayRect();
  const n = photoEditor.cropNorm;
  if (!box || !disp || !n || !photoEditor.cropMode) return;
  box.style.left = (disp.left + n.x * disp.width) + 'px';
  box.style.top = (disp.top + n.y * disp.height) + 'px';
  box.style.width = (n.w * disp.width) + 'px';
  box.style.height = (n.h * disp.height) + 'px';
  box.hidden = false;
}

function clampCropNorm(n, minPx) {
  const disp = canvasDisplayRect();
  const minW = disp ? Math.min(0.95, Math.max(0.02, (minPx || 10) / disp.width)) : 0.02;
  const minH = disp ? Math.min(0.95, Math.max(0.02, (minPx || 10) / disp.height)) : 0.02;
  n.w = Math.max(minW, Math.min(n.w, 1));
  n.h = Math.max(minH, Math.min(n.h, 1));
  n.x = Math.max(0, Math.min(n.x, 1 - n.w));
  n.y = Math.max(0, Math.min(n.y, 1 - n.h));
  return n;
}

function onCropPointerDown(ev) {
  if (!photoEditor.cropMode || !photoEditor.cropNorm) return;
  ev.preventDefault();
  ev.stopPropagation();
  const handle = ev.target.dataset && ev.target.dataset.handle ? ev.target.dataset.handle : 'move';
  const disp = canvasDisplayRect();
  if (!disp) return;
  photoEditor.cropDrag = {
    handle,
    startX: ev.clientX,
    startY: ev.clientY,
    orig: { ...photoEditor.cropNorm },
    pointerId: ev.pointerId,
  };
  ev.currentTarget.setPointerCapture(ev.pointerId);
  ev.currentTarget.addEventListener('pointermove', onCropPointerMove);
  ev.currentTarget.addEventListener('pointerup', onCropPointerUp);
  ev.currentTarget.addEventListener('pointercancel', onCropPointerUp);
}

function onCropPointerMove(ev) {
  const drag = photoEditor.cropDrag;
  const disp = canvasDisplayRect();
  if (!drag || !disp) return;
  const dx = (ev.clientX - drag.startX) / disp.width;
  const dy = (ev.clientY - drag.startY) / disp.height;
  const o = drag.orig;
  const n = { x: o.x, y: o.y, w: o.w, h: o.h };
  const h = drag.handle;
  if (h === 'move') {
    n.x = o.x + dx;
    n.y = o.y + dy;
  } else {
    let x2 = o.x + o.w;
    let y2 = o.y + o.h;
    if (h.includes('w')) n.x = o.x + dx;
    if (h.includes('e')) x2 = o.x + o.w + dx;
    if (h.includes('n')) n.y = o.y + dy;
    if (h.includes('s')) y2 = o.y + o.h + dy;
    if (h.includes('w') || h.includes('e')) {
      n.w = x2 - n.x;
      if (n.w < 0) { n.x = x2; n.w = -n.w; }
    }
    if (h.includes('n') || h.includes('s')) {
      n.h = y2 - n.y;
      if (n.h < 0) { n.y = y2; n.h = -n.h; }
    }
  }
  photoEditor.cropNorm = clampCropNorm(n);
  layoutCropBox();
}

function onCropPointerUp(ev) {
  const box = ev.currentTarget;
  const drag = photoEditor.cropDrag;
  if (drag && box.hasPointerCapture && box.hasPointerCapture(drag.pointerId)) {
    try { box.releasePointerCapture(drag.pointerId); } catch { /* ignore */ }
  }
  box.removeEventListener('pointermove', onCropPointerMove);
  box.removeEventListener('pointerup', onCropPointerUp);
  box.removeEventListener('pointercancel', onCropPointerUp);
  photoEditor.cropDrag = null;
}

function applyCrop() {
  const src = photoEditor.working;
  const n = photoEditor.cropNorm;
  if (!src || !n) return;
  const x = Math.round(n.x * src.width);
  const y = Math.round(n.y * src.height);
  const w = Math.max(1, Math.round(n.w * src.width));
  const h = Math.max(1, Math.round(n.h * src.height));
  const sx = Math.max(0, Math.min(x, src.width - 1));
  const sy = Math.max(0, Math.min(y, src.height - 1));
  const sw = Math.max(1, Math.min(w, src.width - sx));
  const sh = Math.max(1, Math.min(h, src.height - sy));
  photoEditor.working = transformCanvas(src, (from, dest) => {
    dest.width = sw;
    dest.height = sh;
    dest.getContext('2d').drawImage(from, sx, sy, sw, sh, 0, 0, sw, sh);
  });
  setCropMode(false);
  renderPhotoEditor();
}

function renderPhotoEditor() {
  const canvas = $('#photo-editor-canvas');
  const src = photoEditor.working;
  if (!canvas || !src) return;
  const stage = $('#photo-editor-stage');
  const maxW = Math.max(100, (stage && stage.clientWidth) || 800);
  const maxH = Math.max(100, (stage && stage.clientHeight) || 500);
  const scale = Math.min(maxW / src.width, maxH / src.height, 1);
  const w = Math.max(1, Math.round(src.width * scale));
  const h = Math.max(1, Math.round(src.height * scale));
  if (canvas.width !== w) canvas.width = w;
  if (canvas.height !== h) canvas.height = h;
  const ctx = canvas.getContext('2d');
  ctx.clearRect(0, 0, w, h);
  ctx.drawImage(src, 0, 0, w, h);
  const b = photoEditor.brightness;
  const c = photoEditor.contrast;
  if (b !== 100 || c !== 100) {
    if (!window.FilehubPhotoAdjust || !FilehubPhotoAdjust.applyBrightnessContrastToCanvas) {
      throw new Error('Photo adjustment helper is missing');
    }
    FilehubPhotoAdjust.applyBrightnessContrastToCanvas(canvas, b, c);
  }
  if (photoEditor.cropMode) layoutCropBox();
}

function resetPhotoEditorAdjustments(full) {
  photoEditor.brightness = 100;
  photoEditor.contrast = 100;
  const br = $('#photo-editor-brightness');
  const ct = $('#photo-editor-contrast');
  const brv = $('#photo-editor-brightness-val');
  const ctv = $('#photo-editor-contrast-val');
  if (br) br.value = '100';
  if (ct) ct.value = '100';
  if (brv) brv.textContent = '100%';
  if (ctv) ctv.textContent = '100%';
  if (full && photoEditor.original) {
    photoEditor.working = cloneCanvas(photoEditor.original);
  }
  setCropMode(false);
  renderPhotoEditor();
}

function closePhotoEditor(opts) {
  const dlg = $('#photo-editor');
  photoEditor.saving = false;
  photoEditor._reopen = !!(opts && opts.reopen);
  photoEditor._savedBust = (opts && opts.cacheBust) ? opts.cacheBust : null;
  if (dlg && dlg.open) {
    dlg.close();
    return;
  }
  finishPhotoEditorClose();
}

function finishPhotoEditorClose() {
  const reopen = photoEditor._reopen !== false && !!state.previewEntry;
  const entry = state.previewEntry;
  const bust = photoEditor._savedBust;
  photoEditor.entry = null;
  photoEditor.original = null;
  photoEditor.working = null;
  photoEditor.spec = null;
  photoEditor.saving = false;
  photoEditor._reopen = true;
  photoEditor._savedBust = null;
  setCropMode(false);
  setPhotoEditorMessage('', '');
  if (reopen && entry && !($('#preview') && $('#preview').open)) {
    openPreview(entry, bust ? { cacheBust: bust } : undefined);
  }
}

async function exportEditedPhotoBlob() {
  const src = photoEditor.working;
  const spec = photoEditor.spec;
  if (!src || !spec) throw new Error('Nothing to save');
  const out = document.createElement('canvas');
  out.width = src.width;
  out.height = src.height;
  const ctx = out.getContext('2d', { willReadFrequently: true });
  ctx.drawImage(src, 0, 0);
  const b = photoEditor.brightness;
  const c = photoEditor.contrast;
  if (b !== 100 || c !== 100) {
    if (!window.FilehubPhotoAdjust || !FilehubPhotoAdjust.applyBrightnessContrastToCanvas) {
      throw new Error('Photo adjustment helper is missing');
    }
    FilehubPhotoAdjust.applyBrightnessContrastToCanvas(out, b, c);
  }
  const blob = await new Promise((resolve, reject) => {
    const done = (v) => { if (v) resolve(v); else reject(new Error('Could not encode the edited photo')); };
    if (spec.quality == null) out.toBlob(done, spec.mime);
    else out.toBlob(done, spec.mime, spec.quality);
  });
  return blob;
}

async function saveEditedPhoto(mode) {
  if (photoEditor.saving) return;
  const entry = photoEditor.entry;
  if (!entry) return;
  if (!photoEditor.canWrite) {
    setPhotoEditorMessage('error', 'You need write permission to save.');
    return;
  }
  if (mode === 'overwrite' && !photoEditor.canOverwrite) {
    setPhotoEditorMessage('error', photoEditor.spec && photoEditor.spec.converted
      ? 'Overwrite is only available when the photo can be saved in its original format. Use Save as copy to write a PNG.'
      : 'Overwrite requires delete permission (the original is moved to the Recycle bin).');
    return;
  }
  if (mode === 'overwrite') {
    const ok = confirm('Overwrite the original? The current file will be moved to the Recycle bin.');
    if (!ok) return;
  }
  photoEditor.saving = true;
  photoEditorToolsDisabled(true);
  syncPhotoEditorSaveButtons();
  setPhotoEditorMessage('status', 'Saving…');
  try {
    const blob = await exportEditedPhotoBlob();
    const folder = dirnamePath(entry.path);
    const filename = mode === 'overwrite' ? entry.name : editedPhotoName(entry.name, photoEditor.spec.ext);
    const conflict = mode === 'overwrite' ? 'overwrite' : 'rename';
    const file = new File([blob], filename, { type: blob.type || photoEditor.spec.mime });
    const data = await uploadOneFile(file, folder, null, conflict);
    const savedName = (data && data.name) || filename;
    const savedPath = (data && data.path) || (folder ? folder + '/' + savedName : savedName);
    try { await loadList(); } catch { /* ignore */ }
    const next = (state.entries || []).find((item) => item.path === savedPath) || {
      ...entry,
      name: savedName,
      path: savedPath,
      size: blob.size,
      kind: 'image',
      modTime: new Date().toISOString(),
    };
    state.previewEntry = next;
    const bust = [Date.now(), next.modTime || '', next.size || blob.size].join('-');
    closePhotoEditor({ reopen: true, cacheBust: bust });
  } catch (err) {
    setPhotoEditorMessage('error', err.message || String(err));
    photoEditor.saving = false;
    photoEditorToolsDisabled(false);
    syncPhotoEditorSaveButtons();
  }
}

async function openPhotoEditor(e) {
  const dlg = $('#photo-editor');
  if (!dlg || !e || e.kind !== 'image') return;
  if (state.root === 'trash') {
    alert('Photos in the Recycle bin cannot be edited.');
    return;
  }
  setPhotoEditorMessage('', '');
  photoEditor.saving = false;
  photoEditor._reopen = null;
  photoEditor._savedBust = null;
  photoEditor.entry = e;
  photoEditor.spec = photoEditorExportSpec(e.name);
  photoEditor.canWrite = entryCanWrite(e, state.perm === 'write') && state.root !== 'trash';
  photoEditor.canOverwrite = photoEditor.canWrite && !!state.canDelete && !photoEditor.spec.converted;
  const nameEl = $('#photo-editor-filename');
  if (nameEl) nameEl.textContent = e.name;
  const preview = $('#preview');
  if (preview && preview.open) preview.close();
  state.previewEntry = e;
  resetPhotoEditorAdjustments(false);
  photoEditorToolsDisabled(true);
  syncPhotoEditorSaveButtons();
  if (!dlg.open) dlg.showModal();
  setPhotoEditorMessage('status', 'Loading photo…');
  try {
    const url = `/api/raw?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(e.path)}`;
    const res = await fetch(url, { credentials: 'same-origin' });
    if (!res.ok) throw new Error('Could not load this photo');
    const buf = await res.arrayBuffer();
    if (buf.byteLength > PHOTO_EDIT_MAX_BYTES) {
      throw new Error('This photo is too large to edit in the browser (max 80 MiB).');
    }
    const bytes = new Uint8Array(buf);
    const type = res.headers.get('content-type') || '';
    const blocked = photoEditBlockReason(e.name, bytes, type);
    if (blocked) throw new Error(blocked);
    const blob = new Blob([buf], { type: type || 'application/octet-stream' });
    const img = await decodeImageBlob(blob);
    const pixels = (img.naturalWidth || img.width) * (img.naturalHeight || img.height);
    if (pixels > PHOTO_EDIT_MAX_PIXELS) {
      throw new Error('This photo is too large to edit in the browser (max 40 megapixels).');
    }
    photoEditor.original = imageToCanvas(img);
    photoEditor.working = cloneCanvas(photoEditor.original);
    photoEditorToolsDisabled(false);
    resetPhotoEditorAdjustments(false);
    let note = '';
    if (!photoEditor.canWrite) {
      note = 'You need write permission to save.';
    } else if (photoEditor.spec.converted) {
      note = 'This format will be saved as a PNG copy.';
    } else if (!photoEditor.canOverwrite) {
      note = 'Overwrite requires delete permission (the original is moved to the Recycle bin). You can still save a copy.';
    }
    if (note) setPhotoEditorMessage('status', note);
    else setPhotoEditorMessage('', '');
    syncPhotoEditorSaveButtons();
    requestAnimationFrame(renderPhotoEditor);
  } catch (err) {
    photoEditor.original = null;
    photoEditor.working = null;
    photoEditorToolsDisabled(true);
    const br = $('#photo-editor-brightness');
    const ct = $('#photo-editor-contrast');
    if (br) br.disabled = true;
    if (ct) ct.disabled = true;
    syncPhotoEditorSaveButtons();
    setPhotoEditorMessage('error', err.message || String(err));
  }
}

$('#btn-preview-edit')?.addEventListener('click', (ev) => {
  ev.preventDefault();
  ev.stopPropagation();
  const e = state.previewEntry;
  if (e) openPhotoEditor(e);
});

$('#photo-editor-rotate')?.addEventListener('click', () => {
  if (!photoEditor.working || photoEditor.saving) return;
  setCropMode(false);
  photoEditor.working = transformCanvas(photoEditor.working, (src, dest) => {
    dest.width = src.height;
    dest.height = src.width;
    const ctx = dest.getContext('2d');
    ctx.translate(dest.width, 0);
    ctx.rotate(Math.PI / 2);
    ctx.drawImage(src, 0, 0);
  });
  renderPhotoEditor();
});

$('#photo-editor-flip-h')?.addEventListener('click', () => {
  if (!photoEditor.working || photoEditor.saving) return;
  setCropMode(false);
  photoEditor.working = transformCanvas(photoEditor.working, (src, dest) => {
    dest.width = src.width;
    dest.height = src.height;
    const ctx = dest.getContext('2d');
    ctx.translate(dest.width, 0);
    ctx.scale(-1, 1);
    ctx.drawImage(src, 0, 0);
  });
  renderPhotoEditor();
});

$('#photo-editor-flip-v')?.addEventListener('click', () => {
  if (!photoEditor.working || photoEditor.saving) return;
  setCropMode(false);
  photoEditor.working = transformCanvas(photoEditor.working, (src, dest) => {
    dest.width = src.width;
    dest.height = src.height;
    const ctx = dest.getContext('2d');
    ctx.translate(0, dest.height);
    ctx.scale(1, -1);
    ctx.drawImage(src, 0, 0);
  });
  renderPhotoEditor();
});

$('#photo-editor-crop-toggle')?.addEventListener('click', () => {
  if (!photoEditor.working || photoEditor.saving) return;
  setCropMode(true);
});
$('#photo-editor-crop-apply')?.addEventListener('click', () => {
  if (!photoEditor.working || photoEditor.saving) return;
  applyCrop();
});
$('#photo-editor-crop-cancel')?.addEventListener('click', () => setCropMode(false));
$('#photo-editor-reset')?.addEventListener('click', () => {
  if (photoEditor.saving) return;
  resetPhotoEditorAdjustments(true);
});
$('#photo-editor-brightness')?.addEventListener('input', (ev) => {
  photoEditor.brightness = Number(ev.target.value) || 0;
  const el = $('#photo-editor-brightness-val');
  if (el) el.textContent = photoEditor.brightness + '%';
  renderPhotoEditor();
});
$('#photo-editor-contrast')?.addEventListener('input', (ev) => {
  photoEditor.contrast = Number(ev.target.value) || 0;
  const el = $('#photo-editor-contrast-val');
  if (el) el.textContent = photoEditor.contrast + '%';
  renderPhotoEditor();
});
$('#photo-editor-save-copy')?.addEventListener('click', () => saveEditedPhoto('copy'));
$('#photo-editor-overwrite')?.addEventListener('click', () => saveEditedPhoto('overwrite'));
$('#photo-editor-cancel')?.addEventListener('click', () => closePhotoEditor({ reopen: true }));

function syncPhotoEditorViewport() {
  const vv = window.visualViewport;
  const h = (vv && vv.height) ? vv.height : window.innerHeight;
  const top = (vv && typeof vv.offsetTop === 'number') ? vv.offsetTop : 0;
  const root = document.documentElement;
  root.style.setProperty('--photo-editor-vp-height', Math.round(h) + 'px');
  root.style.setProperty('--photo-editor-vp-top', Math.round(top) + 'px');
}

(function setupPhotoEditorDialog() {
  const dlg = $('#photo-editor');
  if (!dlg || dlg.dataset.closeBound) return;
  dlg.dataset.closeBound = '1';
  dlg.addEventListener('close', () => {
    // Default to returning to the photo preview (Escape / close button).
    if (photoEditor._reopen == null) photoEditor._reopen = true;
    finishPhotoEditorClose();
  });
  const onViewport = () => {
    syncPhotoEditorViewport();
    if (dlg.open) renderPhotoEditor();
  };
  window.addEventListener('resize', onViewport);
  if (window.visualViewport) {
    window.visualViewport.addEventListener('resize', onViewport);
    window.visualViewport.addEventListener('scroll', onViewport);
  }
  syncPhotoEditorViewport();
})();


$('#btn-mkdir').addEventListener('click', async () => {
  const name = prompt('Folder name');
  if (!name) return;
  const path = state.path ? state.path + '/' + name : name;
  await api('/api/mkdir', { method: 'POST', body: JSON.stringify({ root: state.root, path }) });
  invalidateTreeCache(state.root);
  state.treeExpanded.add(treeKey(state.root, state.path || ''));
  await loadRoots();
  await loadList();
  try { await refreshMe(); } catch { /* ignore */ }
});

function syncUploadPanelVisibility() {
  const panel = $('#upload-status');
  const list = $('#upload-list');
  if (!panel || !list) return;
  const hasItems = list.children.length > 0;
  panel.hidden = !hasItems;
  const clearBtn = $('#btn-clear-uploads');
  if (clearBtn) clearBtn.hidden = !hasItems;
  if (!hasItems) setUploadProgressLabel(0, 0);
}

function ensureUploadPanel() {
  const panel = $('#upload-status');
  panel.hidden = false;
  return panel;
}

function setUploadProgressLabel(done, total) {
  const el = $('#upload-progress-label');
  if (!el) return;
  if (!total) { el.textContent = ''; return; }
  el.textContent = `(${done}/${total})`;
}

function setUploadItem(li, { loaded, total, status, error, detail }) {
  const pct = total > 0 ? Math.min(100, Math.round((loaded / total) * 100)) : 0;
  const bar = li.querySelector('.bar > span');
  const meta = li.querySelector('.meta');
  bar.style.width = pct + '%';
  li.classList.remove('done', 'err', 'skipped');
  if (status === 'uploading') {
    meta.textContent = total > 0
      ? `${fmtSize(loaded)} / ${fmtSize(total)} (${pct}%)`
      : `${fmtSize(loaded)}…`;
  } else if (status === 'done') {
    li.classList.add('done');
    meta.textContent = detail || (total > 0 ? `Done · ${fmtSize(total)}` : 'Done');
    bar.style.width = '100%';
  } else if (status === 'skipped') {
    li.classList.add('skipped');
    meta.textContent = detail || 'Skipped';
    bar.style.width = '100%';
  } else if (status === 'error') {
    li.classList.add('err');
    meta.textContent = error || 'Upload failed';
  }
}

const CHUNK_THRESHOLD = 1024 * 1024;
const CHUNK_SIZE = 4 * 1024 * 1024;

function joinUploadPath(base, rel) {
  const b = (base || '').replace(/\/+$/, '');
  const r = (rel || '').replace(/^\/+/, '');
  if (!b) return r;
  if (!r) return b;
  return b + '/' + r;
}

function dirnamePath(rel) {
  const i = (rel || '').lastIndexOf('/');
  return i <= 0 ? '' : rel.slice(0, i);
}

function basenamePath(rel) {
  const i = (rel || '').lastIndexOf('/');
  return i < 0 ? (rel || '') : rel.slice(i + 1);
}

function uploadResumeKey(root, dest, rel, file) {
  return `filehub-upload:${root}:${dest || ''}:${rel || file.name}:${file.size}:${file.lastModified}`;
}

function uploadFileWithProgress(file, destPath, onProgress, conflict) {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    fd.append('root', state.root);
    fd.append('path', destPath || '');
    fd.append('file', file);
    if (conflict) fd.append('conflict', conflict);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/upload');
    xhr.withCredentials = true;
    xhr.upload.onprogress = (e) => {
      if (onProgress) onProgress(e.loaded, e.lengthComputable ? e.total : file.size);
    };
    xhr.onload = () => {
      let data = null;
      try { data = xhr.responseText ? JSON.parse(xhr.responseText) : null; } catch { data = { raw: xhr.responseText }; }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data);
      else {
        const err = new Error((data && data.error) || xhr.statusText || 'Upload failed');
        err.status = xhr.status;
        err.code = data && data.code;
        err.data = data;
        reject(err);
      }
    };
    xhr.onerror = () => reject(new Error('Network error'));
    xhr.onabort = () => reject(new Error('Upload aborted'));
    xhr.send(fd);
  });
}

function putUploadChunk(id, blob, offset, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('PUT', '/api/upload/sessions/' + id);
    xhr.withCredentials = true;
    xhr.setRequestHeader('Upload-Offset', String(offset));
    xhr.upload.onprogress = (e) => {
      if (onProgress) onProgress(e.loaded);
    };
    xhr.onload = () => {
      let data = null;
      try { data = xhr.responseText ? JSON.parse(xhr.responseText) : null; } catch { data = { raw: xhr.responseText }; }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data);
      else {
        const err = new Error((data && data.error) || xhr.statusText || 'Chunk upload failed');
        err.status = xhr.status;
        err.data = data;
        reject(err);
      }
    };
    xhr.onerror = () => reject(new Error('Network error'));
    xhr.onabort = () => reject(new Error('Upload aborted'));
    xhr.send(blob);
  });
}

async function uploadChunked(file, destPath, onProgress, conflict, relPath) {
  const key = uploadResumeKey(state.root, destPath, relPath || file.name, file);
  let id = null;
  let offset = 0;
  const stored = (() => {
    try { return localStorage.getItem(key); } catch { return null; }
  })();
  if (stored) {
    try {
      const data = await api('/api/upload/sessions/' + stored);
      if (data && data.id && data.size === file.size) {
        id = data.id;
        offset = data.offset || 0;
      } else {
        try { localStorage.removeItem(key); } catch { /* ignore */ }
      }
    } catch {
      try { localStorage.removeItem(key); } catch { /* ignore */ }
    }
  }
  if (!id) {
    const data = await api('/api/upload/sessions', {
      method: 'POST',
      body: JSON.stringify({
        root: state.root,
        path: destPath || '',
        name: file.name,
        size: file.size,
        conflict: conflict || '',
      }),
    });
    if (data && data.skipped) return data;
    id = data.id;
    offset = data.offset || 0;
    try { localStorage.setItem(key, id); } catch { /* ignore */ }
    if (onProgress) onProgress(offset, file.size);
    if (data) data._started = true;
  }
  while (offset < file.size) {
    const end = Math.min(offset + CHUNK_SIZE, file.size);
    const blob = file.slice(offset, end);
    let attempt = 0;
    const start = offset;
    for (;;) {
      try {
        const data = await putUploadChunk(id, blob, start, (loaded) => {
          if (onProgress) onProgress(start + loaded, file.size);
        });
        offset = data.offset;
        break;
      } catch (err) {
        attempt += 1;
        if (attempt >= 4) throw err;
        await new Promise((r) => setTimeout(r, 400 * attempt));
        try {
          const st = await api('/api/upload/sessions/' + id);
          offset = st.offset || 0;
          if (offset > start) break;
        } catch { /* retry same chunk */ }
      }
    }
  }
  const data = await api('/api/upload/sessions/' + id + '/complete', { method: 'POST', body: '{}' });
  try { localStorage.removeItem(key); } catch { /* ignore */ }
  return data;
}

async function uploadOneFile(file, destPath, onProgress, conflict, relPath) {
  const size = file.size || 0;
  if (size >= CHUNK_THRESHOLD) {
    return uploadChunked(file, destPath, onProgress, conflict, relPath);
  }
  return uploadFileWithProgress(file, destPath, onProgress, conflict);
}

function splitUploadName(filename) {
  const dot = filename.lastIndexOf('.');
  if (dot <= 0) return { base: filename, ext: '' };
  return { base: filename.slice(0, dot), ext: filename.slice(dot) };
}

function nextAvailableUploadName(filename, taken) {
  if (!taken.has(filename)) return filename;
  const { base, ext } = splitUploadName(filename);
  for (let n = 1; n < 10000; n++) {
    const cand = `${base}_${n}${ext}`;
    if (!taken.has(cand)) return cand;
  }
  return `${base}_${Date.now()}${ext}`;
}

async function listDestinationNames(destPath) {
  const taken = new Set();
  const dirs = new Set();
  try {
    const q = new URLSearchParams({ root: state.root, path: destPath || '' });
    const data = await api('/api/list?' + q.toString());
    for (const e of data.entries || []) {
      if (!e || !e.name) continue;
      taken.add(e.name);
      if (e.isDir) dirs.add(e.name);
    }
  } catch {
    // Fall back to 409 handling per file if the listing fails.
  }
  return { taken, dirs };
}

function findUploadConflicts(entries, takenBy) {
  const planned = new Map();
  const conflicts = [];
  for (const e of entries) {
    const dest = e.dest || '';
    if (!planned.has(dest)) planned.set(dest, new Set());
    const taken = takenBy.get(dest) || new Set();
    const used = planned.get(dest);
    if (taken.has(e.name) || used.has(e.name)) conflicts.push(e);
    else used.add(e.name);
  }
  return conflicts;
}

function destTakenSet(takenBy, dest) {
  if (!takenBy.has(dest)) takenBy.set(dest, new Set());
  return takenBy.get(dest);
}

function destDirSet(dirsBy, dest) {
  return dirsBy.get(dest) || new Set();
}

function showUploadConflictDialog(conflicts, takenBy, dirsBy) {
  return new Promise((resolve) => {
    const dlg = $('#upload-conflict-dialog');
    const list = $('#upload-conflict-list');
    const applyBar = $('#upload-conflict-apply');
    const continueBtn = $('#upload-conflict-continue');
    const cancelBtn = $('#upload-conflict-cancel');
    if (!dlg || !list) {
      resolve(null);
      return;
    }
    const decisions = new Map();
    const canOverwriteItem = (e) => state.canDelete && !destDirSet(dirsBy, e.dest).has(e.name);
    function renderHints() {
      const nextBy = new Map();
      for (const [dest, set] of takenBy.entries()) nextBy.set(dest, new Set(set));
      for (const e of conflicts) {
        const li = e._conflictRow;
        if (!li) continue;
        const action = decisions.get(e) || 'rename';
        const hint = li.querySelector('.conflict-row-hint');
        li.querySelectorAll('.conflict-choice[data-action]').forEach((btn) => {
          btn.classList.toggle('active', btn.dataset.action === action);
        });
        const next = destTakenSet(nextBy, e.dest);
        if (action === 'skip') {
          hint.textContent = 'Existing file will be kept';
        } else if (action === 'overwrite') {
          hint.textContent = destDirSet(dirsBy, e.dest).has(e.name)
            ? 'A folder with this name exists — overwrite is not available'
            : (!state.canDelete
              ? 'Overwrite is not available without delete permission'
              : 'Existing file will be moved to the Recycle bin');
          if (canOverwriteItem(e)) next.add(e.name);
        } else {
          const name = nextAvailableUploadName(e.name, next);
          hint.textContent = name === e.name ? 'Will be uploaded as ' + name : 'Will be saved as ' + name;
          next.add(name);
        }
      }
    }
    list.innerHTML = '';
    applyBar.hidden = conflicts.length < 2;
    for (const e of conflicts) {
      decisions.set(e, 'rename');
      const li = document.createElement('li');
      li.className = 'conflict-row';
      const info = document.createElement('div');
      info.className = 'conflict-row-info';
      const name = document.createElement('div');
      name.className = 'conflict-row-name';
      const label = e.relPath && e.relPath !== e.name ? e.relPath : e.name;
      name.textContent = label;
      name.title = label;
      const hint = document.createElement('div');
      hint.className = 'conflict-row-hint';
      info.appendChild(name);
      info.appendChild(hint);
      const actions = document.createElement('div');
      actions.className = 'conflict-actions';
      actions.setAttribute('role', 'group');
      actions.setAttribute('aria-label', 'Action for ' + label);
      for (const [action, caption] of [['skip', 'Skip'], ['overwrite', 'Overwrite'], ['rename', 'Rename']]) {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'conflict-choice';
        btn.dataset.action = action;
        btn.textContent = caption;
        if (action === 'overwrite' && !canOverwriteItem(e)) btn.disabled = true;
        btn.addEventListener('click', () => {
          if (btn.disabled) return;
          decisions.set(e, action);
          renderHints();
        });
        actions.appendChild(btn);
      }
      li.appendChild(info);
      li.appendChild(actions);
      e._conflictRow = li;
      list.appendChild(li);
    }
    renderHints();

    function applyAll(action) {
      for (const e of conflicts) {
        if (action === 'overwrite' && !canOverwriteItem(e)) continue;
        decisions.set(e, action);
      }
      renderHints();
    }
    const applyBtns = [...applyBar.querySelectorAll('[data-apply]')];
    function onApply(ev) {
      const action = ev.currentTarget.dataset.apply;
      if (action) applyAll(action);
    }
    applyBtns.forEach((b) => b.addEventListener('click', onApply));

    let settled = false;
    function finish(result) {
      if (settled) return;
      settled = true;
      applyBtns.forEach((b) => b.removeEventListener('click', onApply));
      continueBtn.removeEventListener('click', onContinue);
      cancelBtn.removeEventListener('click', onCancel);
      dlg.removeEventListener('cancel', onCancel);
      dlg.removeEventListener('close', onClose);
      if (dlg.open) dlg.close();
      for (const e of conflicts) delete e._conflictRow;
      resolve(result);
    }
    function onContinue(ev) {
      ev.preventDefault();
      const out = new Map();
      for (const e of conflicts) out.set(e, decisions.get(e) || 'rename');
      finish(out);
    }
    function onCancel(ev) {
      if (ev && ev.preventDefault) ev.preventDefault();
      finish(null);
    }
    function onClose() {
      finish(null);
    }
    continueBtn.addEventListener('click', onContinue);
    cancelBtn.addEventListener('click', onCancel);
    dlg.addEventListener('cancel', onCancel);
    dlg.addEventListener('close', onClose);
    dlg.showModal();
  });
}

async function resolveUploadConflicts(entries, destPath) {
  const dests = [...new Set(entries.map((e) => e.dest || destPath || ''))];
  const takenBy = new Map();
  const dirsBy = new Map();
  await Promise.all(dests.map(async (d) => {
    const { taken, dirs } = await listDestinationNames(d);
    takenBy.set(d, taken);
    dirsBy.set(d, dirs);
  }));
  const conflicts = findUploadConflicts(entries, takenBy);
  if (!conflicts.length) return new Map();
  const decisions = await showUploadConflictDialog(conflicts, takenBy, dirsBy);
  if (!decisions) return null;
  return decisions;
}

async function ensureUploadDirs(dirs, destPath) {
  const unique = [...new Set(dirs)].filter(Boolean).sort((a, b) => a.length - b.length || a.localeCompare(b));
  for (const d of unique) {
    const path = joinUploadPath(destPath, d);
    try {
      await api('/api/mkdir', { method: 'POST', body: JSON.stringify({ root: state.root, path }) });
    } catch {
      // Existing folders are merged; a later file upload reports a real conflict.
    }
  }
}

function entriesFromFileList(list, fromFolderPicker) {
  const files = [];
  const dirs = new Set();
  for (const f of list) {
    if (!f || !f.name) continue;
    let rel = (fromFolderPicker && f.webkitRelativePath) ? f.webkitRelativePath.replace(/\\/g, '/') : f.name;
    if (!rel) rel = f.name;
    files.push({ file: f, relPath: rel });
    const parts = rel.split('/');
    parts.pop();
    let acc = '';
    for (const part of parts) {
      acc = acc ? acc + '/' + part : part;
      dirs.add(acc);
    }
  }
  return { files, dirs: [...dirs] };
}

async function readDirectoryEntry(dirEntry, prefix) {
  const reader = dirEntry.createReader();
  const all = [];
  for (;;) {
    const batch = await new Promise((res, rej) => reader.readEntries(res, rej));
    if (!batch.length) break;
    all.push(...batch);
  }
  const relDir = prefix ? prefix + '/' + dirEntry.name : dirEntry.name;
  const files = [];
  const dirs = [relDir];
  for (const ent of all) {
    if (ent.isDirectory) {
      const sub = await readDirectoryEntry(ent, relDir);
      files.push(...sub.files);
      dirs.push(...sub.dirs);
    } else {
      const file = await new Promise((res, rej) => ent.file(res, rej));
      files.push({ file, relPath: relDir + '/' + file.name });
    }
  }
  return { files, dirs };
}

async function collectDroppedTree(dt) {
  const files = [];
  const dirs = [];
  if (dt.items && dt.items.length) {
    const pending = [];
    for (const item of dt.items) {
      if (item.kind !== 'file') continue;
      const entry = item.webkitGetAsEntry ? item.webkitGetAsEntry() : null;
      if (entry && entry.isDirectory) {
        pending.push(readDirectoryEntry(entry, ''));
        continue;
      }
      const f = item.getAsFile ? item.getAsFile() : null;
      if (f) files.push({ file: f, relPath: f.name });
    }
    for (const sub of await Promise.all(pending)) {
      files.push(...sub.files);
      dirs.push(...sub.dirs);
    }
  } else if (dt.files) {
    return entriesFromFileList(dt.files, false);
  }
  return { files, dirs };
}

$('#btn-clear-uploads').addEventListener('click', () => {
  $('#upload-list').innerHTML = '';
  syncUploadPanelVisibility();
});

async function uploadFiles(input, destPath, opts = {}) {
  let packed;
  if (opts.tree) {
    packed = { files: input || [], dirs: opts.dirs || [] };
  } else {
    packed = entriesFromFileList(input || [], !!opts.folderPicker);
  }
  const listEntries = packed.files.filter((e) => e && e.file && e.file.name);
  if (!listEntries.length && !(packed.dirs && packed.dirs.length)) return;
  if (state.perm !== 'write') {
    alert('This folder is read-only');
    return;
  }
  const target = destPath == null ? state.path : destPath;
  for (const e of listEntries) {
    e.name = basenamePath(e.relPath) || e.file.name;
    e.dest = joinUploadPath(target, dirnamePath(e.relPath));
  }
  if (packed.dirs && packed.dirs.length) {
    await ensureUploadDirs(packed.dirs, target);
  }
  const decisions = listEntries.length ? await resolveUploadConflicts(listEntries, target) : new Map();
  if (decisions == null) return; // cancelled
  if (!listEntries.length) {
    invalidateTreeCache(state.root);
    try { await loadRoots(); } catch { /* ignore */ }
    try { await loadList(); } catch { /* ignore */ }
    return true;
  }
  ensureUploadPanel();
  const list = $('#upload-list');
  $('#btn-clear-uploads').hidden = true;
  const batchTotal = listEntries.length;
  let batchDone = 0;
  setUploadProgressLabel(batchDone, batchTotal);
  let anyErr = false;
  for (const e of listEntries) {
    const f = e.file;
    const action = decisions.get(e) || '';
    const li = document.createElement('li');
    li.className = 'upload-item';
    li.innerHTML = `<div class="row"><span class="name"></span><span class="meta"></span></div><div class="bar"><span></span></div>`;
    const shown = e.relPath && e.relPath !== f.name ? e.relPath : f.name;
    const label = e.dest ? `${shown} → ${e.dest}/` : shown;
    li.querySelector('.name').textContent = label;
    li.querySelector('.name').title = label;
    list.appendChild(li);
    if (action === 'skip') {
      setUploadItem(li, { loaded: f.size, total: f.size, status: 'skipped', detail: 'Skipped' });
      batchDone += 1;
      setUploadProgressLabel(batchDone, batchTotal);
      continue;
    }
    setUploadItem(li, { loaded: 0, total: f.size, status: 'uploading' });
    try {
      const data = await uploadOneFile(f, e.dest, (loaded, total) => {
        setUploadItem(li, { loaded, total, status: 'uploading' });
      }, action, e.relPath);
      let detail = f.size ? `Done · ${fmtSize(f.size)}` : 'Done';
      if (data && data.renamed && data.name) {
        detail = `Done · saved as ${data.name}`;
        const nameEl = li.querySelector('.name');
        const renamedLabel = `${shown} → ${data.name}`;
        nameEl.textContent = renamedLabel;
        nameEl.title = renamedLabel;
      } else if (data && data.overwritten) {
        detail = (f.size ? `Done · overwritten · ${fmtSize(f.size)}` : 'Done · overwritten');
      } else if (data && data.skipped) {
        setUploadItem(li, { loaded: f.size, total: f.size, status: 'skipped', detail: 'Skipped' });
        batchDone += 1;
        setUploadProgressLabel(batchDone, batchTotal);
        continue;
      }
      setUploadItem(li, { loaded: f.size, total: f.size, status: 'done', detail });
    } catch (err) {
      if (err && err.code === 'exists' && !action) {
        try {
          const takenBy = new Map([[e.dest, new Set([e.name])]]);
          const dirsBy = new Map([[e.dest, new Set()]]);
          const one = await showUploadConflictDialog([e], takenBy, dirsBy);
          if (one && one.get(e) && one.get(e) !== 'skip') {
            const data = await uploadOneFile(f, e.dest, (loaded, total) => {
              setUploadItem(li, { loaded, total, status: 'uploading' });
            }, one.get(e), e.relPath);
            let detail = f.size ? `Done · ${fmtSize(f.size)}` : 'Done';
            if (data && data.renamed && data.name) detail = `Done · saved as ${data.name}`;
            else if (data && data.overwritten) detail = (f.size ? `Done · overwritten · ${fmtSize(f.size)}` : 'Done · overwritten');
            setUploadItem(li, { loaded: f.size, total: f.size, status: 'done', detail });
            batchDone += 1;
            setUploadProgressLabel(batchDone, batchTotal);
            continue;
          }
          if (one && one.get(e) === 'skip') {
            setUploadItem(li, { loaded: f.size, total: f.size, status: 'skipped', detail: 'Skipped' });
            batchDone += 1;
            setUploadProgressLabel(batchDone, batchTotal);
            continue;
          }
        } catch (retryErr) {
          anyErr = true;
          setUploadItem(li, { loaded: 0, total: f.size, status: 'error', error: retryErr.message });
          batchDone += 1;
          setUploadProgressLabel(batchDone, batchTotal);
          continue;
        }
      }
      anyErr = true;
      setUploadItem(li, { loaded: 0, total: f.size, status: 'error', error: err.message });
    }
    batchDone += 1;
    setUploadProgressLabel(batchDone, batchTotal);
  }
  $('#btn-clear-uploads').hidden = false;
  invalidateTreeCache(state.root);
  try { await loadRoots(); } catch { /* ignore */ }
  try { await loadList(); } catch { /* keep status visible */ }
  try { await refreshMe(); } catch { /* ignore */ }
  return !anyErr;
}

function collectDroppedFiles(dt) {
  const out = [];
  if (dt.items && dt.items.length) {
    for (const item of dt.items) {
      if (item.kind !== 'file') continue;
      const entry = item.webkitGetAsEntry ? item.webkitGetAsEntry() : null;
      if (entry && entry.isDirectory) continue;
      const f = item.getAsFile();
      if (f) out.push(f);
    }
  } else if (dt.files) {
    out.push(...dt.files);
  }
  return out;
}


function findFolderDropPath(target) {
  const el = target && target.closest ? target.closest('.is-folder-drop') : null;
  return el ? el.dataset.dropPath : null;
}

(function setupDragDrop() {
  const zone = $('#files-dropzone');
  const hint = $('#drop-hint');
  if (!zone) return;
  let dragDepth = 0;

  function clearFolderHighlights() {
    document.querySelectorAll('.drop-folder-target').forEach((el) => el.classList.remove('drop-folder-target'));
  }

  function hasFiles(dt) {
    if (!dt) return false;
    if (dt.types && [...dt.types].includes('Files')) return true;
    if (dt.items) {
      for (const it of dt.items) if (it.kind === 'file') return true;
    }
    return !!(dt.files && dt.files.length);
  }

  window.addEventListener('dragover', (ev) => {
    if (hasFiles(ev.dataTransfer)) ev.preventDefault();
  });
  window.addEventListener('drop', (ev) => {
    if (hasFiles(ev.dataTransfer)) ev.preventDefault();
  });

  zone.addEventListener('dragenter', (ev) => {
    if (!hasFiles(ev.dataTransfer)) return;
    ev.preventDefault();
    dragDepth += 1;
    zone.classList.add('drop-active');
    if (hint) hint.hidden = false;
  });
  zone.addEventListener('dragleave', (ev) => {
    if (!hasFiles(ev.dataTransfer)) return;
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) {
      zone.classList.remove('drop-active');
      if (hint) hint.hidden = true;
      clearFolderHighlights();
    }
  });
  zone.addEventListener('dragover', (ev) => {
    if (!hasFiles(ev.dataTransfer)) return;
    ev.preventDefault();
    ev.dataTransfer.dropEffect = state.perm === 'write' ? 'copy' : 'none';
    clearFolderHighlights();
    const folderPath = findFolderDropPath(ev.target);
    if (folderPath != null) {
      const el = ev.target.closest('.is-folder-drop');
      if (el) el.classList.add('drop-folder-target');
      if (hint) {
        hint.hidden = false;
        hint.textContent = 'Drop to upload into “' + folderPath.split('/').pop() + '”';
      }
    } else if (hint) {
      hint.hidden = false;
      hint.textContent = 'Drop files or folders to upload here';
    }
  });
  zone.addEventListener('drop', async (ev) => {
    if (!hasFiles(ev.dataTransfer)) return;
    ev.preventDefault();
    ev.stopPropagation();
    dragDepth = 0;
    zone.classList.remove('drop-active');
    if (hint) {
      hint.hidden = true;
      hint.textContent = 'Drop files or folders to upload here';
    }
    const folderPath = findFolderDropPath(ev.target);
    clearFolderHighlights();
    const tree = await collectDroppedTree(ev.dataTransfer);
    if (!tree.files.length && !tree.dirs.length) return;
    await uploadFiles(tree.files, folderPath != null ? folderPath : state.path, { tree: true, dirs: tree.dirs });
  });
})();

$('#file-input').addEventListener('change', async (ev) => {
  const files = [...ev.target.files];
  ev.target.value = '';
  if (!files.length) return;
  await uploadFiles(files, state.path);
});

const folderInput = $('#folder-input');
if (folderInput) {
  folderInput.addEventListener('change', async (ev) => {
    const files = [...ev.target.files];
    ev.target.value = '';
    if (!files.length) return;
    await uploadFiles(files, state.path, { folderPicker: true });
  });
}

$('#btn-refresh').addEventListener('click', loadList);

function parseQuotaInput(s) {
  s = (s || '').trim();
  if (!s || s === '0' || /^unlim/i.test(s)) return 0;
  const m = s.match(/^(\d+(?:\.\d+)?)\s*(b|kb|mb|gb|tb)?$/i);
  if (!m) throw new Error('invalid quota: use e.g. 5GB, 500MB, or 0');
  const n = parseFloat(m[1]);
  const unit = (m[2] || 'b').toLowerCase();
  const mul = { b: 1, kb: 1024, mb: 1024**2, gb: 1024**3, tb: 1024**4 }[unit];
  return Math.round(n * mul);
}

function fmtQuota(q) {
  if (!q) return 'unlimited';
  return fmtSize(q);
}

function openUserEdit(u) {
  const dlg = $('#user-edit-dialog');
  const form = $('#edit-user');
  form.id.value = u.id;
  form.username.value = u.username;
  if (form.email) form.email.value = u.email || '';
  form.homePath.value = u.homePath;
  form.quota.value = u.quotaBytes ? fmtQuota(u.quotaBytes).replace(' ', '') : '0';
  if (form.quotaCustom) {
    form.quotaCustom.checked = !!u.quotaCustom;
    form.dataset.prevCustomQuota = u.quotaCustom && u.quotaBytes ? fmtQuota(u.quotaBytes).replace(' ', '') : '';
  }
  applyGlobalQuotaUI();
  form.isAdmin.checked = !!u.isAdmin;
  form.disabled.checked = !!u.disabled;
  form.totpRequired.checked = !!u.totpRequired;
  $('#edit-user-error').hidden = true;
  $('#set-password').id.value = u.id;
  $('#set-password').password.value = '';
  $('#set-password-msg').hidden = true;
  dlg.showModal();
}

function renderTotpSettings() {
  const u = state.user;
  const status = $('#totp-status');
  const msg = $('#totp-msg');
  if (!status || !u) return;
  msg.hidden = true;
  msg.classList.remove('error');
  $('#totp-setup-panel').hidden = true;
  $('#totp-disable-form').hidden = true;
  const required = !!(u.force2faAll || u.totpRequired);
  if (u.totpEnabled) {
    status.innerHTML = '<span class="badge admin">enabled</span>' +
      (required ? ' <span class="badge">required</span>' : '');
    $('#btn-totp-enable').hidden = true;
    $('#btn-totp-disable').hidden = !!required;
  } else {
    status.innerHTML = '<span class="badge">disabled</span>' +
      (required ? ' <span class="badge disabled">enrollment required</span>' : '');
    $('#btn-totp-enable').hidden = false;
    $('#btn-totp-disable').hidden = true;
  }
}

$('#btn-totp-enable').addEventListener('click', async () => {
  const msg = $('#totp-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  try {
    const data = await api('/api/me/totp/setup', { method: 'POST', body: '{}' });
    const img = $('#settings-totp-qr');
    if (data.qrDataUrl) { img.src = data.qrDataUrl; img.hidden = false; }
    $('#settings-totp-secret').textContent = data.secret || '';
    $('#totp-setup-panel').hidden = false;
    $('#totp-confirm-form').reset();
  } catch (err) {
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

$('#totp-confirm-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const msg = $('#totp-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  try {
    const data = await api('/api/me/totp/confirm', {
      method: 'POST',
      body: JSON.stringify({ code: fd.get('code') }),
    });
    state.user = data.user;
    state.onlyofficeReady = !!(data.user && data.user.onlyoffice && data.user.onlyoffice.ready);
    if (data.backupCodes && data.backupCodes.length) {
      const ul = $('#totp-backup-list');
      ul.innerHTML = data.backupCodes.map((c) => `<li><code>${c}</code></li>`).join('');
      $('#totp-backup-panel').hidden = false;
    }
    renderTotpSettings();
    applySettingsGate();
    msg.textContent = '2FA enabled';
    msg.hidden = false;
    if (state.user && !state.user.mustChangePassword && !state.user.mustEnrollTotp) {
      await enterApp();
    }
  } catch (err) {
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

$('#btn-totp-disable').addEventListener('click', () => {
  $('#totp-disable-form').hidden = false;
  $('#totp-disable-msg').hidden = true;
  $('#totp-disable-msg').classList.remove('error');
  $('#totp-disable-form').reset();
});

$('#totp-disable-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const msg = $('#totp-disable-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  const cred = String(fd.get('credential') || '');
  const body = {};
  if (/^\d{6}$/.test(cred) || /^[A-Fa-f0-9]{4}-?[A-Fa-f0-9]{4}$/.test(cred)) {
    body.code = cred;
  } else {
    body.password = cred;
  }
  try {
    const data = await api('/api/me/totp/disable', {
      method: 'POST',
      body: JSON.stringify(body),
    });
    state.user = data.user;
    state.onlyofficeReady = !!(data.user && data.user.onlyoffice && data.user.onlyoffice.ready);
    renderTotpSettings();
    msg.textContent = '2FA disabled';
    msg.hidden = false;
  } catch (err) {
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});


function ensureSelectOption(select, value, label) {
  if (!select) return;
  const v = String(value);
  for (const opt of select.options) {
    if (opt.value === v) {
      select.value = v;
      return;
    }
  }
  const opt = document.createElement('option');
  opt.value = v;
  opt.textContent = label || (v + ' seconds');
  select.appendChild(opt);
  select.value = v;
}

function formatDurationLabel(seconds) {
  const n = Number(seconds) || 0;
  const day = 86400;
  const hour = 3600;
  if (n % day === 0) {
    const d = n / day;
    return d === 1 ? '1 day' : (d + ' days');
  }
  if (n % hour === 0) {
    const h = n / hour;
    return h === 1 ? '1 hour' : (h + ' hours');
  }
  return n + ' seconds';
}

async function loadSystemSettings() {
  const data = await api('/api/admin/settings');
  const openReg = $('#open-registration');
  if (openReg) {
    openReg.checked = !!data.openRegistration;
    $('#open-registration-msg').hidden = true;
    $('#open-registration-msg').classList.remove('error');
  }
  const reqAp = $('#registration-require-approval');
  if (reqAp) {
    reqAp.checked = data.registrationRequireApproval !== false;
  }
  const box = $('#force-2fa-all');
  box.checked = !!data.force2faAll;
  box.disabled = !!data.force2faAllEnv;
  const note = $('#force-2fa-env-note');
  if (data.force2faAllEnv) {
    note.textContent = 'FORCE_2FA_ALL is set in the environment; UI toggle is locked on.';
    note.hidden = false;
  } else {
    note.hidden = true;
  }
  $('#force-2fa-msg').hidden = true;
  $('#force-2fa-msg').classList.remove('error');
  const pol = data.passwordPolicy || {};
  const form = $('#password-policy-form');
  if (form) {
    form.passwordMinLength.value = pol.minLength || 4;
    form.passwordRequireLetters.checked = !!pol.requireLetters;
    form.passwordRequireDigits.checked = !!pol.requireDigits;
    form.passwordRequireSymbols.checked = !!pol.requireSymbols;
  }
  const ver = $('#password-policy-version');
  if (ver) ver.textContent = 'Policy version: ' + (pol.version ?? 0) + (data.passwordPolicyHint ? ' · ' + data.passwordPolicyHint : '');
  $('#password-policy-msg').hidden = true;
  $('#password-policy-msg').classList.remove('error');
  updatePasswordPolicyHints({ passwordPolicy: pol, passwordPolicyHint: data.passwordPolicyHint });
  const smtp = data.smtp || {};
  const sf = $('#smtp-form');
  if (sf) {
    sf.smtpEnabled.checked = !!smtp.enabled;
    sf.smtpHost.value = smtp.host || '';
    sf.smtpPort.value = smtp.port || 587;
    sf.smtpUsername.value = smtp.username || '';
    sf.smtpPassword.value = '';
    sf.smtpFrom.value = smtp.from || '';
    const locked = !!smtp.envOverride;
    ['smtpEnabled','smtpHost','smtpPort','smtpUsername','smtpPassword','smtpFrom'].forEach((n) => {
      if (sf[n]) sf[n].disabled = locked && n !== 'smtpPassword' ? locked : false;
    });
    // Keep editable in UI even with env override so admin can still save DB values,
    // but note override in meta.
    ['smtpEnabled','smtpHost','smtpPort','smtpUsername','smtpPassword','smtpFrom'].forEach((n) => {
      if (sf[n]) sf[n].disabled = false;
    });
    const setNote = $('#smtp-password-set');
    if (setNote) {
      setNote.textContent = smtp.passwordSet ? 'A password is saved (leave field blank to keep it).' : 'No password saved yet.';
      setNote.hidden = false;
    }
    const ready = $('#smtp-ready');
    if (ready) {
      let t = smtp.ready ? 'SMTP ready for forgot-password emails.' : 'SMTP not ready (need enable + host + from).';
      if (smtp.envOverride) t += ' Env SMTP_* overrides apply on top of saved settings.';
      if (data.publicBaseURL) t += ' PUBLIC_BASE_URL=' + data.publicBaseURL;
      ready.textContent = t;
    }
  }
  $('#smtp-msg').hidden = true;
  $('#smtp-msg').classList.remove('error');
  const sessForm = $('#session-settings-form');
  if (sessForm) {
    ensureSelectOption(sessForm.sessionTtlSeconds, data.sessionTtlSeconds ?? 604800, formatDurationLabel(data.sessionTtlSeconds ?? 604800));
    ensureSelectOption(sessForm.rememberMeTtlSeconds, data.rememberMeTtlSeconds ?? 2592000, formatDurationLabel(data.rememberMeTtlSeconds ?? 2592000));
    sessForm.allowRememberMe.checked = data.allowRememberMe !== false;
    const wrap = $('#remember-me-ttl-wrap');
    if (wrap) wrap.hidden = !sessForm.allowRememberMe.checked;
  }
  const sessMsg = $('#session-settings-msg');
  if (sessMsg) {
    sessMsg.hidden = true;
    sessMsg.classList.remove('error');
  }
  state.globalQuotaEnabled = !!data.globalQuotaEnabled;
  state.globalQuotaBytes = Number(data.globalQuotaBytes) || 0;
  const gqForm = $('#global-quota-form');
  if (gqForm) {
    gqForm.globalQuotaEnabled.checked = state.globalQuotaEnabled;
    gqForm.globalQuota.value = state.globalQuotaBytes ? fmtQuota(state.globalQuotaBytes).replace(' ', '') : '0';
  }
  const gqMsg = $('#global-quota-msg');
  if (gqMsg) {
    gqMsg.hidden = true;
    gqMsg.classList.remove('error');
  }
  const oidc = data.oidc || {};
  const of = $('#oidc-form');
  if (of) {
    of.oidcEnabled.checked = !!oidc.enabled;
    of.oidcIssuer.value = oidc.issuer || '';
    of.oidcClientId.value = oidc.clientId || '';
    of.oidcClientSecret.value = '';
    of.oidcLabel.value = oidc.label || 'Sign in with SSO';
    of.oidcAutoCreate.checked = !!oidc.autoCreate;
    if (of.oidcRedirectURL) of.oidcRedirectURL.value = oidc.redirectURL || data.oidcRedirectURL || '';
    const setNote = $('#oidc-secret-set');
    if (setNote) {
      const saved = !!(oidc.clientSecretSet || data.oidcClientSecretSet);
      setNote.textContent = saved ? 'A client secret is saved (leave field blank to keep it).' : 'No client secret saved yet.';
      setNote.hidden = false;
    }
    const ready = $('#oidc-ready');
    if (ready) {
      let t = '';
      if (!oidc.publicBaseURLSet && !data.publicBaseURL) {
        t = 'Set PUBLIC_BASE_URL before enabling SSO (the redirect URI is based on it).';
      } else if (oidc.enabled && oidc.issuer && oidc.clientId && (oidc.clientSecretSet || data.oidcClientSecretSet)) {
        t = 'SSO is enabled. Local username and password sign-in remains available.';
      } else {
        t = 'SSO is off. Local username and password sign-in is unchanged.';
      }
      if (oidc.envOverride) t += ' Env OIDC_* overrides apply on top of saved settings.';
      if (data.publicBaseURL) t += ' PUBLIC_BASE_URL=' + data.publicBaseURL;
      ready.textContent = t;
    }
  }
  const oidcMsg = $('#oidc-msg');
  if (oidcMsg) {
    oidcMsg.hidden = true;
    oidcMsg.classList.remove('error');
  }
  const oo = data.onlyoffice || {};
  const oof = $('#onlyoffice-form');
  if (oof) {
    oof.onlyofficeEnabled.checked = !!oo.enabled;
    oof.onlyofficeUrl.value = oo.url || '';
    oof.onlyofficeJwtSecret.value = '';
    const setNote = $('#onlyoffice-secret-set');
    if (setNote) {
      setNote.textContent = oo.jwtSecretSet ? 'A JWT secret is saved (leave field blank to keep it).' : 'No JWT secret saved yet.';
      setNote.hidden = false;
    }
    const ready = $('#onlyoffice-ready');
    if (ready) {
      let t = '';
      if (!oo.publicBaseURLSet && !data.publicBaseURL) {
        t = 'Set PUBLIC_BASE_URL before enabling ONLYOFFICE (the Document Server must reach Filehub at that URL).';
      } else if (oo.ready) {
        t = 'ONLYOFFICE is ready. Users can open supported Office files with Edit.';
      } else if (oo.enabled) {
        t = 'ONLYOFFICE is enabled but not ready (need Document Server URL + JWT secret + PUBLIC_BASE_URL).';
      } else {
        t = 'ONLYOFFICE is off. Existing download and preview are unchanged.';
      }
      if (data.publicBaseURL) t += ' PUBLIC_BASE_URL=' + data.publicBaseURL;
      ready.textContent = t;
    }
    const dsHealth = $('#onlyoffice-ds-health');
    if (dsHealth) {
      if (oo.enabled && oo.url) {
        dsHealth.hidden = false;
        if (oo.documentServerReachable === true) {
          dsHealth.textContent = 'Document Server at ' + oo.url + ' is reachable.';
          dsHealth.classList.remove('error');
        } else if (oo.documentServerReachable === false) {
          dsHealth.textContent = 'Warning: Document Server did not respond at ' + oo.url + (oo.documentServerHealth ? ' (' + oo.documentServerHealth + ')' : '') + '.';
          dsHealth.classList.add('error');
        } else {
          dsHealth.textContent = 'Document Server URL: ' + oo.url;
          dsHealth.classList.remove('error');
        }
      } else {
        dsHealth.hidden = true;
        dsHealth.textContent = '';
      }
    }
  }
  const ooMsg = $('#onlyoffice-msg');
  if (ooMsg) {
    ooMsg.hidden = true;
    ooMsg.classList.remove('error');
  }
  applyAuditLogMaxForm(data.auditLogMax);
  const auditMaxMsg = $('#audit-log-max-msg');
  if (auditMaxMsg) {
    auditMaxMsg.hidden = true;
    auditMaxMsg.classList.remove('error');
  }
  applyGlobalQuotaUI();
}

$('#oidc-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const msg = $('#oidc-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  const body = {
    oidcEnabled: !!e.target.oidcEnabled.checked,
    oidcIssuer: e.target.oidcIssuer.value,
    oidcClientId: e.target.oidcClientId.value,
    oidcAutoCreate: !!e.target.oidcAutoCreate.checked,
    oidcLabel: e.target.oidcLabel.value,
  };
  const secret = e.target.oidcClientSecret.value;
  if (secret !== '') body.oidcClientSecret = secret;
  try {
    const data = await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify(body),
    });
    await loadSystemSettingsFrom(data);
    msg.textContent = 'SSO settings saved';
    msg.hidden = false;
  } catch (err) {
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

$('#onlyoffice-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const msg = $('#onlyoffice-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  const body = {
    onlyofficeEnabled: !!e.target.onlyofficeEnabled.checked,
    onlyofficeUrl: e.target.onlyofficeUrl.value,
  };
  const secret = e.target.onlyofficeJwtSecret.value;
  if (secret !== '') body.onlyofficeJwtSecret = secret;
  try {
    await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify(body),
    });
    await loadSystemSettingsFrom();
    try { await refreshMe(); } catch { /* ignore */ }
    msg.textContent = 'ONLYOFFICE settings saved';
    msg.hidden = false;
  } catch (err) {
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

async function loadSystemSettingsFrom(data) {
  if (!data) {
    await loadSystemSettings();
    return;
  }
  // Reuse GET-shaped payload from PUT.
  // loadSystemSettings always GETs; after save, refresh from GET so nested oidc is consistent.
  await loadSystemSettings();
}

$('#allow-remember-me')?.addEventListener('change', (e) => {
  const wrap = $('#remember-me-ttl-wrap');
  if (wrap) wrap.hidden = !e.target.checked;
});

$('#session-settings-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const msg = $('#session-settings-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  try {
    const data = await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify({
        sessionTtlSeconds: Number(e.target.sessionTtlSeconds.value),
        rememberMeTtlSeconds: Number(e.target.rememberMeTtlSeconds.value),
        allowRememberMe: !!e.target.allowRememberMe.checked,
      }),
    });
    ensureSelectOption(e.target.sessionTtlSeconds, data.sessionTtlSeconds, formatDurationLabel(data.sessionTtlSeconds));
    ensureSelectOption(e.target.rememberMeTtlSeconds, data.rememberMeTtlSeconds, formatDurationLabel(data.rememberMeTtlSeconds));
    e.target.allowRememberMe.checked = !!data.allowRememberMe;
    const wrap = $('#remember-me-ttl-wrap');
    if (wrap) wrap.hidden = !e.target.allowRememberMe.checked;
    msg.textContent = 'Session settings saved (applies to new logins only)';
    msg.hidden = false;
  } catch (err) {
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

document.querySelector('#edit-user')?.quotaCustom?.addEventListener('change', () => {
  const form = $('#edit-user');
  if (form && form.quotaCustom && form.quotaCustom.checked && form.dataset.prevCustomQuota) {
    form.quota.value = form.dataset.prevCustomQuota;
  }
  applyGlobalQuotaUI();
});

function applyGlobalQuotaUI() {
  const on = !!state.globalQuotaEnabled;
  const createQuota = $('#create-user-quota');
  const createNote = $('#create-user-quota-note');
  if (createQuota) {
    createQuota.disabled = on;
    createQuota.hidden = on;
  }
  if (createNote) createNote.hidden = !on;
  const form = $('#edit-user');
  const customWrap = $('#edit-user-quota-custom-wrap');
  if (customWrap) customWrap.hidden = !on;
  const customBox = form && form.quotaCustom;
  const editQuota = form && form.quota;
  const custom = !!(customBox && customBox.checked);
  if (editQuota) {
    const lock = on && !custom;
    editQuota.disabled = lock;
    editQuota.readOnly = lock;
    if (lock) {
      editQuota.value = state.globalQuotaBytes ? fmtQuota(state.globalQuotaBytes).replace(' ', '') : '0';
    }
  }
  const editNote = $('#edit-user-quota-note');
  if (editNote) {
    editNote.hidden = !on;
    editNote.textContent = (on && !custom)
      ? 'This user is using the default quota from Settings → Storage quota. Tick “Use a custom quota” to set a different limit.'
      : (on ? 'This user has a custom quota that overrides the default.' : '');
  }
}

$('#global-quota-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const msg = $('#global-quota-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  const enabled = !!e.target.globalQuotaEnabled.checked;
  const raw = (e.target.globalQuota.value || '').trim();
  if (enabled && !raw) {
    msg.textContent = 'Enter a quota (e.g. 5GB, 500MB, or 0 for unlimited).';
    msg.classList.add('error');
    msg.hidden = false;
    return;
  }
  let globalQuotaBytes = state.globalQuotaBytes || 0;
  if (raw) {
    try { globalQuotaBytes = parseQuotaInput(raw); }
    catch (err) {
      msg.textContent = err.message;
      msg.classList.add('error');
      msg.hidden = false;
      return;
    }
  }
  try {
    const data = await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify({ globalQuotaEnabled: enabled, globalQuotaBytes }),
    });
    state.globalQuotaEnabled = !!data.globalQuotaEnabled;
    state.globalQuotaBytes = Number(data.globalQuotaBytes) || 0;
    e.target.globalQuotaEnabled.checked = state.globalQuotaEnabled;
    e.target.globalQuota.value = state.globalQuotaBytes ? fmtQuota(state.globalQuotaBytes).replace(' ', '') : '0';
    applyGlobalQuotaUI();
    msg.textContent = state.globalQuotaEnabled
      ? 'Saved — all users now have this quota'
      : 'Saved — per-user quotas can be edited again';
    msg.hidden = false;
    try { await refreshAdmin(); } catch { /* ignore */ }
    try { await refreshMe(); } catch { /* ignore */ }
  } catch (err) {
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

$('#open-registration')?.addEventListener('change', async (e) => {
  const msg = $('#open-registration-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  try {
    const data = await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify({ openRegistration: !!e.target.checked }),
    });
    e.target.checked = !!data.openRegistration;
    msg.textContent = 'Saved';
    msg.hidden = false;
  } catch (err) {
    e.target.checked = !e.target.checked;
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

$('#registration-require-approval')?.addEventListener('change', async (e) => {
  const msg = $('#open-registration-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  try {
    const data = await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify({ registrationRequireApproval: !!e.target.checked }),
    });
    e.target.checked = data.registrationRequireApproval !== false;
    msg.textContent = 'Saved';
    msg.hidden = false;
  } catch (err) {
    e.target.checked = !e.target.checked;
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

$('#force-2fa-all').addEventListener('change', async (e) => {
  const msg = $('#force-2fa-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  try {
    const data = await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify({ force2faAll: !!e.target.checked }),
    });
    e.target.checked = !!data.force2faAll;
    msg.textContent = 'Saved';
    msg.hidden = false;
  } catch (err) {
    e.target.checked = !e.target.checked;
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

$('#password-policy-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const msg = $('#password-policy-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  try {
    const data = await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify({
        passwordMinLength: Number(fd.get('passwordMinLength')),
        passwordRequireLetters: !!e.target.passwordRequireLetters.checked,
        passwordRequireDigits: !!e.target.passwordRequireDigits.checked,
        passwordRequireSymbols: !!e.target.passwordRequireSymbols.checked,
      }),
    });
    const pol = data.passwordPolicy || {};
    e.target.passwordMinLength.value = pol.minLength || 4;
    e.target.passwordRequireLetters.checked = !!pol.requireLetters;
    e.target.passwordRequireDigits.checked = !!pol.requireDigits;
    e.target.passwordRequireSymbols.checked = !!pol.requireSymbols;
    const ver = $('#password-policy-version');
    if (ver) ver.textContent = 'Policy version: ' + (pol.version ?? 0) + (data.passwordPolicyHint ? ' · ' + data.passwordPolicyHint : '');
    updatePasswordPolicyHints({ passwordPolicy: pol, passwordPolicyHint: data.passwordPolicyHint });
    msg.textContent = 'Password policy saved' + (pol.version ? ` (version ${pol.version})` : '');
    msg.hidden = false;
  } catch (err) {
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

function grantUserName(userId) {
  const u = (state.adminUsers || []).find((x) => Number(x.id) === Number(userId));
  return u ? u.username : ('user#' + userId);
}

function grantAccessSummary(g) {
  const parts = [g.perm === 'write' ? 'write' : 'read'];
  if (g.canDelete) parts.push('delete');
  return parts.join(', ');
}

function renderShareGrantList(s) {
  const grants = s.grants || [];
  if (!grants.length) return '<span class="muted">No grants</span>';
  return '<div class="grant-list">' + grants.map((g) => {
    const name = escapeHtml(g.username || grantUserName(g.userId));
    const summary = escapeHtml(grantAccessSummary(g));
    const readSel = g.perm === 'read' ? ' selected' : '';
    const writeSel = g.perm === 'write' ? ' selected' : '';
    const delChk = g.canDelete ? ' checked' : '';
    return '<div class="grant-row">' +
      '<span class="grant-name" title="' + name + '">' + name + '</span>' +
      '<span class="grant-summary">' + summary + '</span>' +
      '<select class="grant-perm" data-share-id="' + s.id + '" data-user-id="' + g.userId + '" aria-label="Access for ' + name + '">' +
        '<option value="read"' + readSel + '>Read</option>' +
        '<option value="write"' + writeSel + '>Write</option>' +
      '</select>' +
      '<label class="check grant-delete-flag"><input type="checkbox" class="grant-can-delete" data-share-id="' + s.id + '" data-user-id="' + g.userId + '" aria-label="Allow delete for ' + name + '"' + delChk + ' /> Allow delete</label>' +
      '<button type="button" class="ghost danger grant-revoke" data-share-id="' + s.id + '" data-user-id="' + g.userId + '" data-username="' + name + '" title="Revoke grant">Revoke</button>' +
    '</div>';
  }).join('') + '</div>';
}

function fillGrantUserSelect(share) {
  const sel = $('#share-grant-user');
  if (!sel) return;
  const granted = new Set((share.grants || []).map((g) => Number(g.userId)));
  const users = (state.adminUsers || []).filter((u) => !u.disabled && !granted.has(Number(u.id)));
  sel.innerHTML = '';
  if (!users.length) {
    const opt = document.createElement('option');
    opt.value = '';
    opt.textContent = 'No users available';
    sel.appendChild(opt);
    sel.disabled = true;
    const submit = $('#share-grant-form') && $('#share-grant-form').querySelector('button[type="submit"]');
    if (submit) submit.disabled = true;
    return;
  }
  sel.disabled = false;
  const placeholder = document.createElement('option');
  placeholder.value = '';
  placeholder.textContent = 'Select a user';
  sel.appendChild(placeholder);
  for (const u of users) {
    const opt = document.createElement('option');
    opt.value = String(u.id);
    opt.textContent = u.username + (u.isAdmin ? ' (admin)' : '');
    sel.appendChild(opt);
  }
  const submit = $('#share-grant-form') && $('#share-grant-form').querySelector('button[type="submit"]');
  if (submit) submit.disabled = false;
}

function renderDialogGrants(share) {
  const box = $('#share-grant-current');
  const empty = $('#share-grant-empty');
  if (!box || !empty) return;
  const grants = share.grants || [];
  if (!grants.length) {
    box.innerHTML = '';
    empty.hidden = false;
    return;
  }
  empty.hidden = true;
  box.innerHTML = renderShareGrantList(share);
}

function openShareGrantDialog(share) {
  state.grantShareId = share.id;
  const dlg = $('#share-grant-dialog');
  if (!dlg) return;
  const title = $('#share-grant-title');
  if (title) title.textContent = 'Grant access';
  const target = $('#share-grant-target');
  if (target) target.textContent = (share.name || '') + (share.path ? ' — ' + share.path : '');
  const form = $('#share-grant-form');
  if (form) {
    form.shareId.value = share.id;
    form.perm.value = 'read';
    if (form.canDelete) form.canDelete.checked = false;
  }
  const err = $('#share-grant-error');
  if (err) { err.hidden = true; err.textContent = ''; err.classList.remove('error'); }
  fillGrantUserSelect(share);
  renderDialogGrants(share);
  if (typeof dlg.showModal === 'function') dlg.showModal();
  else dlg.setAttribute('open', '');
}

async function afterShareGrantChange(shareId) {
  await refreshAdmin();
  try { await loadRoots(); } catch { /* ignore */ }
  const dlg = $('#share-grant-dialog');
  if (dlg && dlg.open) {
    const s = (state.adminShares || []).find((x) => Number(x.id) === Number(shareId));
    if (s) {
      fillGrantUserSelect(s);
      renderDialogGrants(s);
    } else {
      dlg.close();
    }
  }
}

async function changeShareGrant(shareId, userId, perm, canDelete) {
  await api('/api/admin/shares/' + shareId + '/grants', {
    method: 'POST',
    body: JSON.stringify({ userId: Number(userId), perm, canDelete: !!canDelete }),
  });
  await afterShareGrantChange(shareId);
}

function grantControlsFrom(el) {
  const row = el.closest('.grant-row');
  if (!row) return null;
  const sel = row.querySelector('select.grant-perm');
  const box = row.querySelector('input.grant-can-delete');
  if (!sel) return null;
  return {
    shareId: sel.getAttribute('data-share-id'),
    userId: sel.getAttribute('data-user-id'),
    perm: sel.value,
    canDelete: !!(box && box.checked),
    controls: [sel, box],
  };
}

async function revokeShareGrant(shareId, userId, username) {
  const who = username || grantUserName(userId);
  if (!confirm('Revoke ' + who + "'s access to this share?")) return;
  await api('/api/admin/shares/' + shareId + '/grants?userId=' + encodeURIComponent(userId), { method: 'DELETE' });
  await afterShareGrantChange(shareId);
}

async function refreshAdmin() {
  const users = await api('/api/admin/users');
  state.adminUsers = users.users || [];
  const ul = $('#user-list');
  ul.innerHTML = '';
  for (const u of users.users) {
    const tr = document.createElement('tr');
    const used = u.usedBytes || 0;
    const quota = u.quotaBytes || 0;
    const pct = quota > 0 ? Math.min(100, Math.round(used / quota * 100)) : 0;
    const badges = [];
    if (u.isAdmin) badges.push('<span class="badge admin">admin</span>');
    if (u.status === 'pending') badges.push('<span class="badge disabled">pending</span>');
    if (u.disabled) badges.push('<span class="badge disabled">disabled</span>');
    if (u.totpEnabled) badges.push('<span class="badge admin">2FA</span>');
    else if (u.totpRequired || u.force2faAll) badges.push('<span class="badge disabled">2FA req</span>');
    if (!badges.length) badges.push('<span class="badge">user</span>');
    let quotaLabel = quota
      ? `${fmtSize(used)} / ${fmtQuota(quota)} (${pct}%)`
      : `${fmtSize(used)} / unlimited`;
    if (state.globalQuotaEnabled) {
      quotaLabel += u.quotaCustom ? ' · custom' : ' · default';
    }
    tr.innerHTML = `
      <td class="user-cell">
        <div class="user-name" title="${u.username}">${u.username}</div>
        <div class="meta">#${u.id}</div>
      </td>
      <td class="home-cell"><code title="${u.homePath}">${u.homePath}</code></td>
      <td class="quota-cell">
        <div class="meta">${quotaLabel}</div>
        ${quota ? `<div class="quota-bar${used > quota ? ' over' : ''}"><span style="width:${pct}%"></span></div>` : ''}
      </td>
      <td class="status-cell">${badges.join(' ')}</td>
      <td class="row-actions"><div class="row-actions-inner"></div></td>
    `;
    const btns = tr.querySelector('.row-actions-inner');
    const edit = document.createElement('button');
    edit.textContent = 'Edit';
    edit.className = 'ghost';
    edit.title = 'Edit user';
    edit.onclick = () => openUserEdit(u);
    btns.appendChild(edit);
    if (u.status === 'pending') {
      const ap = document.createElement('button');
      ap.textContent = 'Approve';
      ap.className = 'ghost';
      ap.title = 'Approve registration';
      ap.onclick = async () => {
        await api('/api/admin/users/' + u.id + '/approve', { method: 'POST', body: '{}' });
        await refreshAdmin();
      };
      btns.appendChild(ap);
      const rj = document.createElement('button');
      rj.textContent = 'Reject';
      rj.className = 'ghost danger';
      rj.title = 'Reject and delete pending account';
      rj.onclick = async () => {
        if (!confirm('Reject and delete pending user ' + u.username + '? The username can be reused.')) return;
        await api('/api/admin/users/' + u.id + '/reject', { method: 'POST', body: '{}' });
        await refreshAdmin();
      };
      btns.appendChild(rj);
    }
    if (u.id !== state.user.id && u.status !== 'pending') {
      const b = document.createElement('button');
      b.textContent = 'Del';
      b.className = 'ghost danger';
      b.title = 'Delete user';
      b.onclick = async () => {
        if (!confirm('Delete user ' + u.username + '?')) return;
        await api('/api/admin/users/' + u.id, { method: 'DELETE' });
        await refreshAdmin();
      };
      btns.appendChild(b);
    }
    ul.appendChild(tr);
  }
  if (!users.users.length) {
    const tr = document.createElement('tr');
    tr.innerHTML = '<td colspan="5" class="empty-row muted">No users</td>';
    ul.appendChild(tr);
  }

  const shares = await api('/api/admin/shares');
  state.adminShares = shares.shares || [];
  const sl = $('#share-list');
  sl.innerHTML = '';
  for (const s of shares.shares) {
    const tr = document.createElement('tr');
    tr.innerHTML = `
      <td class="share-name-cell"><div class="user-name" title="${s.name}">${s.name}</div><div class="meta">#${s.id}</div></td>
      <td class="home-cell"><code title="${s.path}">${s.path}</code></td>
      <td class="grants-cell">${renderShareGrantList(s)}</td>
      <td class="row-actions"></td>
    `;
    const btns = tr.querySelector('.row-actions');
    const grantBtn = document.createElement('button');
    grantBtn.type = 'button';
    grantBtn.textContent = 'Grant';
    grantBtn.className = 'ghost';
    grantBtn.title = 'Grant access';
    grantBtn.onclick = () => openShareGrantDialog(s);
    const del = document.createElement('button');
    del.type = 'button';
    del.textContent = 'Del';
    del.className = 'ghost danger';
    del.title = 'Delete share';
    del.onclick = async () => {
      if (!confirm('Delete share definition? (files kept)')) return;
      await api('/api/admin/shares/' + s.id, { method: 'DELETE' });
      await refreshAdmin();
    };
    btns.appendChild(grantBtn);
    btns.appendChild(del);
    sl.appendChild(tr);
  }
  if (!shares.shares.length) {
    const tr = document.createElement('tr');
    tr.innerHTML = '<td colspan="4" class="empty-row muted">No shared folders</td>';
    sl.appendChild(tr);
  }
}

const shareListBody = $('#share-list');
if (shareListBody && !shareListBody.dataset.grantBound) {
  shareListBody.dataset.grantBound = '1';
  shareListBody.addEventListener('change', async (e) => {
    const el = e.target.closest && (e.target.closest('select.grant-perm') || e.target.closest('input.grant-can-delete'));
    if (!el) return;
    const vals = grantControlsFrom(el);
    if (!vals) return;
    vals.controls.forEach((c) => { if (c) c.disabled = true; });
    try {
      await changeShareGrant(vals.shareId, vals.userId, vals.perm, vals.canDelete);
    } catch (err) {
      alert(err.message || 'Could not update grant');
      await refreshAdmin();
    }
  });
  shareListBody.addEventListener('click', async (e) => {
    const btn = e.target.closest && e.target.closest('button.grant-revoke');
    if (!btn) return;
    try {
      await revokeShareGrant(btn.getAttribute('data-share-id'), btn.getAttribute('data-user-id'), btn.getAttribute('data-username'));
    } catch (err) {
      alert(err.message || 'Could not revoke grant');
    }
  });
}
const shareGrantCurrent = $('#share-grant-current');
if (shareGrantCurrent && !shareGrantCurrent.dataset.grantBound) {
  shareGrantCurrent.dataset.grantBound = '1';
  shareGrantCurrent.addEventListener('change', async (e) => {
    const el = e.target.closest && (e.target.closest('select.grant-perm') || e.target.closest('input.grant-can-delete'));
    if (!el) return;
    const vals = grantControlsFrom(el);
    if (!vals) return;
    vals.controls.forEach((c) => { if (c) c.disabled = true; });
    try {
      await changeShareGrant(vals.shareId, vals.userId, vals.perm, vals.canDelete);
    } catch (err) {
      alert(err.message || 'Could not update grant');
      await refreshAdmin();
    }
  });
  shareGrantCurrent.addEventListener('click', async (e) => {
    const btn = e.target.closest && e.target.closest('button.grant-revoke');
    if (!btn) return;
    try {
      await revokeShareGrant(btn.getAttribute('data-share-id'), btn.getAttribute('data-user-id'), btn.getAttribute('data-username'));
    } catch (err) {
      alert(err.message || 'Could not revoke grant');
    }
  });
}
const shareGrantForm = $('#share-grant-form');
if (shareGrantForm && !shareGrantForm.dataset.bound) {
  shareGrantForm.dataset.bound = '1';
  shareGrantForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const errEl = $('#share-grant-error');
    if (errEl) { errEl.hidden = true; errEl.textContent = ''; }
    const shareId = Number(shareGrantForm.shareId.value);
    const userId = Number(shareGrantForm.userId.value);
    const perm = shareGrantForm.perm.value;
    const canDelete = !!(shareGrantForm.canDelete && shareGrantForm.canDelete.checked);
    if (!shareId || !userId) {
      if (errEl) { errEl.textContent = 'Select a user'; errEl.hidden = false; }
      return;
    }
    const submit = shareGrantForm.querySelector('button[type="submit"]');
    if (submit) submit.disabled = true;
    try {
      await api('/api/admin/shares/' + shareId + '/grants', {
        method: 'POST',
        body: JSON.stringify({ userId, perm, canDelete }),
      });
      shareGrantForm.userId.value = '';
      await afterShareGrantChange(shareId);
    } catch (err) {
      if (errEl) { errEl.textContent = err.message || 'Could not add grant'; errEl.hidden = false; }
    } finally {
      if (submit) submit.disabled = false;
    }
  });
}

$('#create-user').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  let quotaBytes = 0;
  if (!state.globalQuotaEnabled) {
    try { quotaBytes = parseQuotaInput(fd.get('quota')); }
    catch (err) { alert(err.message); return; }
  }
  const body = {
    username: fd.get('username'),
    email: fd.get('email') || '',
    password: fd.get('password'),
    homePath: fd.get('homePath') || '',
    isAdmin: !!fd.get('isAdmin'),
  };
  if (!state.globalQuotaEnabled) body.quotaBytes = quotaBytes;
  await api('/api/admin/users', {
    method: 'POST',
    body: JSON.stringify(body),
  });
  e.target.reset();
  applyGlobalQuotaUI();
  await refreshAdmin();
});

$('#edit-user').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const errEl = $('#edit-user-error');
  errEl.hidden = true;
  const custom = !state.globalQuotaEnabled || !!fd.get('quotaCustom');
  let quotaBytes = 0;
  if (custom) {
    try { quotaBytes = parseQuotaInput(fd.get('quota')); }
    catch (err) { errEl.textContent = err.message; errEl.hidden = false; return; }
  }
  try {
    const body = {
      username: fd.get('username'),
      email: fd.get('email') || '',
      homePath: fd.get('homePath'),
      isAdmin: !!fd.get('isAdmin'),
      disabled: !!fd.get('disabled'),
      totpRequired: !!fd.get('totpRequired'),
      quotaCustom: custom && !!state.globalQuotaEnabled,
    };
    if (custom) body.quotaBytes = quotaBytes;
    if (state.globalQuotaEnabled && !custom) body.quotaCustom = false;
    await api('/api/admin/users/' + fd.get('id'), {
      method: 'PATCH',
      body: JSON.stringify(body),
    });
    $('#user-edit-dialog').close();
    await refreshAdmin();
  } catch (err) {
    errEl.textContent = err.message;
    errEl.hidden = false;
  }
});

$('#set-password').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const msg = $('#set-password-msg');
  msg.hidden = true;
  try {
    await api('/api/admin/users/' + fd.get('id') + '/password', {
      method: 'POST',
      body: JSON.stringify({ password: fd.get('password') }),
    });
    msg.textContent = 'Password updated (sessions cleared)';
    msg.hidden = false;
    e.target.password.value = '';
  } catch (err) {
    msg.textContent = err.message;
    msg.hidden = false;
  }
});

$('#create-share').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  await api('/api/admin/shares', {
    method: 'POST',
    body: JSON.stringify({ name: fd.get('name'), path: fd.get('path') }),
  });
  e.target.reset();
  await refreshAdmin();
});


/* ---- multi-select / bulk / link share / public ---- */

const selectAllEl = $('#select-all');
if (selectAllEl) {
  selectAllEl.addEventListener('change', () => {
    const on = selectAllEl.checked;
    state.selected.clear();
    if (on && state.entries) {
      for (const e of state.entries) state.selected.add(e.path);
    }
    const canWrite = state.perm === 'write';
    renderEntries(sortEntries(state.entries || []), canWrite);
    updateBulkBar();
  });
}

function selectedEntries() {
  const set = state.selected;
  return (state.entries || []).filter((e) => set.has(e.path));
}

$('#btn-bulk-clear')?.addEventListener('click', () => {
  state.selected.clear();
  const canWrite = state.perm === 'write';
  renderEntries(sortEntries(state.entries || []), canWrite);
  updateBulkBar();
});

$('#btn-bulk-delete')?.addEventListener('click', async () => {
  const paths = [...state.selected];
  if (!paths.length || !state.canDelete) return;
  if (!confirm(`Delete ${paths.length} item(s)?`)) return;
  try {
    const res = await api('/api/batch/delete', {
      method: 'POST',
      body: JSON.stringify({ root: state.root, paths }),
    });
    if (res.failed && res.failed.length) {
      alert('Some deletes failed:\n' + res.failed.map((f) => f.path + ': ' + f.error).join('\n'));
    }
    invalidateTreeCache(state.root);
    await loadRoots();
    await loadList();
    try { await refreshMe(); } catch { /* ignore */ }
  } catch (err) {
    alert(err.message);
  }
});

function transferRootName(id) {
  const r = (state.roots || []).find((x) => x.id === id);
  return r ? r.name : (id || 'Destination');
}

function transferNormalizePath(p) {
  return String(p || '').replace(/^\/+|\/+$/g, '');
}

function transferParentPath(path) {
  const norm = transferNormalizePath(path);
  if (!norm) return '';
  const i = norm.lastIndexOf('/');
  return i === -1 ? '' : norm.slice(0, i);
}

function transferDestLabel() {
  const name = transferRootName(state.transferDestRoot);
  const path = transferNormalizePath(state.transferDestPath);
  if (!path) return name;
  return name + ' / ' + path.split('/').join(' / ');
}

/** Returns a selected folder path if dest is that folder or a descendant of it. */
function transferSelfTarget() {
  if (state.transferDestRoot !== state.root) return null;
  const dest = transferNormalizePath(state.transferDestPath);
  for (const p of state.selected) {
    const src = transferNormalizePath(p);
    if (!src) continue;
    if (dest === src || dest.startsWith(src + '/')) return src;
  }
  return null;
}

function renderTransferCrumbs() {
  const nav = $('#transfer-crumbs');
  if (!nav) return;
  nav.innerHTML = '';
  const rootId = state.transferDestRoot;
  const path = transferNormalizePath(state.transferDestPath);
  const parts = path ? path.split('/') : [];
  const addSep = () => {
    const sep = document.createElement('span');
    sep.className = 'transfer-crumb-sep';
    sep.textContent = '/';
    sep.setAttribute('aria-hidden', 'true');
    nav.appendChild(sep);
  };
  const addCrumb = (label, destPath, isCurrent) => {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'ghost transfer-crumb';
    btn.textContent = label;
    btn.title = label;
    if (isCurrent) {
      btn.setAttribute('aria-current', 'location');
    } else {
      btn.addEventListener('click', () => {
        state.transferDestPath = destPath;
        $('#transfer-error').hidden = true;
        loadTransferFolders();
      });
    }
    nav.appendChild(btn);
  };
  addCrumb(transferRootName(rootId), '', parts.length === 0);
  let acc = '';
  parts.forEach((part, i) => {
    acc = acc ? acc + '/' + part : part;
    addSep();
    addCrumb(part, acc, i === parts.length - 1);
  });
}

function renderTransferFolderList(dirs) {
  const box = $('#transfer-folders');
  box.innerHTML = '';
  const current = document.createElement('p');
  current.className = 'transfer-folder-current';
  current.setAttribute('role', 'option');
  current.setAttribute('aria-selected', 'true');
  current.innerHTML = '<span aria-hidden="true">✓</span><span class="transfer-folder-name">This folder</span>';
  box.appendChild(current);
  const destPath = transferNormalizePath(state.transferDestPath);
  if (destPath) {
    const up = document.createElement('button');
    up.type = 'button';
    up.className = 'ghost transfer-folder-btn';
    up.innerHTML = '<span aria-hidden="true">↑</span><span class="transfer-folder-name">Up</span>';
    up.addEventListener('click', () => {
      state.transferDestPath = transferParentPath(destPath);
      $('#transfer-error').hidden = true;
      loadTransferFolders();
    });
    box.appendChild(up);
  }
  if (!dirs.length) {
    const empty = document.createElement('p');
    empty.className = 'meta transfer-folders-empty';
    empty.textContent = destPath ? 'No subfolders in this folder.' : 'No folders in this location.';
    box.appendChild(empty);
    return;
  }
  for (const d of dirs) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'ghost transfer-folder-btn';
    btn.setAttribute('role', 'option');
    const name = document.createElement('span');
    name.className = 'transfer-folder-name';
    name.textContent = d.name;
    const icon = document.createElement('span');
    icon.setAttribute('aria-hidden', 'true');
    icon.textContent = '📁';
    btn.appendChild(icon);
    btn.appendChild(name);
    btn.title = d.name;
    btn.addEventListener('click', () => {
      state.transferDestPath = transferNormalizePath(d.path || (destPath ? destPath + '/' + d.name : d.name));
      $('#transfer-error').hidden = true;
      loadTransferFolders();
    });
    box.appendChild(btn);
  }
}

let transferListGen = 0;

async function loadTransferFolders() {
  const gen = ++transferListGen;
  const box = $('#transfer-folders');
  const hint = $('#transfer-dest-hint');
  const errEl = $('#transfer-error');
  hint.textContent = 'Destination: ' + transferDestLabel();
  renderTransferCrumbs();
  box.innerHTML = '<p class="meta transfer-folders-empty">Loading…</p>';
  try {
    const data = await api(`/api/list?root=${encodeURIComponent(state.transferDestRoot)}&path=${encodeURIComponent(state.transferDestPath || '')}`);
    if (gen !== transferListGen) return;
    const dirs = (data.entries || []).filter((e) => e.isDir).sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));
    renderTransferFolderList(dirs);
  } catch (err) {
    if (gen !== transferListGen) return;
    box.innerHTML = '<p class="meta transfer-folders-empty">Could not load folders.</p>';
    errEl.textContent = err.message;
    errEl.hidden = false;
  }
}

async function openTransferDialog(mode) {
  if (mode === 'move' && !state.canDelete) return;
  state.transferMode = mode;
  const dlg = $('#transfer-dialog');
  $('#transfer-title').textContent = mode === 'copy' ? 'Copy items' : 'Move items';
  $('#transfer-submit').textContent = mode === 'copy' ? 'Copy' : 'Move';
  const errEl = $('#transfer-error');
  errEl.hidden = true;
  errEl.textContent = '';
  try {
    const data = await api('/api/roots');
    if (data && Array.isArray(data.roots)) state.roots = data.roots;
    if (data && data.trashCount != null) state.trashCount = Number(data.trashCount) || 0;
  } catch { /* keep cached roots */ }
  const writable = (state.roots || []).filter((r) => r.perm === 'write');
  const sel = $('#transfer-root');
  sel.innerHTML = '';
  if (!writable.length) {
    state.transferDestRoot = '';
    state.transferDestPath = '';
    $('#transfer-crumbs').innerHTML = '';
    $('#transfer-dest-hint').textContent = '';
    $('#transfer-folders').innerHTML = '';
    errEl.textContent = 'No writable destination is available.';
    errEl.hidden = false;
    dlg.showModal();
    return;
  }
  const preferred = writable.some((r) => r.id === state.root) ? state.root : writable[0].id;
  for (const r of writable) {
    const opt = document.createElement('option');
    opt.value = r.id;
    opt.textContent = r.name;
    if (r.id === preferred) opt.selected = true;
    sel.appendChild(opt);
  }
  state.transferDestRoot = preferred;
  state.transferDestPath = preferred === state.root ? (state.path || '') : '';
  await loadTransferFolders();
  dlg.showModal();
}

$('#btn-bulk-move')?.addEventListener('click', () => openTransferDialog('move'));
$('#btn-bulk-copy')?.addEventListener('click', () => openTransferDialog('copy'));

$('#transfer-root')?.addEventListener('change', () => {
  state.transferDestRoot = $('#transfer-root').value;
  state.transferDestPath = '';
  $('#transfer-error').hidden = true;
  loadTransferFolders();
});

$('#transfer-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const paths = [...state.selected];
  if (!paths.length) return;
  if (state.transferMode === 'move' && !state.canDelete) return;
  const destRoot = state.transferDestRoot || $('#transfer-root').value;
  const destPath = transferNormalizePath(state.transferDestPath);
  const errEl = $('#transfer-error');
  errEl.hidden = true;
  const selfTarget = transferSelfTarget();
  if (selfTarget) {
    const verb = state.transferMode === 'copy' ? 'copy' : 'move';
    errEl.textContent = 'Cannot ' + verb + ' a folder into itself or one of its subfolders.';
    errEl.hidden = false;
    return;
  }
  const endpoint = state.transferMode === 'copy' ? '/api/batch/copy' : '/api/batch/move';
  try {
    const res = await api(endpoint, {
      method: 'POST',
      body: JSON.stringify({ root: state.root, paths, destRoot, destPath }),
    });
    if (res.failed && res.failed.length) {
      errEl.textContent = res.failed.map((f) => {
        const msg = f.error === 'cannot move/copy into itself'
          ? 'cannot ' + (state.transferMode === 'copy' ? 'copy' : 'move') + ' a folder into itself or one of its subfolders'
          : f.error;
        return f.path + ': ' + msg;
      }).join('; ');
      errEl.hidden = false;
      return;
    }
    $('#transfer-dialog').close();
    invalidateTreeCache(state.root);
    invalidateTreeCache(destRoot);
    await loadRoots();
    await loadList();
    try { await refreshMe(); } catch { /* ignore */ }
  } catch (err) {
    errEl.textContent = err.message;
    errEl.hidden = false;
  }
});

function shareRootId() {
  return state.root || 'home';
}

function selectedShareUserIds() {
  return [...state.shareSelectedIds].filter((id) => id > 0);
}

function shareQueryValue() {
  const el = $('#share-user-filter');
  return el ? String(el.value || '').trim() : '';
}

function shareUserMatchesQuery(user, q) {
  if (!q) return true;
  const needle = q.toLowerCase();
  if (String(user.username || '').toLowerCase().includes(needle)) return true;
  if (user.displayName && String(user.displayName).toLowerCase().includes(needle)) return true;
  if (user.email && String(user.email).toLowerCase().includes(needle)) return true;
  return false;
}

function configureShareUserSearch(userCount) {
  const filter = $('#share-user-filter');
  const hint = $('#share-user-hint');
  // Keyword field is always available, including when the list is short.
  if (filter) filter.hidden = false;
  if (hint) hint.hidden = !(userCount > 5);
}

function visibleShareUsers() {
  const all = state.shareDirectory || [];
  const byId = new Map(all.map((u) => [Number(u.id), u]));
  const q = shareQueryValue();
  const seen = new Set();
  const out = [];
  const add = (u) => {
    if (!u) return;
    const id = Number(u.id);
    if (!id || seen.has(id)) return;
    seen.add(id);
    out.push(byId.get(id) || u);
  };
  if (q) {
    // Keep selected users visible even when they do not match the keyword.
    [...state.shareSelectedIds].forEach((id) => add(byId.get(id)));
    all.forEach((u) => { if (shareUserMatchesQuery(u, q)) add(u); });
    (state.shareQueryMatches || []).forEach((u) => add(byId.get(Number(u.id)) || u));
    return out;
  }
  if (all.length <= 5) {
    all.forEach(add);
    return out;
  }
  (state.shareFrequentIds || []).forEach((id) => add(byId.get(Number(id))));
  [...state.shareSelectedIds].forEach((id) => add(byId.get(id)));
  return out;
}

function renderShareUserList() {
  const box = $('#share-user-list');
  const empty = $('#share-user-empty');
  if (!box) return;
  const all = state.shareDirectory || [];
  configureShareUserSearch(all.length);
  const q = shareQueryValue();
  const list = visibleShareUsers();
  const frequent = new Set((state.shareFrequentIds || []).map((id) => Number(id)));
  box.innerHTML = list.map((u) => {
    const id = Number(u.id);
    const checked = state.shareSelectedIds.has(id) ? ' checked' : '';
    const badge = frequent.has(id) ? '<span class="share-user-badge">Frequent</span>' : '';
    return '<label class="share-user-row"><input type="checkbox" value="' + id + '"' + checked + ' />' +
      '<span class="share-user-name">' + escapeHtml(u.username) + '</span>' + badge + '</label>';
  }).join('');
  if (!empty) return;
  if (all.length === 0) {
    empty.textContent = 'No other users are available.';
    empty.hidden = false;
  } else if (list.length === 0) {
    empty.textContent = q ? 'No matching users.' : 'No other users are available.';
    empty.hidden = false;
  } else if (q && list.every((u) => state.shareSelectedIds.has(Number(u.id))) &&
      !all.some((u) => shareUserMatchesQuery(u, q)) &&
      !(state.shareQueryMatches || []).length) {
    empty.textContent = 'No other matching users.';
    empty.hidden = false;
  } else {
    empty.hidden = true;
  }
}

let shareSearchTimer = 0;
let shareSearchSeq = 0;

async function loadShareDirectory() {
  const data = await api('/api/directory');
  state.shareDirectory = data.users || [];
  state.shareFrequentIds = data.frequentUserIds || [];
  state.shareQueryMatches = null;
  renderShareUserList();
  const q = shareQueryValue();
  if (q) await searchShareDirectory(q);
}

async function searchShareDirectory(q) {
  const seq = ++shareSearchSeq;
  const needle = String(q || '').trim();
  if (!needle) {
    state.shareQueryMatches = null;
    if (seq === shareSearchSeq) renderShareUserList();
    return;
  }
  try {
    const data = await api('/api/directory?q=' + encodeURIComponent(needle));
    if (seq !== shareSearchSeq) return;
    state.shareQueryMatches = data.users || [];
    renderShareUserList();
  } catch (err) {
    if (seq !== shareSearchSeq) return;
    console.error(err);
  }
}

function onShareUserFilterInput() {
  state.shareQueryMatches = null;
  renderShareUserList();
  const q = shareQueryValue();
  clearTimeout(shareSearchTimer);
  if (!q) {
    shareSearchSeq += 1;
    return;
  }
  shareSearchTimer = setTimeout(() => { searchShareDirectory(q); }, 150);
}

function setShareLinkOptionsVisible() {
  const box = $('#share-link-options');
  const on = !!($('#share-public') && $('#share-public').checked);
  if (box) box.hidden = !on;
}

async function loadExistingShares(target) {
  const usersEl = $('#share-existing-users');
  const linksEl = $('#share-existing-links');
  const wrap = $('#share-existing');
  if (!usersEl || !linksEl || !wrap) return;
  usersEl.innerHTML = '';
  linksEl.innerHTML = '';
  let any = false;
  try {
    const data = await api('/api/item-shares?root=' + encodeURIComponent(shareRootId()) + '&path=' + encodeURIComponent(target.path));
    const shares = data.shares || [];
    for (const sh of shares) {
      any = true;
      const li = document.createElement('li');
      const nameSpan = document.createElement('span');
      nameSpan.className = 'meta share-existing-user';
      nameSpan.textContent = sh.username || ('user #' + sh.userId);
      const sel = document.createElement('select');
      sel.className = 'share-existing-perm';
      sel.setAttribute('aria-label', 'Access for ' + (sh.username || 'this user'));
      sel.innerHTML = '<option value="read">Read only</option><option value="write">Read/Write</option>';
      sel.value = sh.perm === 'write' ? 'write' : 'read';
      sel.addEventListener('change', async () => {
        const perm = sel.value === 'write' ? 'write' : 'read';
        sel.disabled = true;
        try {
          await api('/api/item-shares', {
            method: 'POST',
            body: JSON.stringify({
              root: shareRootId(),
              path: target.path,
              userIds: [sh.userId],
              perm,
            }),
          });
          await loadExistingShares(target);
        } catch (err) {
          alert(err.message || String(err));
          sel.value = sh.perm === 'write' ? 'write' : 'read';
          sel.disabled = false;
        }
      });
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'ghost danger';
      btn.textContent = 'Revoke';
      btn.addEventListener('click', async () => {
        if (!confirm('Revoke access for ' + (sh.username || 'this user') + '?')) return;
        await api('/api/item-shares/' + sh.id, { method: 'DELETE' });
        await loadExistingShares(target);
      });
      li.className = 'share-existing-user-row';
      li.appendChild(nameSpan);
      li.appendChild(sel);
      li.appendChild(btn);
      usersEl.appendChild(li);
    }
  } catch (err) {
    console.error(err);
  }
  try {
    const data = await api('/api/links');
    const links = (data.links || []).filter((l) => l.rootId === shareRootId() && l.path === target.path && !l.revoked);
    for (const link of links) {
      any = true;
      const li = document.createElement('li');
      li.className = 'share-existing-link';
      const url = location.origin + '/s/' + link.token;
      li.innerHTML =
        '<span class="meta share-link-label">Public link</span>' +
        '<code class="share-link-url" title="' + escapeHtml(url) + '">' + escapeHtml(url) + '</code>' +
        '<span class="share-link-actions"></span>';
      const copy = document.createElement('button');
      copy.type = 'button';
      copy.className = 'ghost';
      copy.textContent = 'Copy';
      copy.addEventListener('click', async () => {
        try { await navigator.clipboard.writeText(url); copy.textContent = 'Copied'; setTimeout(() => { copy.textContent = 'Copy'; }, 1500); }
        catch { /* ignore */ }
      });
      const del = document.createElement('button');
      del.type = 'button';
      del.className = 'ghost danger';
      del.textContent = 'Delete';
      del.addEventListener('click', async () => {
        if (!confirm('Delete this public link?')) return;
        await api('/api/links/' + link.id, { method: 'DELETE' });
        await loadExistingShares(target);
      });
      const actions = li.querySelector('.share-link-actions');
      actions.appendChild(copy);
      actions.appendChild(del);
      linksEl.appendChild(li);
    }
  } catch (err) {
    console.error(err);
  }
  wrap.hidden = !any;
}

function openShareDialog(target) {
  state.shareTarget = target;
  state.shareCreating = false;
  state.shareDirectory = state.shareDirectory || [];
  $('#share-target').textContent = (target.isDir ? 'Folder: ' : 'File: ') + (target.name || target.path);
  const label = $('#share-label');
  if (label) label.value = target.name || '';
  const exp = $('#share-expires');
  if (exp) exp.value = '';
  const pw = $('#share-password');
  if (pw) pw.value = '';
  const pub = $('#share-public');
  if (pub) pub.checked = false;
  setShareLinkOptionsVisible();
  const permWrap = $('#share-perm-wrap');
  if (permWrap) permWrap.hidden = false;
  const perm = $('#share-perm');
  if (perm) perm.value = 'read';
  state.shareSelectedIds = new Set();
  state.shareQueryMatches = null;
  const filter = $('#share-user-filter');
  if (filter) filter.value = '';
  const errEl = $('#share-error');
  if (errEl) {
    errEl.hidden = true;
    errEl.textContent = '';
  }
  $('#share-result').hidden = true;
  $('#share-form').hidden = false;
  const existing = $('#share-existing');
  if (existing) existing.hidden = true;
  const submit = $('#btn-create-share');
  if (submit) {
    submit.disabled = false;
    submit.textContent = 'Share';
  }
  const copyBtn = $('#btn-copy-share-url');
  if (copyBtn) {
    copyBtn.textContent = 'Copy URL';
    copyBtn.hidden = true;
  }
  const urlInp = $('#share-url');
  if (urlInp) {
    urlInp.value = '';
    urlInp.hidden = true;
  }
  renderShareUserList();
  $('#share-dialog').showModal();
  loadShareDirectory().catch((err) => {
    const el = $('#share-error');
    if (el) { el.textContent = err.message || 'Could not load users'; el.hidden = false; }
  });
  loadExistingShares(target).catch((err) => console.error(err));
}

$('#btn-bulk-share')?.addEventListener('click', () => {
  const items = selectedEntries();
  if (items.length !== 1) return;
  const e = items[0];
  openShareDialog({ path: e.path, isDir: e.isDir, name: e.name });
});

function expiresToRFC3339(v) {
  if (!v) return '';
  const days = v === '1d' ? 1 : v === '7d' ? 7 : v === '30d' ? 30 : 0;
  if (!days) return '';
  return new Date(Date.now() + days * 86400000).toISOString();
}

$('#share-public')?.addEventListener('change', setShareLinkOptionsVisible);
$('#share-user-filter')?.addEventListener('input', onShareUserFilterInput);
const shareUserList = $('#share-user-list');
if (shareUserList && !shareUserList.dataset.shareBound) {
  shareUserList.dataset.shareBound = '1';
  shareUserList.addEventListener('change', (e) => {
    const inp = e.target;
    if (!inp || inp.type !== 'checkbox') return;
    const id = Number(inp.value);
    if (!(id > 0)) return;
    if (inp.checked) state.shareSelectedIds.add(id);
    else state.shareSelectedIds.delete(id);
  });
}

$('#share-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const t = state.shareTarget;
  if (!t || state.shareCreating) return;
  const errEl = $('#share-error');
  const submit = $('#btn-create-share');
  if (errEl) { errEl.hidden = true; errEl.textContent = ''; }
  const userIds = selectedShareUserIds();
  const wantLink = !!($('#share-public') && $('#share-public').checked);
  if (!userIds.length && !wantLink) {
    if (errEl) {
      errEl.textContent = 'Choose at least one user, or create a public link.';
      errEl.hidden = false;
    }
    return;
  }
  state.shareCreating = true;
  if (submit) {
    submit.disabled = true;
    submit.textContent = 'Sharing…';
  }
  const notes = [];
  let linkUrl = '';
  try {
    if (userIds.length) {
      const res = await api('/api/item-shares', {
        method: 'POST',
        body: JSON.stringify({
          root: shareRootId(),
          path: t.path,
          userIds,
          perm: (($('#share-perm') && $('#share-perm').value) === 'write') ? 'write' : 'read',
        }),
      });
      const n = (res.shares || []).length;
      notes.push('Shared with ' + n + ' user' + (n === 1 ? '' : 's') + '. Email is sent when SMTP is configured and the user has an address.');
    }
    if (wantLink) {
      const res = await api('/api/links', {
        method: 'POST',
        body: JSON.stringify({
          root: shareRootId(),
          path: t.path,
          label: ($('#share-label') && $('#share-label').value.trim()) || '',
          password: ($('#share-password') && $('#share-password').value) || '',
          expiresAt: expiresToRFC3339($('#share-expires') && $('#share-expires').value),
        }),
      });
      linkUrl = location.origin + (res.url || ('/s/' + res.link.token));
      notes.push('Public link created. Anyone with the URL can download or browse (read-only).');
    }
    const msg = $('#share-success-msg');
    if (msg) msg.textContent = notes.join(' ');
    const title = $('#share-success-title');
    if (title) title.textContent = 'Shared';
    const urlInp = $('#share-url');
    const copyBtn = $('#btn-copy-share-url');
    if (linkUrl && urlInp) {
      urlInp.value = linkUrl;
      urlInp.hidden = false;
      if (copyBtn) { copyBtn.hidden = false; copyBtn.textContent = 'Copy URL'; }
    } else {
      if (urlInp) { urlInp.value = ''; urlInp.hidden = true; }
      if (copyBtn) copyBtn.hidden = true;
    }
    $('#share-form').hidden = true;
    const existing = $('#share-existing');
    if (existing) existing.hidden = true;
    $('#share-result').hidden = false;
    try { await loadRoots(); } catch { /* ignore */ }
  } catch (err) {
    if (errEl) {
      errEl.textContent = err.message;
      errEl.hidden = false;
    }
    if (submit) {
      submit.disabled = false;
      submit.textContent = 'Share';
    }
  } finally {
    state.shareCreating = false;
  }
});

$('#btn-copy-share-url')?.addEventListener('click', async () => {
  const inp = $('#share-url');
  const btn = $('#btn-copy-share-url');
  try {
    await navigator.clipboard.writeText(inp.value);
    if (btn) {
      btn.textContent = 'Copied';
      setTimeout(() => { btn.textContent = 'Copy URL'; }, 1500);
    }
  } catch {
    inp.select();
    document.execCommand('copy');
  }
});

$('#btn-share-done')?.addEventListener('click', () => {
  const dlg = $('#share-dialog');
  if (dlg && typeof dlg.close === 'function') dlg.close();
});

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function selectedLinkIds() {
  return [...document.querySelectorAll('#link-list .link-check:checked')]
    .map((el) => Number(el.value))
    .filter((id) => id > 0);
}

function updateLinksSelection() {
  const boxes = [...document.querySelectorAll('#link-list .link-check')];
  const checked = boxes.filter((b) => b.checked);
  const btn = $('#btn-links-bulk-delete');
  if (btn) btn.disabled = checked.length === 0;
  const all = $('#link-select-all');
  if (all) {
    all.disabled = boxes.length === 0;
    all.indeterminate = checked.length > 0 && checked.length < boxes.length;
    all.checked = boxes.length > 0 && checked.length === boxes.length;
  }
}

async function deleteSelectedLinks() {
  const ids = selectedLinkIds();
  if (!ids.length) return;
  const n = ids.length;
  if (!confirm('Delete ' + n + ' link(s)? This cannot be undone.')) return;
  const btn = $('#btn-links-bulk-delete');
  const msg = $('#links-bulk-msg');
  if (btn) btn.disabled = true;
  if (msg) { msg.hidden = true; msg.textContent = ''; msg.classList.remove('error'); }
  let ok = 0;
  let failed = 0;
  const errors = [];
  for (const id of ids) {
    try {
      await api('/api/links/' + id, { method: 'DELETE' });
      ok++;
    } catch (err) {
      failed++;
      errors.push('#' + id + ': ' + (err.message || 'error'));
    }
  }
  await refreshLinks();
  if (msg) {
    if (failed) {
      msg.textContent = 'Deleted ' + ok + ' link(s); ' + failed + ' failed.' + (errors.length ? ' ' + errors.join('; ') : '');
      msg.classList.add('error');
    } else {
      msg.textContent = 'Deleted ' + ok + ' link(s).';
    }
    msg.hidden = false;
  }
}

async function refreshLinks() {
  const data = await api('/api/links');
  const body = $('#link-list');
  if (!body) return;
  body.innerHTML = '';
  const links = data.links || [];
  if (!links.length) {
    const tr = document.createElement('tr');
    tr.innerHTML = '<td colspan="6" class="muted empty-row">No link shares yet. Use the share button on a file or folder.</td>';
    body.appendChild(tr);
    updateLinksSelection();
    return;
  }
  for (const l of links) {
    const tr = document.createElement('tr');
    const status = [];
    if (l.revoked) status.push('revoked');
    else if (l.expiresAt && new Date(l.expiresAt) < new Date()) status.push('expired');
    else status.push('active');
    if (l.hasPassword) status.push('password');
    if (l.isDir) status.push('folder');
    else status.push('file');
    const url = location.origin + '/s/' + l.token;
    const label = l.label || (l.isDir ? 'Folder' : 'File');
    tr.innerHTML =
      '<td class="check-cell"><input type="checkbox" class="link-check" value="' + l.id + '" aria-label="Select ' + escapeHtml(label) + '" /></td>' +
      '<td class="link-label-cell" title="' + escapeHtml(label) + '">' + escapeHtml(label) + '</td>' +
      '<td class="path-cell link-target-cell" title="' + escapeHtml(l.rootId + ':' + l.path) + '"><code>' + escapeHtml(l.rootId + ':' + l.path) + '</code></td>' +
      '<td class="link-url-cell" title="' + escapeHtml(url) + '"><a href="' + url + '" target="_blank" rel="noopener">' + escapeHtml(url) + '</a></td>' +
      '<td class="status-cell">' + status.map((s) => '<span class="badge">' + s + '</span>').join(' ') + '</td>' +
      '<td class="row-actions"></td>';
    const btns = tr.querySelector('.row-actions');
    const copy = document.createElement('button');
    copy.type = 'button';
    copy.className = 'ghost';
    copy.textContent = 'Copy';
    copy.onclick = async () => {
      try { await navigator.clipboard.writeText(url); copy.textContent = 'OK'; setTimeout(() => { copy.textContent = 'Copy'; }, 1000); } catch { /* ignore */ }
    };
    const rev = document.createElement('button');
    rev.type = 'button';
    rev.className = 'ghost danger';
    rev.textContent = 'Revoke';
    rev.disabled = !!l.revoked;
    rev.onclick = async () => {
      if (!confirm('Revoke and delete this link?')) return;
      await api('/api/links/' + l.id, { method: 'DELETE' });
      await refreshLinks();
    };
    btns.appendChild(copy);
    btns.appendChild(rev);
    body.appendChild(tr);
  }
  updateLinksSelection();
}

const linkSelectAll = $('#link-select-all');
if (linkSelectAll) {
  linkSelectAll.addEventListener('change', () => {
    const on = linkSelectAll.checked;
    document.querySelectorAll('#link-list .link-check').forEach((b) => { b.checked = on; });
    updateLinksSelection();
  });
}
const linkListBody = $('#link-list');
if (linkListBody) {
  linkListBody.addEventListener('change', (e) => {
    if (e.target && e.target.classList.contains('link-check')) updateLinksSelection();
  });
}
const btnLinksBulkDelete = $('#btn-links-bulk-delete');
if (btnLinksBulkDelete) {
  btnLinksBulkDelete.addEventListener('click', () => { deleteSelectedLinks(); });
}

function publicRawURL(path) {
  const token = state.publicToken;
  const q = path ? ('?path=' + encodeURIComponent(path)) : '';
  return '/api/public/links/' + token + '/raw' + q;
}

function publicDownloadURL(path) {
  const token = state.publicToken;
  const q = path ? ('?path=' + encodeURIComponent(path)) : '';
  return '/api/public/links/' + token + '/download' + q;
}

function clearPublicInlinePreview() {
  const box = $('#public-single-preview');
  if (!box) return;
  box.querySelectorAll('video, audio').forEach((el) => {
    try { el.pause(); el.removeAttribute('src'); el.src = ''; el.load(); } catch { /* ignore */ }
  });
  revokePdfObjectUrls(box);
  box.classList.remove('pdf-mode');
  box.innerHTML = '';
  box.hidden = true;
}

function openPublicPreview(e) {
  // Modal preview using public raw URL
  stopPreviewMedia();
  const body = $('#preview-body');
  const url = publicRawURL(e.path || '');
  if (e.kind === 'image') {
    const img = document.createElement('img');
    img.src = url;
    img.alt = e.name || '';
    body.appendChild(img);
  } else if (e.kind === 'pdf') {
    body.classList.add('pdf-mode');
    appendPdfFrame(body, url, e.name || 'PDF');
  } else {
    const v = document.createElement('video');
    v.controls = true;
    v.autoplay = true;
    v.src = url;
    body.appendChild(v);
  }
  const dlg = $('#preview');
  if (!dlg.dataset.closeBound) {
    dlg.dataset.closeBound = '1';
    dlg.addEventListener('close', stopPreviewMedia);
    dlg.addEventListener('cancel', stopPreviewMedia);
  }
  dlg.showModal();
}

function renderPublicInlineMedia(e) {
  const box = $('#public-single-preview');
  clearPublicInlinePreview();
  if (!box || !e || e.isDir) return;
  if (!isPreviewable(e)) return;
  const url = publicRawURL(e.path || '');
  if (e.kind === 'image') {
    const img = document.createElement('img');
    img.src = url;
    img.alt = e.name || '';
    img.style.cursor = 'zoom-in';
    img.onclick = () => openPublicPreview(e);
    box.appendChild(img);
  } else if (e.kind === 'pdf') {
    box.classList.add('pdf-mode');
    appendPdfFrame(box, url, e.name || 'PDF');
  } else {
    const v = document.createElement('video');
    v.controls = true;
    v.src = url;
    box.appendChild(v);
  }
  const actions = document.createElement('div');
  actions.className = 'public-actions';
  const dl = document.createElement('a');
  dl.href = publicDownloadURL(e.path || '');
  dl.textContent = 'Download';
  dl.className = 'ghost';
  actions.appendChild(dl);
  if (e.kind === 'image' || e.kind === 'pdf') {
    const open = document.createElement('button');
    open.type = 'button';
    open.className = 'ghost';
    open.textContent = 'Open larger';
    open.onclick = () => openPublicPreview(e);
    actions.appendChild(open);
  }
  box.appendChild(actions);
  box.hidden = false;
}

async function loadPublicShare() {
  const token = state.publicToken;
  const errEl = $('#public-share-error');
  errEl.hidden = true;
  clearPublicInlinePreview();
  try {
    const meta = await api('/api/public/links/' + token + '/meta');
    state.publicMeta = meta;
    $('#public-share-label').textContent = meta.label || meta.pathName || 'Shared item';
    // Only show password UI when the link actually has a password AND unlock is still needed.
    const hasPassword = !!meta.hasPassword;
    const needPassword = hasPassword && !!meta.needPassword;
    if (needPassword) {
      $('#public-share-unlock').hidden = false;
      $('#public-share-browser').hidden = true;
      return;
    }
    $('#public-share-unlock').hidden = true;
    $('#public-share-browser').hidden = false;
    await loadPublicList();
  } catch (err) {
    $('#public-share-label').textContent = 'Link unavailable';
    errEl.textContent = err.message;
    errEl.hidden = false;
    $('#public-share-unlock').hidden = true;
    $('#public-share-browser').hidden = true;
  }
}

async function loadPublicList() {
  const token = state.publicToken;
  const path = state.publicPath || '';
  const data = await api('/api/public/links/' + token + '/list?path=' + encodeURIComponent(path));
  const crumbs = $('#public-breadcrumbs');
  crumbs.innerHTML = '';
  const rootBtn = document.createElement('button');
  rootBtn.type = 'button';
  rootBtn.className = 'ghost';
  rootBtn.textContent = 'Shared';
  rootBtn.onclick = async () => { state.publicPath = ''; await loadPublicList(); };
  crumbs.appendChild(rootBtn);
  if (path) {
    const parts = path.split('/');
    let acc = '';
    for (const part of parts) {
      acc = acc ? acc + '/' + part : part;
      const sep = document.createElement('span');
      sep.textContent = ' / ';
      crumbs.appendChild(sep);
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'ghost';
      b.textContent = part;
      const capture = acc;
      b.onclick = async () => { state.publicPath = capture; await loadPublicList(); };
      crumbs.appendChild(b);
    }
  }
  const ul = $('#public-file-list');
  ul.innerHTML = '';
  clearPublicInlinePreview();
  const entries = data.entries || [];
  // Single-file share (or one media file at this path): show inline preview.
  if (!data.isDir && entries.length === 1 && !entries[0].isDir) {
    const e = entries[0];
    if (isPreviewable(e)) {
      renderPublicInlineMedia(e);
      ul.hidden = true;
      return;
    }
  }
  ul.hidden = false;
  for (const e of entries) {
    const li = document.createElement('li');
    const icon = entryIcon(e);
    const name = document.createElement('button');
    name.type = 'button';
    name.className = 'ghost name-btn';
    name.textContent = icon + ' ' + e.name;
    name.onclick = async () => {
      if (e.isDir) {
        state.publicPath = e.path;
        await loadPublicList();
      } else if (isPreviewable(e)) {
        openPublicPreview(e);
      } else {
        window.location = publicDownloadURL(e.path);
      }
    };
    li.appendChild(name);
    if (!e.isDir) {
      const actions = document.createElement('span');
      actions.style.display = 'flex';
      actions.style.gap = '.35rem';
      if (isPreviewable(e)) {
        const prev = document.createElement('button');
        prev.type = 'button';
        prev.className = 'ghost';
        prev.textContent = e.kind === 'video' ? 'Play' : 'Preview';
        prev.onclick = () => openPublicPreview(e);
        actions.appendChild(prev);
      }
      const dl = document.createElement('a');
      dl.href = publicDownloadURL(e.path);
      dl.textContent = 'Download';
      dl.className = 'ghost';
      actions.appendChild(dl);
      li.appendChild(actions);
    }
    ul.appendChild(li);
  }
  if (!entries.length) {
    const li = document.createElement('li');
    li.className = 'muted';
    li.textContent = 'Empty';
    ul.appendChild(li);
  }
}

$('#public-share-unlock')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const errEl = $('#public-share-error');
  errEl.hidden = true;
  const fd = new FormData(e.target);
  try {
    await api('/api/public/links/' + state.publicToken + '/unlock', {
      method: 'POST',
      body: JSON.stringify({ password: fd.get('password') }),
    });
    await loadPublicShare();
  } catch (err) {
    errEl.textContent = err.message;
    errEl.hidden = false;
  }
});


async function initRegisterPage() {
  const errEl = $('#signup-error');
  const msgEl = $('#signup-msg');
  if (errEl) errEl.hidden = true;
  if (msgEl) msgEl.hidden = true;
  const data = await refreshAuthFeatures();
  if (!data || !data.openRegistration) {
    if (errEl) {
      errEl.textContent = 'Open registration is disabled.';
      errEl.hidden = false;
    }
    const form = $('#signup-form');
    if (form) form.hidden = true;
  }
}

async function initForgotPage() {
  const errEl = $('#forgot-error');
  const msgEl = $('#forgot-msg');
  if (errEl) errEl.hidden = true;
  if (msgEl) msgEl.hidden = true;
  const data = await refreshAuthFeatures();
  if (!data || !data.forgotPassword) {
    if (errEl) {
      errEl.textContent = 'Password reset is not available (SMTP not configured).';
      errEl.hidden = false;
    }
    const form = $('#forgot-form');
    if (form) form.hidden = true;
  }
}

$('#signup-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const errEl = $('#signup-error');
  const msgEl = $('#signup-msg');
  errEl.hidden = true;
  msgEl.hidden = true;
  const password = fd.get('password') || '';
  const confirm = fd.get('confirm') || '';
  if (password !== confirm) {
    errEl.textContent = 'Passwords do not match';
    errEl.hidden = false;
    return;
  }
  try {
    const data = await api('/api/register', {
      method: 'POST',
      body: JSON.stringify({
        username: fd.get('username'),
        email: fd.get('email'),
        password,
      }),
    });
    const user = data.username || fd.get('username') || '';
    if (data.pending) {
      msgEl.textContent = data.message || 'Your account is waiting for admin approval. You cannot sign in until an administrator approves it.';
      msgEl.hidden = false;
      e.target.hidden = true;
      const intro = $('#signup-intro');
      if (intro) intro.hidden = true;
    } else {
      msgEl.textContent = data.message || 'Account created. You can sign in now.';
      msgEl.hidden = false;
      setTimeout(() => {
        location.href = user ? ('/?registered=' + encodeURIComponent(user)) : '/';
      }, 800);
    }
  } catch (err) {
    errEl.textContent = err.message;
    errEl.hidden = false;
  }
});

$('#forgot-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const errEl = $('#forgot-error');
  const msgEl = $('#forgot-msg');
  errEl.hidden = true;
  msgEl.hidden = true;
  try {
    const data = await api('/api/forgot-password', {
      method: 'POST',
      body: JSON.stringify({ usernameOrEmail: fd.get('usernameOrEmail') }),
    });
    msgEl.textContent = data.message || 'If an account exists, a reset link has been sent.';
    msgEl.hidden = false;
  } catch (err) {
    errEl.textContent = err.message;
    errEl.hidden = false;
  }
});

async function initResetPage(token) {
  const errEl = $('#reset-error');
  const msgEl = $('#reset-msg');
  errEl.hidden = true;
  msgEl.hidden = true;
  state.resetToken = token;
  if (!token) {
    errEl.textContent = 'Missing reset token';
    errEl.hidden = false;
    $('#reset-form').hidden = true;
    return;
  }
  try {
    const data = await api('/api/reset-password/meta?token=' + encodeURIComponent(token));
    $('#reset-user').textContent = 'Account: ' + (data.username || '');
    $('#reset-policy-hint').textContent = 'Requirements: ' + (data.passwordPolicyHint || 'min 4 characters');
  } catch (err) {
    errEl.textContent = err.message;
    errEl.hidden = false;
    $('#reset-form').hidden = true;
  }
}

$('#reset-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const errEl = $('#reset-error');
  const msgEl = $('#reset-msg');
  errEl.hidden = true;
  msgEl.hidden = true;
  if (fd.get('newPassword') !== fd.get('confirmPassword')) {
    errEl.textContent = 'Passwords do not match';
    errEl.hidden = false;
    return;
  }
  try {
    const data = await api('/api/reset-password', {
      method: 'POST',
      body: JSON.stringify({ token: state.resetToken, newPassword: fd.get('newPassword') }),
    });
    msgEl.textContent = data.message || 'Password updated.';
    msgEl.hidden = false;
    e.target.hidden = true;
    setTimeout(() => { location.href = '/'; }, 1500);
  } catch (err) {
    errEl.textContent = err.message;
    errEl.hidden = false;
  }
});

$('#smtp-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const msg = $('#smtp-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  const body = {
    smtpEnabled: !!e.target.smtpEnabled.checked,
    smtpHost: fd.get('smtpHost') || '',
    smtpPort: Number(fd.get('smtpPort') || 587),
    smtpUsername: fd.get('smtpUsername') || '',
    smtpFrom: fd.get('smtpFrom') || '',
  };
  const pw = fd.get('smtpPassword');
  if (pw) body.smtpPassword = pw;
  try {
    const data = await api('/api/admin/settings', { method: 'PUT', body: JSON.stringify(body) });
    msg.textContent = 'SMTP settings saved';
    msg.hidden = false;
    // reuse loader
    const smtp = data.smtp || {};
    e.target.smtpEnabled.checked = !!smtp.enabled;
    e.target.smtpHost.value = smtp.host || '';
    e.target.smtpPort.value = smtp.port || 587;
    e.target.smtpUsername.value = smtp.username || '';
    e.target.smtpPassword.value = '';
    e.target.smtpFrom.value = smtp.from || '';
    const setNote = $('#smtp-password-set');
    if (setNote) {
      setNote.textContent = smtp.passwordSet ? 'A password is saved (leave field blank to keep it).' : 'No password saved yet.';
      setNote.hidden = false;
    }
    const ready = $('#smtp-ready');
    if (ready) ready.textContent = smtp.ready ? 'SMTP ready for forgot-password emails.' : 'SMTP not ready (need enable + host + from).';
  } catch (err) {
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});

$('#btn-smtp-test')?.addEventListener('click', async () => {
  const msg = $('#smtp-msg');
  msg.hidden = true;
  msg.classList.remove('error');
  const to = prompt('Send test email to (blank = your account email):', state.user?.email || '') || '';
  try {
    const data = await api('/api/admin/smtp/test', {
      method: 'POST',
      body: JSON.stringify({ to }),
    });
    msg.textContent = data.message || 'Test email sent';
    msg.hidden = false;
  } catch (err) {
    msg.textContent = err.message;
    msg.classList.add('error');
    msg.hidden = false;
  }
});


bootstrap();


(function bindFileSearchAndTrash() {
  const input = $('#file-search');
  let timer = 0;
  if (input) {
    input.addEventListener('input', () => {
      clearTimeout(timer);
      const v = input.value;
      timer = setTimeout(() => {
        runFileSearch(v, false).catch((err) => console.error(err));
      }, 250);
    });
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        input.value = '';
        state.searchQuery = '';
        loadList().catch((err) => console.error(err));
      }
    });
  }
  $('#btn-trash-empty')?.addEventListener('click', async () => {
    if (!confirm('Permanently delete all items in your recycle bin? This cannot be undone.')) return;
    await api('/api/trash/purge', { method: 'POST', body: JSON.stringify({ empty: true }) });
    await loadRoots();
    await loadList();
  });
  $('#btn-trash-restore')?.addEventListener('click', async () => {
    const ids = [...state.selected];
    if (!ids.length) {
      alert('Select items to restore.');
      return;
    }
    for (const id of ids) {
      await api('/api/trash/restore', { method: 'POST', body: JSON.stringify({ id }) });
    }
    state.selected.clear();
    await loadRoots();
    await loadList();
  });
})();

const auditState = { offset: 0, limit: 50, total: 0 };

function fmtSessionTime(v) {
  if (!v) return '—';
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleString();
}

function shortUserAgent(ua) {
  const s = String(ua || '').trim();
  if (!s) return 'Unknown device';
  if (s.length <= 96) return s;
  return s.slice(0, 93) + '…';
}

async function refreshMySessions() {
  const tbody = $('#session-list');
  const msg = $('#sessions-msg');
  if (!tbody) return;
  let data;
  try {
    data = await api('/api/me/sessions');
  } catch (err) {
    tbody.innerHTML = '<tr><td colspan="5" class="error">Could not load sessions: ' + escapeHtml(err.message || 'error') + '</td></tr>';
    throw err;
  }
  const list = data.sessions || [];
  if (!list.length) {
    tbody.innerHTML = '<tr><td colspan="5" class="muted">No active sessions.</td></tr>';
    return;
  }
  tbody.innerHTML = list.map((sess) => {
    const current = sess.current ? ' <span class="badge current">this device</span>' : '';
    const revoke = sess.current
      ? '<span class="muted">Current</span>'
      : `<button type="button" class="ghost danger btn-revoke-session" data-id="${sess.id}">Revoke</button>`;
    return '<tr>' +
      '<td>' + escapeHtml(shortUserAgent(sess.userAgent)) + current + '</td>' +
      '<td><code>' + escapeHtml(sess.ip || '—') + '</code></td>' +
      '<td>' + escapeHtml(fmtSessionTime(sess.createdAt)) + '</td>' +
      '<td>' + escapeHtml(fmtSessionTime(sess.lastSeenAt)) + '</td>' +
      '<td>' + revoke + '</td>' +
      '</tr>';
  }).join('');
  tbody.querySelectorAll('.btn-revoke-session').forEach((btn) => {
    btn.addEventListener('click', async () => {
      if (!confirm('Revoke this session? That device will be signed out.')) return;
      try {
        await api('/api/me/sessions/' + btn.dataset.id, { method: 'DELETE' });
        if (msg) { msg.hidden = false; msg.textContent = 'Session revoked'; }
        await refreshMySessions();
      } catch (err) {
        if (msg) { msg.hidden = false; msg.textContent = err.message; }
      }
    });
  });
}

$('#btn-revoke-other-sessions')?.addEventListener('click', async () => {
  if (!confirm('Sign out all other devices? This device will stay signed in.')) return;
  const msg = $('#sessions-msg');
  try {
    await api('/api/me/sessions/revoke-others', { method: 'POST' });
    if (msg) { msg.hidden = false; msg.textContent = 'Other sessions revoked'; }
    await refreshMySessions();
  } catch (err) {
    if (msg) { msg.hidden = false; msg.textContent = err.message; }
  }
});

function auditActionLabel(action) {
  switch (action) {
    case 'login_success': return 'Sign-in success';
    case 'login_failure': return 'Sign-in failure';
    case 'share_link_create': return 'Link share created';
    case 'share_link_revoke': return 'Link share revoked';
    case 'share_grant_set': return 'Share grant updated';
    case 'share_grant_remove': return 'Share grant removed';
    case 'item_share_grant': return 'File or folder shared with user';
    case 'item_share_revoke': return 'User share revoked';
    case 'password_reset_request': return 'Password reset requested';
    default: return action || '';
  }
}

async function refreshAudit(offset) {
  if (typeof offset === 'number') auditState.offset = Math.max(0, offset);
  let data;
  try {
    data = await api('/api/admin/audit?limit=' + auditState.limit + '&offset=' + auditState.offset);
  } catch (err) {
    const tbody = $('#audit-list');
    if (tbody) tbody.innerHTML = '<tr><td colspan="5" class="error">Could not load audit log: ' + escapeHtml(err.message || 'error') + '</td></tr>';
    throw err;
  }
  auditState.total = data.total || 0;
  auditState.limit = data.limit || auditState.limit;
  if (data.auditLogMax != null) applyAuditLogMaxForm(data.auditLogMax);
  const tbody = $('#audit-list');
  const events = data.events || [];
  if (tbody) {
    if (!events.length) {
      tbody.innerHTML = '<tr><td colspan="5" class="muted">No audit events yet.</td></tr>';
    } else {
      tbody.innerHTML = events.map((e) => {
        return '<tr>' +
          '<td>' + escapeHtml(fmtSessionTime(e.createdAt)) + '</td>' +
          '<td>' + escapeHtml(e.actorUsername || '—') + '</td>' +
          '<td><code>' + escapeHtml(e.ip || '—') + '</code></td>' +
          '<td>' + escapeHtml(auditActionLabel(e.action)) + '</td>' +
          '<td class="meta">' + escapeHtml(e.detail || '') + '</td>' +
          '</tr>';
      }).join('');
    }
  }
  const info = $('#audit-page-info');
  const from = auditState.total === 0 ? 0 : auditState.offset + 1;
  const to = Math.min(auditState.offset + events.length, auditState.total);
  if (info) info.textContent = auditState.total ? (`${from}–${to} of ${auditState.total}`) : '0 events';
  const prev = $('#btn-audit-prev');
  const next = $('#btn-audit-next');
  if (prev) prev.disabled = auditState.offset <= 0;
  if (next) next.disabled = auditState.offset + auditState.limit >= auditState.total;
}

$('#btn-audit-prev')?.addEventListener('click', async () => {
  try { await refreshAudit(auditState.offset - auditState.limit); } catch (err) { console.error(err); }
});
$('#btn-audit-next')?.addEventListener('click', async () => {
  try { await refreshAudit(auditState.offset + auditState.limit); } catch (err) { console.error(err); }
});

function applyAuditLogMaxForm(value) {
  const input = $('#audit-log-max');
  if (!input) return;
  const n = Number(value);
  input.value = Number.isFinite(n) && n >= 0 ? String(n) : '0';
}

$('#audit-log-max-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const msg = $('#audit-log-max-msg');
  if (msg) {
    msg.hidden = true;
    msg.classList.remove('error');
  }
  const raw = (e.target.auditLogMax.value || '').trim();
  const n = raw === '' ? 0 : Number(raw);
  if (!Number.isInteger(n) || n < 0 || n > 1000000) {
    if (msg) {
      msg.textContent = 'Enter a whole number from 0 to 1000000. 0 means no limit.';
      msg.classList.add('error');
      msg.hidden = false;
    }
    return;
  }
  try {
    const data = await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify({ auditLogMax: n }),
    });
    applyAuditLogMaxForm(data.auditLogMax);
    if (msg) {
      msg.textContent = Number(data.auditLogMax) ? ('Saved — keeping at most ' + data.auditLogMax + ' events') : 'Saved — no limit';
      msg.hidden = false;
    }
    try { await refreshAudit(0); } catch (err) { console.error(err); }
  } catch (err) {
    if (msg) {
      msg.textContent = err.message;
      msg.classList.add('error');
      msg.hidden = false;
    }
  }
});


