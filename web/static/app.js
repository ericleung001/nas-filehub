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

$('#file-input').addEventListener('change', async (ev) => {
  const files = [...ev.target.files];
  for (const f of files) {
    const fd = new FormData();
    fd.append('root', state.root);
    fd.append('path', state.path);
    fd.append('file', f);
    await api('/api/upload', { method: 'POST', body: fd });
  }
  ev.target.value = '';
  await loadList();
});

$('#btn-refresh').addEventListener('click', loadList);

async function refreshAdmin() {
  const users = await api('/api/admin/users');
  const ul = $('#user-list');
  ul.innerHTML = '';
  for (const u of users.users) {
    const li = document.createElement('li');
    li.innerHTML = `<strong>${u.username}</strong> · home: <code>${u.homePath}</code> ${u.isAdmin ? '· admin' : ''}`;
    if (u.id !== state.user.id) {
      const b = document.createElement('button');
      b.textContent = 'Delete';
      b.className = 'ghost';
      b.onclick = async () => {
        if (!confirm('Delete user ' + u.username + '?')) return;
        await api('/api/admin/users/' + u.id, { method: 'DELETE' });
        await refreshAdmin();
      };
      li.appendChild(b);
    }
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
  await api('/api/admin/users', {
    method: 'POST',
    body: JSON.stringify({
      username: fd.get('username'),
      password: fd.get('password'),
      homePath: fd.get('homePath') || '',
      isAdmin: !!fd.get('isAdmin'),
    }),
  });
  e.target.reset();
  await refreshAdmin();
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
