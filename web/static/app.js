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
  roots: [],
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
  /** pending share target { path, isDir, name } */
  shareTarget: null,
  /** true while POST /api/links is in flight */
  shareCreating: false,
  /** public link token when on /s/:token */
  publicToken: null,
  publicPath: '',
  publicMeta: null,
  /** password reset token */
  resetToken: null,
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
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
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
  if (view !== 'settings' && view !== 'main' && view !== 'public') stopPreviewMedia();
  if (view !== 'public') clearPublicInlinePreview();
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
    await enterApp();
  } catch {
    show('login');
    await refreshForgotLink();
    const registered = new URLSearchParams(location.search).get('registered');
    if (registered) {
      const loginUser = $('#login-form')?.username;
      if (loginUser) loginUser.value = registered;
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
  $('#whoami').textContent = state.user.username + (state.user.isAdmin ? ' (admin)' : '');
  renderSidebarQuota();
}

function treeKey(rootId, path) {
  return rootId + ':' + (path || '');
}

async function fetchTreeDirs(rootId, path) {
  const key = treeKey(rootId, path);
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

async function refreshAuthFeatures() {
  const forgot = $('#link-forgot');
  const signup = $('#link-signup');
  const rememberLabel = $('#remember-me-label');
  try {
    const data = await api('/api/auth/features');
    if (forgot) forgot.hidden = !data.forgotPassword;
    if (signup) signup.hidden = !data.openRegistration;
    if (rememberLabel) rememberLabel.hidden = !data.allowRememberMe;
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
  await api('/api/logout', { method: 'POST', body: '{}' });
  state.user = null;
  state.pendingToken = null;
  showLoginStep('password');
  show('login');
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
    if (tab === 'sessions' || tab === 'registration' || tab === 'security' || tab === 'email') {
      try { await loadSystemSettings(); } catch (err) { console.error(err); }
    }
    if (tab === 'links') {
      try { await refreshLinks(); } catch (err) { console.error(err); }
    }
  });
});

