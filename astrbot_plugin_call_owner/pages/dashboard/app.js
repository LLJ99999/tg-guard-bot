// 呼叫站长面板（AstrBot 插件视图，sandbox iframe：不用 alert/confirm/localStorage）
(() => {
"use strict";
const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const view = $("#view");
const st = { tab: "overview", data: null, draft: null, filter: "all" };

function toast(msg, err) {
  const t = $("#toast"); t.textContent = msg; t.className = err ? "err" : ""; t.hidden = false;
  clearTimeout(toast.t); toast.t = setTimeout(() => (t.hidden = true), err ? 5000 : 2200);
}
function ask(title, text, yes = "确定") {
  return new Promise((res) => {
    $("#m-title").textContent = title; $("#m-text").textContent = text || ""; $("#m-yes").textContent = yes;
    const m = $("#modal"); m.hidden = false;
    const done = (v) => { m.hidden = true; res(v); };
    $("#m-yes").onclick = () => done(true); $("#m-no").onclick = () => done(false);
  });
}
async function bridge() {
  for (let i = 0; i < 60; i++) {
    const b = window.AstrBotPluginView || window.AstrBotPluginPage;
    if (b) { await b.ready(); return b; }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("AstrBot 插件桥接未加载，请从 AstrBot 后台「插件 → 呼叫站长 → 视图」打开");
}
function unwrap(res) {
  if (res && typeof res === "object" && !Array.isArray(res)) {
    if (res.status === "error") throw new Error(res.message || (res.data && res.data.error) || "请求失败");
    if (res.status === "ok" && "data" in res) return res.data;
  }
  return res;
}
const get = async (p) => unwrap(await (await bridge()).apiGet("page/" + p, {}));
const post = async (p, body) => unwrap(await (await bridge()).apiPost("page/" + p, body || {}));

async function load() {
  try {
    st.data = await get("state");
    if (!st.draft) st.draft = { ...st.data.rules };
    const d = st.data;
    $("#sub").textContent = !d.telegram ? "⚠️ 没找到 Telegram 平台" : !d.rules.miao_id ? "⚠️ 还没填喵码" : d.hooked ? `运行中 · ${d.telegram_id}` : "正在挂载…";
    render();
  } catch (e) { view.innerHTML = `<div class="card bad">${esc(e.message)}</div>`; }
}

function render() {
  $$("#tabs button").forEach((b) => b.classList.toggle("on", b.dataset.tab === st.tab));
  if (!st.data) return;
  ({ overview, calls, rules })[st.tab]();
}

function gname(id) { return (st.data.known_groups || {})[id] || id; }
function mins(s) { return Math.ceil(s / 60); }

function overview() {
  const d = st.data, r = d.rules;
  const auto = d.calls.filter((c) => c.kind === "auto");
  const okc = auto.filter((c) => c.ok).length;
  const woke = auto.filter((c) => c.awake_after_min != null);
  const avg = woke.length ? (woke.reduce((a, c) => a + c.awake_after_min, 0) / woke.length).toFixed(1) : "—";
  let h = `<div class="card"><div class="grid">
    <div class="stat"><b>${auto.length}</b><span>累计呼叫通话</span></div>
    <div class="stat"><b class="ok">${okc}</b><span>拨通成功</span></div>
    <div class="stat"><b>${avg}</b><span>平均几分钟后 /awake</span></div>
    <div class="stat"><b>${r.threshold}人/${r.window_min}分</b><span>触发规则 · 冷却${r.cooldown_min}分</span></div>
  </div>
  <p class="muted" style="margin:10px 0 0">北京时间 ${esc(d.now)} · ${d.in_active_hours ? "当前可呼叫" : `<span class="warn">当前不在可呼叫时段（${esc(r.active_hours)}）</span>`} · 定时消息：${r.schedule_enabled ? `开启（${esc(r.schedule_times)}）` : "关闭"}${d.last_sched ? ` · 上次 ${esc(d.last_sched)}` : ""}</p></div>`;
  if (!d.groups.length) h += `<div class="card muted">还没有群。去「规则设置」填群 ID。</div>`;
  for (const g of d.groups) {
    h += `<div class="card"><h2>${esc(g.title || g.chat_id)} <small class="muted">${esc(g.chat_id)}</small></h2>`;
    if (g.cooldown_left > 0) h += `<p class="warn">冷却中，还剩 ${mins(g.cooldown_left)} 分钟</p>`;
    else h += `<p>当前 <b>${g.voters.length}/${r.threshold}</b> 人呼叫</p>`;
    if (g.voters.length) h += `<div>${g.voters.map((v) => `<span class="chip">${esc(v.name)}${v.username ? " @" + esc(v.username) : ""} · ${esc(v.via)} · ${esc(v.time.slice(11))}</span>`).join("")}</div>`;
    h += `<div class="row end">
      <button class="ghost" data-act="send" data-id="${esc(g.chat_id)}">立即发按钮消息</button>
      <button class="ghost" data-act="reset" data-id="${esc(g.chat_id)}">清零/解除冷却</button>
      <button class="pri" data-act="test" data-id="${esc(g.chat_id)}">测试拨号</button></div></div>`;
  }
  view.innerHTML = h;
}

function voterTable(vs) {
  if (!vs.length) return "";
  return `<table><tr><th>#</th><th>群友</th><th>方式</th><th>时间</th><th>ID</th></tr>${vs.map((v, i) =>
    `<tr><td>${i + 1}</td><td>${esc(v.name)}${v.username ? ` <span class="muted">@${esc(v.username)}</span>` : ""}</td><td>${esc(v.via)}</td><td>${esc((v.time || "").slice(11))}</td><td class="muted">${esc(v.uid)}</td></tr>`).join("")}</table>`;
}

function calls() {
  const all = st.data.calls;
  const list = all.filter((c) => st.filter === "all" || (st.filter === "auto" ? c.kind === "auto" : st.filter === "fail" ? !c.ok : c.kind === "test"));
  let h = `<div class="card"><div class="row">
    ${[["all", "全部"], ["auto", "群友呼叫"], ["test", "测试"], ["fail", "失败"]].map(([k, t]) => `<button class="${st.filter === k ? "pri" : "ghost"}" data-filter="${k}">${t}</button>`).join("")}
    <span class="muted">共 ${list.length} 条</span></div></div><div class="card">`;
  if (!list.length) h += `<p class="muted">还没有通话记录。</p>`;
  for (const c of list) {
    h += `<div class="call"><div class="head">
      <div><b>${esc(c.time)}</b> <span class="tag ${c.ok ? "ok" : "bad"}">${c.ok ? "已拨通" : "失败"}</span> <span class="tag t">${c.kind === "auto" ? "群友呼叫" : "测试"}</span>
      <span class="muted"> · ${esc(c.chat_title || gname(c.chat_id))} · ${c.voters.length} 人</span></div>
      <div class="muted">${c.awake_after_min != null ? `站长 ${c.awake_after_min} 分钟后 /awake` : c.kind === "auto" ? "站长未 /awake" : ""}</div></div>
      <div class="voice">🔊 ${esc(c.text)}</div>
      ${c.ok ? "" : `<div class="bad">返回：${esc(c.resp)}</div>`}
      <details><summary>谁点了按钮（${c.voters.length}）</summary>${voterTable(c.voters)}</details>
      <div class="row end" style="margin-top:6px"><button class="danger" data-act="del" data-id="${esc(c.id)}">删除</button></div></div>`;
  }
  view.innerHTML = h + `</div>`;
}

function field(k, label, type = "text", hint = "") {
  const v = st.draft[k] ?? "";
  const inp = type === "textarea" ? `<textarea data-k="${k}">${esc(v)}</textarea>`
    : `<input type="${type}" data-k="${k}" value="${esc(v)}" ${type === "number" ? 'min="0"' : ""}>`;
  return `<label class="f"><span>${label}</span>${inp}${hint ? `<small>${hint}</small>` : ""}</label>`;
}
function rules() {
  const kg = st.data.known_groups || {};
  view.innerHTML = `
  <div class="card"><h2>电话</h2>
    ${field("miao_id", "喵码", "text", "喵提醒里创建「语音电话」提醒后得到的 t 开头编码，只填编码不要填网址")}
    ${field("voice_template", "电话里念的内容", "textarea", "可用变量：{group} 群名，{count} 人数，{names} 呼叫人，{time} 时间。最多 200 字")}
  </div>
  <div class="card"><h2>触发规则</h2><div class="two">
    ${field("threshold", "多少人点才打", "number")}
    ${field("window_min", "统计窗口（分钟）", "number")}
    ${field("cooldown_min", "打通后冷却（分钟）", "number")}
    ${field("fail_cooldown_min", "拨号失败后冷却（分钟）", "number")}
    </div>
    ${field("active_hours", "只在这些时段可呼叫", "text", "如 00:00-09:00,13:00-14:00，北京时间；留空=全天")}
    ${field("blacklist_ids", "禁止呼叫的用户 ID", "text", "多个用逗号分隔；用户 ID 可在通话记录里看到")}
  </div>
  <div class="card"><h2>群和站长</h2>
    ${field("group_ids", "允许的群 ID", "text", "逗号分隔；留空=机器人在的所有群。已知群：" + Object.entries(kg).map(([id, t]) => `${esc(t)}（${esc(id)}）`).join("，"))}
    ${field("owner_ids", "站长 TG 用户 ID", "text", "这些人可以在群里发 /awake 表示已醒，并开始冷却")}
    ${field("platform_id", "Telegram 平台 ID", "text", "留空=自动用第一个 Telegram 平台")}
  </div>
  <div class="card"><h2>定时发送按钮消息</h2>
    <label class="switch"><input type="checkbox" data-k="schedule_enabled" ${st.draft.schedule_enabled ? "checked" : ""}> 开启</label>
    ${field("schedule_times", "发送时间", "text", "北京时间，多个用逗号，如 23:30,03:00")}
    ${field("schedule_text", "消息内容", "textarea", "可用变量：{threshold} 人数，{window} 窗口分钟，{cooldown} 冷却分钟")}
    ${field("button_text", "按钮文字")}
  </div>
  <div class="row end"><button class="ghost" data-act="undo">撤销修改</button><button class="pri" data-act="save">保存规则</button></div>`;
}

view.addEventListener("input", (e) => {
  const k = e.target.dataset.k; if (!k) return;
  st.draft[k] = e.target.type === "checkbox" ? e.target.checked : e.target.type === "number" ? Number(e.target.value) : e.target.value;
});
view.addEventListener("change", (e) => { if (e.target.type === "checkbox") st.draft[e.target.dataset.k] = e.target.checked; });
view.addEventListener("click", async (e) => {
  const f = e.target.closest("[data-filter]"); if (f) { st.filter = f.dataset.filter; return render(); }
  const b = e.target.closest("[data-act]"); if (!b) return;
  const id = b.dataset.id, act = b.dataset.act;
  try {
    if (act === "test") {
      if (!(await ask("测试拨号", "会真的给站长手机打一通电话（约 0.08 元），记录为测试。", "拨打"))) return;
      b.disabled = true; await post("test", { chat_id: id }); toast("已拨号，留意手机");
    } else if (act === "send") {
      if (!(await ask("发按钮消息", `现在往 ${gname(id)} 发一条带「叫醒站长」按钮的消息？`, "发送"))) return;
      await post("send", { chat_id: id }); toast("已发送");
    } else if (act === "reset") {
      await post("reset", { chat_id: id }); toast("已清零");
    } else if (act === "del") {
      if (!(await ask("删除记录", "删除这条通话记录？", "删除"))) return;
      await post("delete_call", { id }); toast("已删除");
    } else if (act === "undo") {
      st.draft = { ...st.data.rules }; return render();
    } else if (act === "save") {
      b.disabled = true; await post("save", { rules: st.draft }); st.draft = null; toast("规则已保存，立即生效");
    }
    await load();
  } catch (err) { toast(err.message, true); b.disabled = false; }
});
$("#tabs").addEventListener("click", (e) => {
  const b = e.target.closest("button[data-tab]"); if (!b) return;
  st.tab = b.dataset.tab; render();
});
load();
setInterval(() => { if (st.tab !== "rules") load(); }, 15000);
})();
