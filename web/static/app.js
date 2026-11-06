const state = {
  user: null,
  root: 'home',
  path: '',
  perm: 'write',
  roots: [],
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
  $('#admin-view').hidden = view !== 'admin';
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
  $('#btn-admin').hidden = !state.user.isAdmin;
  show('main');
  await loadRoots();
  await loadList();
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

$('#btn-admin').addEventListener('click', async () => {
  show('admin');
  await refreshAdmin();
});
$('#btn-back').addEventListener('click', () => show('main'));

async function loadRoots() {
  const data = await api('/api/roots');
  state.roots = data.roots;
  const ul = $('#root-list');
  ul.innerHTML = '';
  for (const r of data.roots) {
    const li = document.createElement('li');
    const b = document.createElement('button');
    b.textContent = r.name + (r.perm === 'read' ? ' (ro)' : '');
    if (r.id === state.root) b.classList.add('active');
    b.onclick = async () => {
      state.root = r.id;
      state.path = '';
      await loadRoots();
      await loadList();
    };
    li.appendChild(b);
    ul.appendChild(li);
  }
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

async function loadList() {
  const data = await api(`/api/list?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(state.path)}`);
  state.perm = data.perm;
  const canWrite = state.perm === 'write';
  $('#btn-mkdir').disabled = !canWrite;
  $('#file-input').disabled = !canWrite;
  renderBreadcrumbs();
  const body = $('#file-body');
  body.innerHTML = '';
  const entries = (data.entries || []).slice().sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    return a.name.localeCompare(b.name);
  });
  for (const e of entries) {
    const tr = document.createElement('tr');
    const icon = e.kind === 'dir' ? '📁' : e.kind === 'image' ? '🖼️' : e.kind === 'video' ? '🎬' : '📄';
    tr.innerHTML = `<td class="icon">${icon}</td><td></td><td>${e.isDir ? '—' : fmtSize(e.size)}</td><td>${new Date(e.modTime).toLocaleString()}</td><td class="row-actions"></td>`;
    const nameCell = tr.children[1];
    const nameBtn = document.createElement('button');
    nameBtn.className = 'ghost';
    nameBtn.textContent = e.name;
    nameBtn.onclick = async () => {
      if (e.isDir) {
        state.path = e.path;
        await loadList();
      } else if (e.kind === 'image' || e.kind === 'video') {
        openPreview(e);
      } else {
        window.open(`/api/download?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(e.path)}`, '_blank');
      }
    };
    nameCell.appendChild(nameBtn);
    const actions = tr.children[4];
    const dl = document.createElement('button');
    dl.textContent = '↓';
    dl.className = 'ghost';
    dl.onclick = () => {
      window.location = `/api/download?root=${encodeURIComponent(state.root)}&path=${encodeURIComponent(e.path)}`;
    };
    if (!e.isDir) actions.appendChild(dl);
    if (canWrite) {
      const ren = document.createElement('button');
      ren.textContent = 'Rename';
      ren.className = 'ghost';
      ren.onclick = async () => {
        const next = prompt('New path relative to root', e.path);
        if (!next || next === e.path) return;
        await api('/api/rename', { method: 'POST', body: JSON.stringify({ root: state.root, from: e.path, to: next }) });
        await loadList();
      };
      const del = document.createElement('button');
      del.textContent = 'Del';
      del.className = 'ghost';
      del.onclick = async () => {
        if (!confirm('Delete ' + e.name + '?')) return;
        await api('/api/delete', { method: 'POST', body: JSON.stringify({ root: state.root, path: e.path }) });
        await loadList();
      };
      actions.appendChild(ren);
      actions.appendChild(del);
    }
    body.appendChild(tr);
  }
}

function openPreview(e) {
  const body = $('#preview-body');
  body.innerHTML = '';
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
  $('#preview').showModal();
}

