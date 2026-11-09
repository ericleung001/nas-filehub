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
  if (view !== 'settings') stopPreviewMedia();
}

async function bootstrap() {
  try {
    const me = await api('/api/me');
    state.user = me.user;
    await enterApp();
  } catch {
    show('login');
  }
}

async function enterApp() {
  $('#whoami').textContent = state.user.username + (state.user.isAdmin ? ' (admin)' : '');
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

$('#login-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  $('#login-error').hidden = true;
  try {
    const data = await api('/api/login', {
      method: 'POST',
      body: JSON.stringify({ username: fd.get('username'), password: fd.get('password') }),
    });
    state.user = data.user;
    await enterApp();
  } catch (err) {
    $('#login-error').textContent = err.message;
    $('#login-error').hidden = false;
  }
});

$('#btn-logout').addEventListener('click', async () => {
  await api('/api/logout', { method: 'POST', body: '{}' });
  state.user = null;
  show('login');
});

$('#btn-settings').addEventListener('click', async () => {
  show('settings');
  await openSettings();
});
$('#btn-back-settings').addEventListener('click', () => show('main'));

function setSettingsTab(tab) {
  document.querySelectorAll('.settings-tab').forEach((btn) => {
    const on = btn.dataset.tab === tab;
    btn.classList.toggle('active', on);
    btn.setAttribute('aria-selected', on ? 'true' : 'false');
  });
  document.querySelectorAll('.settings-panel').forEach((panel) => {
    const on = panel.id === 'tab-' + tab;
    panel.classList.toggle('active', on);
    panel.hidden = !on;
  });
}

document.querySelectorAll('.settings-tab').forEach((btn) => {
  btn.addEventListener('click', async () => {
    const tab = btn.dataset.tab;
    setSettingsTab(tab);
    if (tab === 'users' || tab === 'shares') {
      try { await refreshAdmin(); } catch (err) { console.error(err); }
    }
  });
});

async function openSettings() {
  try { await refreshMe(); } catch { /* keep cached */ }
  const u = state.user;
  document.querySelectorAll('.settings-tab.admin-only').forEach((el) => {
    el.hidden = !u.isAdmin;
  });
  setSettingsTab('account');
  const acc = $('#settings-account');
  const badges = [];
  if (u.isAdmin) badges.push('<span class="badge admin">admin</span>');
  const used = u.usedBytes || 0;
  const quota = u.quotaBytes || 0;
  const pct = quota > 0 ? Math.min(100, Math.round(used / quota * 100)) : 0;
  acc.innerHTML = `
    <div><strong>${u.username}</strong> ${badges.join(' ')}</div>
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
    await api('/api/me/password', {
      method: 'POST',
      body: JSON.stringify({
        currentPassword: fd.get('currentPassword'),
        newPassword: fd.get('newPassword'),
      }),
    });
    msg.textContent = 'Password updated';
    msg.hidden = false;
    e.target.reset();
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
  applyViewMode();
  applySortControls();
  renderEntries(sortEntries(state.entries), canWrite);
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
  return e.kind === 'dir' ? '📁' : e.kind === 'image' ? '🖼️' : e.kind === 'video' ? '🎬' : '📄';
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
  } else if (e.kind === 'image' || e.kind === 'video') {
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
      tr.innerHTML = `<td colspan="5" class="empty-row muted">${canWrite ? 'Empty folder — drop files here to upload' : 'Empty folder'}</td>`;
      body.appendChild(tr);
    }
    return;
  }

  if (state.viewMode === 'icons') {
    for (const e of entries) {
      const card = document.createElement('div');
      card.className = 'file-card';
      card.title = e.name;
      if (e.isDir) markFolderDropTarget(card, e.path);

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

      card.appendChild(thumb);
      card.appendChild(name);
      card.appendChild(actions);
      card.onclick = () => openEntry(e);
      grid.appendChild(card);
    }
    return;
  }

  for (const e of entries) {
    const tr = document.createElement('tr');
    if (e.isDir) markFolderDropTarget(tr, e.path);
    tr.innerHTML = `<td class="icon">${entryIcon(e)}</td><td class="name-cell"><div class="name-wrap"></div></td><td class="size-cell">${e.isDir ? '—' : fmtSize(e.size)}</td><td class="mod-cell">${new Date(e.modTime).toLocaleString()}</td><td class="row-actions"></td>`;
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

function ensureUploadPanel() {
  const panel = $('#upload-status');
  panel.hidden = false;
  return panel;
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
  $('#upload-status').hidden = true;
  $('#btn-clear-uploads').hidden = true;
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
  form.homePath.value = u.homePath;
  form.quota.value = u.quotaBytes ? fmtQuota(u.quotaBytes).replace(' ', '') : '0';
  form.isAdmin.checked = !!u.isAdmin;
  form.disabled.checked = !!u.disabled;
  $('#edit-user-error').hidden = true;
  $('#set-password').id.value = u.id;
  $('#set-password').password.value = '';
  $('#set-password-msg').hidden = true;
  dlg.showModal();
}

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
        homePath: fd.get('homePath'),
        isAdmin: !!fd.get('isAdmin'),
        disabled: !!fd.get('disabled'),
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

bootstrap();