async function openSettings() {
  try { await refreshMe(); } catch { /* keep cached */ }
  const u = state.user;
  document.querySelectorAll('.settings-nav-item.admin-only').forEach((el) => {
    el.hidden = !u.isAdmin;
  });
  applySettingsGate();
  updatePasswordPolicyHints(u);
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
  acc.innerHTML = `
    <div><strong>${u.username}</strong> ${badges.join(' ')}</div>
    <div class="meta">Email: ${u.email ? `<code>${u.email}</code>` : '<span class="muted">not set (ask admin)</span>'}</div>
    <div class="meta">Home: <code>${u.homePath || ''}</code></div>
    <div class="meta">Storage: ${fmtSize(used)} / ${fmtQuota(quota)}${quota ? ` (${pct}%)` : ''}</div>
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
    msg.textContent = 'Password updated';
    msg.hidden = false;
    e.target.reset();
    updatePasswordPolicyHints(state.user);
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

async function loadRoots() {
  const data = await api('/api/roots');
  state.roots = data.roots;
  const ul = $('#root-list');
  ul.innerHTML = '';
  for (const r of data.roots) {
    const li = await renderTreeNode(r, '', r.name + (r.perm === 'read' ? ' (ro)' : ''), 0);
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
    try { await fetchTreeDirs(root.id, path); } catch { /* ignore */ }
    await loadRoots();
    await loadList();
  };

  row.appendChild(twisty);
  row.appendChild(b);
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
  add('root', '');
  let acc = '';
  for (const p of parts) {
    acc = acc ? acc + '/' + p : p;
    add(p, acc);
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

async function loadList() {
  const data = await api(`/api/list?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(state.path)}`);
  state.perm = data.perm;
  const canWrite = state.perm === 'write';
  $('#btn-mkdir').disabled = !canWrite;
  $('#file-input').disabled = !canWrite;
  renderBreadcrumbs();
  state.entries = data.entries || [];
  state.selected.clear();
  applyViewMode();
  applySortControls();
  renderEntries(sortEntries(state.entries), canWrite);
  updateBulkBar();
}

function applyViewMode() {
  const listBtn = $('#btn-view-list');
  const iconsBtn = $('#btn-view-icons');
  const table = $('#files-table');
  const grid = $('#files-grid');
  const isIcons = state.viewMode === 'icons';
  listBtn.classList.toggle('active', !isIcons);
  iconsBtn.classList.toggle('active', isIcons);
  listBtn.setAttribute('aria-pressed', !isIcons ? 'true' : 'false');
  iconsBtn.setAttribute('aria-pressed', isIcons ? 'true' : 'false');
  table.hidden = isIcons;
  grid.hidden = !isIcons;
}

function setViewMode(mode) {
  state.viewMode = mode === 'icons' ? 'icons' : 'list';
  try { localStorage.setItem('filehub-view', state.viewMode); } catch { /* ignore */ }
  applyViewMode();
  loadList().catch((err) => console.error(err));
}

$('#btn-view-list').addEventListener('click', () => setViewMode('list'));
$('#btn-view-icons').addEventListener('click', () => setViewMode('icons'));

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
  return '📄';
}

function isPreviewable(e) {
  return !e.isDir && (e.kind === 'image' || e.kind === 'video' || e.kind === 'pdf');
}

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
  if (e.isDir) {
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
  } else {
    window.open(`/api/download?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(e.path)}`, '_blank');
  }
}

function appendEntryActions(container, e, canWrite, compact) {
  if (!e.isDir) {
    const dl = document.createElement('button');
    dl.type = 'button';
    dl.textContent = '↓';
    dl.className = 'ghost';
    dl.title = 'Download';
    dl.setAttribute('aria-label', 'Download');
    dl.onclick = (ev) => {
      ev.stopPropagation();
      window.location = `/api/download?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(e.path)}`;
    };
    container.appendChild(dl);
  }
  const share = document.createElement('button');
  share.type = 'button';
  share.textContent = '🔗';
  share.className = 'ghost';
  share.title = 'Share link';
  share.setAttribute('aria-label', 'Share link');
  share.onclick = (ev) => {
    ev.stopPropagation();
    openShareDialog({ path: e.path, isDir: e.isDir, name: e.name });
  };
  container.appendChild(share);
  if (canWrite) {
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
    container.appendChild(ren);
    container.appendChild(del);
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
  const shareBtn = $('#btn-bulk-share');
  if (shareBtn) shareBtn.disabled = n !== 1;
  const canWrite = state.perm === 'write';
  ['btn-bulk-delete', 'btn-bulk-move'].forEach((id) => {
    const el = $('#' + id);
    if (el) el.disabled = !canWrite;
  });
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

function renderEntries(entries, canWrite) {
  const body = $('#file-body');
  const grid = $('#files-grid');
  body.innerHTML = '';
  grid.innerHTML = '';

  if (!entries.length) {
    if (state.viewMode === 'icons') {
      const empty = document.createElement('div');
      empty.className = 'files-empty';
      empty.textContent = canWrite ? 'Empty folder — drop files here to upload' : 'Empty folder';
      grid.appendChild(empty);
    } else {
      const tr = document.createElement('tr');
      tr.innerHTML = `<td colspan="6" class="empty-row muted">${canWrite ? 'Empty folder — drop files here to upload' : 'Empty folder'}</td>`;
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
        img.src = `/api/thumb?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(e.path)}`;
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
      name.textContent = e.name;

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
    nameBtn.textContent = e.name;
    nameBtn.title = e.name;
    nameBtn.onclick = () => openEntry(e);
    nameWrap.appendChild(nameBtn);
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

function openPreview(e) {
  stopPreviewMedia();
  const body = $('#preview-body');
  const url = `/api/raw?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(e.path)}`;
  if (e.kind === 'image') {
    const img = document.createElement('img');
    img.src = url;
    img.alt = e.name;
    body.appendChild(img);
  } else if (e.kind === 'pdf') {
    body.classList.add('pdf-mode');
    appendPdfFrame(body, url, e.name);
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

function setUploadItem(li, { loaded, total, status, error }) {
  const pct = total > 0 ? Math.min(100, Math.round((loaded / total) * 100)) : 0;
  const bar = li.querySelector('.bar > span');
  const meta = li.querySelector('.meta');
  bar.style.width = pct + '%';
  li.classList.remove('done', 'err');
  if (status === 'uploading') {
    meta.textContent = total > 0
      ? `${fmtSize(loaded)} / ${fmtSize(total)} (${pct}%)`
      : `${fmtSize(loaded)}…`;
  } else if (status === 'done') {
    li.classList.add('done');
    meta.textContent = total > 0 ? `Done · ${fmtSize(total)}` : 'Done';
    bar.style.width = '100%';
  } else if (status === 'error') {
    li.classList.add('err');
    meta.textContent = error || 'Upload failed';
  }
}

function uploadFileWithProgress(file, destPath, onProgress) {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    fd.append('root', state.root);
    fd.append('path', destPath || '');
    fd.append('file', file);
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
      else reject(new Error((data && data.error) || xhr.statusText || 'Upload failed'));
    };
    xhr.onerror = () => reject(new Error('Network error'));
    xhr.onabort = () => reject(new Error('Upload aborted'));
    xhr.send(fd);
  });
}

$('#btn-clear-uploads').addEventListener('click', () => {
  $('#upload-list').innerHTML = '';
  syncUploadPanelVisibility();
});

async function uploadFiles(files, destPath) {
  const listFiles = [...files].filter((f) => f && f.name);
  if (!listFiles.length) return;
  if (state.perm !== 'write') {
    alert('This folder is read-only');
    return;
  }
  const target = destPath == null ? state.path : destPath;
  ensureUploadPanel();
  const list = $('#upload-list');
  $('#btn-clear-uploads').hidden = true;
  const batchTotal = listFiles.length;
  let batchDone = 0;
  setUploadProgressLabel(batchDone, batchTotal);
  let anyErr = false;
  for (const f of listFiles) {
    const li = document.createElement('li');
    li.className = 'upload-item';
    li.innerHTML = `<div class="row"><span class="name"></span><span class="meta"></span></div><div class="bar"><span></span></div>`;
    const label = target ? `${f.name} → ${target}/` : f.name;
    li.querySelector('.name').textContent = label;
    li.querySelector('.name').title = label;
    list.appendChild(li);
    setUploadItem(li, { loaded: 0, total: f.size, status: 'uploading' });
    try {
      await uploadFileWithProgress(f, target, (loaded, total) => {
        setUploadItem(li, { loaded, total, status: 'uploading' });
      });
      setUploadItem(li, { loaded: f.size, total: f.size, status: 'done' });
    } catch (err) {
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

  // Prevent browser from opening dropped files outside the zone
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
      hint.textContent = 'Drop files to upload here';
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
      hint.textContent = 'Drop files to upload here';
    }
    const folderPath = findFolderDropPath(ev.target);
    clearFolderHighlights();
    const files = collectDroppedFiles(ev.dataTransfer);
    if (!files.length) return;
    await uploadFiles(files, folderPath != null ? folderPath : state.path);
  });
})();

$('#file-input').addEventListener('change', async (ev) => {
  const files = [...ev.target.files];
  ev.target.value = '';
  if (!files.length) return;
  await uploadFiles(files, state.path);
});

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

async function refreshAdmin() {
  const users = await api('/api/admin/users');
  const byId = {};
  for (const u of users.users) byId[u.id] = u.username;
  const ul = $('#user-list');
  ul.innerHTML = '';
  for (const u of users.users) {
    const tr = document.createElement('tr');
    const used = u.usedBytes || 0;
    const quota = u.quotaBytes || 0;
    const pct = quota > 0 ? Math.min(100, Math.round(used / quota * 100)) : 0;
    const badges = [];
    if (u.isAdmin) badges.push('<span class="badge admin">admin</span>');
    if (u.disabled) badges.push('<span class="badge disabled">disabled</span>');
    if (u.totpEnabled) badges.push('<span class="badge admin">2FA</span>');
    else if (u.totpRequired || u.force2faAll) badges.push('<span class="badge disabled">2FA req</span>');
    if (!badges.length) badges.push('<span class="badge">user</span>');
    const quotaLabel = quota
      ? `${fmtSize(used)} / ${fmtQuota(quota)} (${pct}%)`
      : `${fmtSize(used)} / unlimited`;
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
      <td class="row-actions"></td>
    `;
    const btns = tr.querySelector('.row-actions');
    const edit = document.createElement('button');
    edit.textContent = 'Edit';
    edit.className = 'ghost';
    edit.title = 'Edit user';
    edit.onclick = () => openUserEdit(u);
    btns.appendChild(edit);
    if (u.id !== state.user.id) {
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
    } else {
      // Keep Edit under Edit when this row has no Del (cannot delete self).
      const spacer = document.createElement('span');
      spacer.className = 'row-action-spacer';
      spacer.setAttribute('aria-hidden', 'true');
      btns.appendChild(spacer);
    }
    ul.appendChild(tr);
  }
  if (!users.users.length) {
    const tr = document.createElement('tr');
    tr.innerHTML = '<td colspan="5" class="empty-row muted">No users</td>';
    ul.appendChild(tr);
  }

  const shares = await api('/api/admin/shares');
  const sl = $('#share-list');
  sl.innerHTML = '';
  for (const s of shares.shares) {
    const tr = document.createElement('tr');
    const grants = (s.grants || []).map((g) => {
      const name = byId[g.userId] || ('user#' + g.userId);
      return `<span class="grant-chip">${name}:${g.perm}</span>`;
    }).join(' ') || '<span class="muted">no grants</span>';
    tr.innerHTML = `
      <td class="share-name-cell"><div class="user-name" title="${s.name}">${s.name}</div><div class="meta">#${s.id}</div></td>
      <td class="home-cell"><code title="${s.path}">${s.path}</code></td>
      <td class="grants-cell">${grants}</td>
      <td class="row-actions"></td>
    `;
    const btns = tr.querySelector('.row-actions');
    const grantBtn = document.createElement('button');
    grantBtn.textContent = 'Grant';
    grantBtn.className = 'ghost';
    grantBtn.title = 'Grant access';
    grantBtn.onclick = async () => {
      const userId = Number(prompt('User ID'));
      const perm = prompt('perm: read or write', 'read');
      if (!userId || !perm) return;
      await api(`/api/admin/shares/${s.id}/grants`, {
        method: 'POST',
        body: JSON.stringify({ userId, perm }),
      });
      await refreshAdmin();
    };
    const del = document.createElement('button');
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

$('#create-user').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  let quotaBytes = 0;
  try { quotaBytes = parseQuotaInput(fd.get('quota')); }
  catch (err) { alert(err.message); return; }
  await api('/api/admin/users', {
    method: 'POST',
    body: JSON.stringify({
      username: fd.get('username'),
      email: fd.get('email') || '',
      password: fd.get('password'),
      homePath: fd.get('homePath') || '',
      isAdmin: !!fd.get('isAdmin'),
      quotaBytes,
    }),
  });
  e.target.reset();
  await refreshAdmin();
});