$('#btn-mkdir').addEventListener('click', async () => {
  const name = prompt('Folder name');
  if (!name) return;
  const path = state.path ? state.path + '/' + name : name;
  await api('/api/mkdir', { method: 'POST', body: JSON.stringify({ root: state.root, path }) });
  await loadList();
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

function uploadFileWithProgress(file, onProgress) {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    fd.append('root', state.root);
    fd.append('path', state.path);
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

$('#file-input').addEventListener('change', async (ev) => {
  const files = [...ev.target.files];
  ev.target.value = '';
  if (!files.length) return;
  ensureUploadPanel();
  const list = $('#upload-list');
  $('#btn-clear-uploads').hidden = true;
  let anyErr = false;
  for (const f of files) {
    const li = document.createElement('li');
    li.className = 'upload-item';
    li.innerHTML = `<div class="row"><span class="name"></span><span class="meta"></span></div><div class="bar"><span></span></div>`;
    li.querySelector('.name').textContent = f.name;
    list.appendChild(li);
    setUploadItem(li, { loaded: 0, total: f.size, status: 'uploading' });
    try {
      await uploadFileWithProgress(f, (loaded, total) => {
        setUploadItem(li, { loaded, total, status: 'uploading' });
      });
      setUploadItem(li, { loaded: f.size, total: f.size, status: 'done' });
    } catch (err) {
      anyErr = true;
      setUploadItem(li, { loaded: 0, total: f.size, status: 'error', error: err.message });
    }
  }
  $('#btn-clear-uploads').hidden = false;
  if (!anyErr) await loadList();
  else {
    try { await loadList(); } catch { /* keep status visible */ }
  }
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
  const ul = $('#user-list');
  ul.innerHTML = '';
  for (const u of users.users) {
    const li = document.createElement('li');
    li.className = 'user-card';
    const used = u.usedBytes || 0;
    const quota = u.quotaBytes || 0;
    const pct = quota > 0 ? Math.min(100, Math.round(used / quota * 100)) : 0;
    const badges = [];
    if (u.isAdmin) badges.push('<span class="badge admin">admin</span>');
    if (u.disabled) badges.push('<span class="badge disabled">disabled</span>');
    li.innerHTML = `
      <div><strong>${u.username}</strong> ${badges.join(' ')} <span class="muted">#${u.id}</span></div>
      <div class="meta">home: <code>${u.homePath}</code></div>
      <div class="meta">used ${fmtSize(used)} / ${fmtQuota(quota)}${quota ? ` (${pct}%)` : ''}</div>
      ${quota ? `<div class="quota-bar${used > quota ? ' over' : ''}"><span style="width:${pct}%"></span></div>` : ''}
    `;
    const btns = document.createElement('div');
    btns.className = 'row-btns';
    const edit = document.createElement('button');
    edit.textContent = 'Edit';
    edit.className = 'ghost';
    edit.onclick = () => openUserEdit(u);
    btns.appendChild(edit);
    if (u.id !== state.user.id) {
      const b = document.createElement('button');
      b.textContent = 'Delete';
      b.className = 'ghost';
      b.onclick = async () => {
        if (!confirm('Delete user ' + u.username + '?')) return;
        await api('/api/admin/users/' + u.id, { method: 'DELETE' });
        await refreshAdmin();
      };
      btns.appendChild(b);
    }
    li.appendChild(btns);
    ul.appendChild(li);
  }

  const shares = await api('/api/admin/shares');
  const sl = $('#share-list');
  sl.innerHTML = '';
  for (const s of shares.shares) {
    const li = document.createElement('li');
    const grants = (s.grants || []).map(g => `user#${g.userId}:${g.perm}`).join(', ') || 'no grants';
    li.innerHTML = `<strong>${s.name}</strong> · <code>${s.path}</code><div class="muted">${grants}</div>`;
    const grantBtn = document.createElement('button');
    grantBtn.textContent = 'Grant';
    grantBtn.className = 'ghost';
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
    del.textContent = 'Delete share';
    del.className = 'ghost';
    del.onclick = async () => {
      if (!confirm('Delete share definition? (files kept)')) return;
      await api('/api/admin/shares/' + s.id, { method: 'DELETE' });
      await refreshAdmin();
    };
    li.appendChild(grantBtn);
    li.appendChild(del);
    sl.appendChild(li);
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
