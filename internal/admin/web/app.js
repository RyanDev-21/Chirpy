// Chirpy admin page. Every value from the server is inserted as text
// (textContent), never as HTML. The admin token lives in sessionStorage and
// is gone when the tab closes.
"use strict";
const $ = (id) => document.getElementById(id);
let token = sessionStorage.getItem("chirpy-admin") || "";
let email = "";
let selected = null;

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: Object.assign({ "Content-Type": "application/json" }, token ? { Authorization: "Bearer " + token } : {}),
    body: body ? JSON.stringify(body) : undefined,
  });
  if (res.status === 401 || res.status === 403) {
    if (token) { signOut(); throw new Error("Session ended, sign in again."); }
  }
  const text = await res.text();
  const data = text ? JSON.parse(text) : {};
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

const fmtTime = (t) => (t ? new Date(t).toLocaleString() : "never");
const fmtBytes = (n) => (n > 1e9 ? (n / 1e9).toFixed(2) + " GB" : (n / 1e6).toFixed(1) + " MB");
function cell(tr, text, cls) { const td = document.createElement("td"); td.textContent = text; if (cls) td.className = cls; tr.appendChild(td); }
function pairs(dl, items) {
  dl.replaceChildren();
  for (const [k, v] of items) {
    const dt = document.createElement("dt"); dt.textContent = k;
    const dd = document.createElement("dd"); dd.textContent = v;
    dl.append(dt, dd);
  }
}
function status(u) {
  if (!u.banned_at) return u.is_admin ? "admin" : "active";
  return "banned " + (u.banned_until ? "until " + fmtTime(u.banned_until) : "permanently");
}

// --- sign in ---
$("login-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  $("login-error").textContent = "";
  email = $("email").value;
  try {
    await api("POST", "/api/admin/login", { email, password: $("password").value });
    $("password").value = "";
    $("login-form").hidden = true;
    $("code-form").hidden = false;
    $("code").focus();
  } catch (err) { $("login-error").textContent = err.message; }
});
$("code-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  $("login-error").textContent = "";
  try {
    const r = await api("POST", "/api/admin/verify", { email, code: $("code").value.trim() });
    token = r.token;
    sessionStorage.setItem("chirpy-admin", token);
    start();
  } catch (err) { $("login-error").textContent = err.message; }
});
function signOut() {
  token = "";
  sessionStorage.removeItem("chirpy-admin");
  $("app").hidden = true; $("logout").hidden = true;
  $("login").hidden = false; $("login-form").hidden = false; $("code-form").hidden = true;
}
$("logout").addEventListener("click", signOut);

// --- tabs ---
document.querySelectorAll("nav button").forEach((b) => b.addEventListener("click", () => show(b.dataset.tab)));
function show(tab) {
  document.querySelectorAll("nav button").forEach((b) => b.classList.toggle("active", b.dataset.tab === tab));
  document.querySelectorAll(".tab").forEach((t) => (t.hidden = t.id !== "tab-" + tab));
  ({ overview: loadOverview, users: loadUsers, groups: loadGroups, audit: loadAudit })[tab]().catch(alertError);
}
const alertError = (err) => { $("detail-msg").textContent = err.message; };

async function loadOverview() {
  const o = await api("GET", "/api/admin/overview");
  pairs($("stats"), [
    ["Users", o.users], ["Verified", o.verified], ["New (24 h / 7 d)", o.signups_24h + " / " + o.signups_7d],
    ["Active (7 d)", o.active_7d], ["Banned", o.banned], ["Messages (24 h)", o.messages_24h],
    ["Groups", o.groups], ["Media stored", fmtBytes(o.media_bytes)],
  ]);
}

async function loadUsers() {
  const q = encodeURIComponent($("user-q").value.trim());
  const r = await api("GET", "/api/admin/users?q=" + q);
  const body = $("users"); body.replaceChildren();
  for (const u of r.users) {
    const tr = document.createElement("tr");
    cell(tr, u.name); cell(tr, u.email); cell(tr, fmtTime(u.created_at)); cell(tr, fmtTime(u.last_seen_at));
    cell(tr, status(u), u.banned_at ? "banned" : "");
    tr.addEventListener("click", () => openUser(u.id).catch(alertError));
    body.appendChild(tr);
  }
}
$("user-search").addEventListener("submit", (e) => { e.preventDefault(); loadUsers().catch(alertError); });

async function openUser(id) {
  const u = await api("GET", "/api/admin/users/" + encodeURIComponent(id));
  selected = u;
  $("user-detail").hidden = false;
  $("detail-msg").textContent = "";
  $("d-name").textContent = u.name;
  pairs($("d-fields"), [
    ["Email", u.email + (u.email_verified ? " (verified)" : " (not verified)")],
    ["Joined", fmtTime(u.created_at)], ["Last active", fmtTime(u.last_seen_at)],
    ["Messages sent", u.stats.messages_sent], ["Friends", u.stats.friends], ["Groups", u.stats.groups],
    ["Phones", u.stats.devices], ["Status", status(u)], ["Ban reason", u.ban_reason || "-"],
  ]);
  $("ban-form").hidden = !!u.banned_at || u.is_admin;
  $("unban").hidden = !u.banned_at;
}
$("ban-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!selected) return;
  const days = Number($("ban-duration").value);
  const body = { reason: $("ban-reason").value };
  if (days > 0) body.until = new Date(Date.now() + days * 864e5).toISOString();
  try {
    await api("POST", "/api/admin/users/" + encodeURIComponent(selected.id) + "/ban", body);
    $("ban-reason").value = "";
    await openUser(selected.id); await loadUsers();
    $("detail-msg").textContent = "Banned.";
  } catch (err) { alertError(err); }
});
$("unban").addEventListener("click", async () => {
  if (!selected) return;
  try {
    await api("DELETE", "/api/admin/users/" + encodeURIComponent(selected.id) + "/ban");
    await openUser(selected.id); await loadUsers();
    $("detail-msg").textContent = "Ban lifted.";
  } catch (err) { alertError(err); }
});

async function loadGroups() {
  const q = encodeURIComponent($("group-q").value.trim());
  const r = await api("GET", "/api/admin/groups?q=" + q);
  const body = $("groups"); body.replaceChildren();
  for (const g of r.groups) {
    const tr = document.createElement("tr");
    cell(tr, g.name); cell(tr, g.member_count); cell(tr, g.leader || "-"); cell(tr, g.messages_7d); cell(tr, fmtTime(g.created_at));
    body.appendChild(tr);
  }
}
$("group-search").addEventListener("submit", (e) => { e.preventDefault(); loadGroups().catch(alertError); });

async function loadAudit() {
  const r = await api("GET", "/api/admin/audit?limit=100");
  const body = $("audit"); body.replaceChildren();
  for (const a of r.actions) {
    const tr = document.createElement("tr");
    cell(tr, fmtTime(a.at)); cell(tr, a.admin); cell(tr, a.action);
    cell(tr, a.target_user || a.target_group || "-"); cell(tr, a.detail || "");
    body.appendChild(tr);
  }
}

function start() {
  $("login").hidden = true; $("app").hidden = false; $("logout").hidden = false;
  show("overview");
}
if (token) start();