$('#edit-user').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const errEl = $('#edit-user-error');
  errEl.hidden = true;
  let quotaBytes = 0;
  try { quotaBytes = parseQuotaInput(fd.get('quota')); }
  catch (err) { errEl.textContent = err.message; errEl.hidden = false; return; }
  try {
    await api('/api/admin/users/' + fd.get('id'), {
      method: 'PATCH',
      body: JSON.stringify({
        username: fd.get('username'),
        email: fd.get('email') || '',
        homePath: fd.get('homePath'),
        isAdmin: !!fd.get('isAdmin'),
        disabled: !!fd.get('disabled'),
        totpRequired: !!fd.get('totpRequired'),
        quotaBytes,
      }),
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
  if (!paths.length) return;
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

function openTransferDialog(mode) {
  state.transferMode = mode;
  const dlg = $('#transfer-dialog');
  $('#transfer-title').textContent = mode === 'copy' ? 'Copy items' : 'Move items';
  $('#transfer-submit').textContent = mode === 'copy' ? 'Copy' : 'Move';
  $('#transfer-error').hidden = true;
  const sel = $('#transfer-root');
  sel.innerHTML = '';
  for (const r of state.roots || []) {
    const opt = document.createElement('option');
    opt.value = r.id;
    opt.textContent = r.name + (r.perm === 'read' ? ' (read-only)' : '');
    opt.disabled = r.perm !== 'write';
    if (r.id === state.root) opt.selected = true;
    sel.appendChild(opt);
  }
  $('#transfer-path').value = state.path || '';
  dlg.showModal();
}

$('#btn-bulk-move')?.addEventListener('click', () => openTransferDialog('move'));
$('#btn-bulk-copy')?.addEventListener('click', () => openTransferDialog('copy'));

$('#transfer-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const paths = [...state.selected];
  if (!paths.length) return;
  const destRoot = $('#transfer-root').value;
  const destPath = $('#transfer-path').value.trim();
  const errEl = $('#transfer-error');
  errEl.hidden = true;
  const endpoint = state.transferMode === 'copy' ? '/api/batch/copy' : '/api/batch/move';
  try {
    const res = await api(endpoint, {
      method: 'POST',
      body: JSON.stringify({ root: state.root, paths, destRoot, destPath }),
    });
    if (res.failed && res.failed.length) {
      errEl.textContent = res.failed.map((f) => f.path + ': ' + f.error).join('; ');
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

function openShareDialog(target) {
  state.shareTarget = target;
  state.shareCreating = false;
  $('#share-target').textContent = (target.isDir ? 'Folder: ' : 'File: ') + (target.name || target.path);
  $('#share-label').value = target.name || '';
  $('#share-expires').value = '';
  $('#share-password').value = '';
  const errEl = $('#share-error');
  if (errEl) {
    errEl.hidden = true;
    errEl.textContent = '';
  }
  $('#share-result').hidden = true;
  $('#share-form').hidden = false;
  const submit = $('#btn-create-share');
  if (submit) {
    submit.disabled = false;
    submit.textContent = 'Create link';
  }
  const copyBtn = $('#btn-copy-share-url');
  if (copyBtn) copyBtn.textContent = 'Copy URL';
  $('#share-url').value = '';
  $('#share-dialog').showModal();
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

$('#share-form')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const t = state.shareTarget;
  if (!t || state.shareCreating) return;
  const errEl = $('#share-error');
  const submit = $('#btn-create-share');
  errEl.hidden = true;
  state.shareCreating = true;
  if (submit) {
    submit.disabled = true;
    submit.textContent = 'Creating…';
  }
  try {
    const res = await api('/api/links', {
      method: 'POST',
      body: JSON.stringify({
        root: state.root,
        path: t.path,
        label: $('#share-label').value.trim(),
        password: $('#share-password').value,
        expiresAt: expiresToRFC3339($('#share-expires').value),
      }),
    });
    const url = location.origin + (res.url || ('/s/' + res.link.token));
    $('#share-url').value = url;
    const copyBtn = $('#btn-copy-share-url');
    if (copyBtn) copyBtn.textContent = 'Copy URL';
    $('#share-form').hidden = true;
    $('#share-result').hidden = false;
  } catch (err) {
    errEl.textContent = err.message;
    errEl.hidden = false;
    if (submit) {
      submit.disabled = false;
      submit.textContent = 'Create link';
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

async function refreshLinks() {
  const data = await api('/api/links');
  const body = $('#link-list');
  if (!body) return;
  body.innerHTML = '';
  const links = data.links || [];
  if (!links.length) {
    const tr = document.createElement('tr');
    tr.innerHTML = '<td colspan="5" class="muted empty-row">No link shares yet. Use the share button on a file or folder.</td>';
    body.appendChild(tr);
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
    msgEl.textContent = data.message || 'Account created. You can sign in now.';
    msgEl.hidden = false;
    const user = data.username || fd.get('username') || '';
    setTimeout(() => {
      location.href = user ? ('/?registered=' + encodeURIComponent(user)) : '/';
    }, 800);
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

