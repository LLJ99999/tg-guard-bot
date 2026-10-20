// TG 群管面板（AstrBot 插件视图）
// 注意：视图运行在 sandbox iframe 里（没有 allow-same-origin / allow-modals），
// 所以不能用 localStorage、confirm()、alert()，一切后端调用都走 AstrBot bridge。
// 用普通 <script>（不用 type=module）：sandbox 下 module 脚本要求 CORS，部分部署会被拦截。

(() => {
"use strict";
const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const view = $("#view");

// ---------- 存储（sandbox 里 localStorage 会抛异常，失败就只存内存） ----------
const mem = {};
const store = {
  get(k) { try { return localStorage.getItem(k) ?? mem[k] ?? ""; } catch { return mem[k] ?? ""; } },
  set(k, v) { mem[k] = v; try { localStorage.setItem(k, v); } catch { /* sandbox */ } },
};

const state = {
  targets: { oai: false, jack: false },
  info: null,
  groups: [],
  gid: store.get("tgguard.gid"),
  tab: (location.hash || "").slice(1) || "overview",
};

// ---------- 提示 / 确认框 ----------
function toast(msg, err) {
  const t = $("#toast");
  t.textContent = msg;
  t.className = err ? "err" : "";
  t.hidden = false;
  clearTimeout(toast.t);
  toast.t = setTimeout(() => (t.hidden = true), err ? 5000 : 2200);
}

function ask(title, text = "", { danger = false, yes = "确定" } = {}) {
  return new Promise((resolve) => {
    const m = $("#modal");
    $("#modal-title").textContent = title;
    if (text instanceof Node) $("#modal-text").replaceChildren(text); else $("#modal-text").textContent = text;
    const y = $("#modal-yes");
    y.textContent = yes;
    y.className = "pri";
    y.style.background = y.style.borderColor = danger ? "var(--bad)" : "";
    m.hidden = false;
    const done = (v) => { m.hidden = true; y.onclick = $("#modal-no").onclick = m.onclick = null; resolve(v); };
    y.onclick = () => done(true);
    $("#modal-no").onclick = () => done(false);
    m.onclick = (e) => e.target === m && done(false);
  });
}

// ---------- bridge ----------
async function bridge() {
  for (let i = 0; i < 40; i++) {
    const b = window.AstrBotPluginView || window.AstrBotPluginPage;
    if (b) { await b.ready(); return b; }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("AstrBot 插件桥接未加载，请从 AstrBot 后台「插件 → TG 群管与中继面板 → 视图」打开");
}

// bridge 已经把 {status:"ok",data:x} 解成 x；这里兼容旧版本 / 其它返回形状
function unwrap(res) {
  if (res && typeof res === "object" && !Array.isArray(res)) {
    if (res.status === "error") throw new Error(res.message || (res.data && res.data.error) || "请求失败");
    if (res.status === "ok" && "data" in res) return res.data;
  }
  return res;
}

async function call(target, method, path, body, query) {
  const b = await bridge();
  return unwrap(await b.apiPost("page/call", { target, method, path, body, query }));
}
const oai = (m, p, b, q) => call("oai", m, "/api" + p, b, q);
const jack = (m, p, b, q) => call("jack", m, "/api" + p, b, q);

// ---------- 小工具 ----------
const fmtDur = (sec) => {
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
  return d ? `${d} 天 ${h} 小时` : h ? `${h} 小时 ${m} 分` : `${m} 分钟`;
};
const fmtTime = (s) => {
  if (!s) return "";
  const d = new Date(s);
  return isNaN(d) || d.getFullYear() < 2000 ? "" : d.toLocaleString("zh-CN", { hour12: false, month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
};
const empty = (ic, text, cols = 1) => `<tr><td colspan="${cols}"><div class="empty"><span class="e">${icon(ic, 30)}</span>${text}</div></td></tr>`;
const pill = (cls, text) => `<span class="pill ${cls}"><span class="dot"></span>${text}</span>`;

function act(btn, fn) {
  return async (...a) => {
    if (btn) btn.disabled = true;
    try { await fn(...a); } catch (e) { toast(e.message || String(e), true); } finally { if (btn) btn.disabled = false; }
  };
}
function on(sel, fn, root = view) { const el = $(sel, root); if (el) el.onclick = act(el, fn); return el; }
function onEnter(sel, fn) { const el = $(sel); if (el) el.onkeydown = (e) => e.key === "Enter" && !e.shiftKey && (e.preventDefault(), fn()); }

// ---------- 表单脏检查 + 底部保存条 ----------
let saveCtx = null;
function readEl(el) {
  if (el.type === "checkbox") return el.checked;
  if (el.type === "radio") return el.checked ? el.value : undefined;
  if (el.type === "number") return Number(el.value);
  return el.value;
}
function collectDiff(root, orig) {
  const diff = {};
  $$("[data-k]", root).forEach((el) => {
    const v = readEl(el);
    if (v === undefined) return;
    const k = el.dataset.k;
    if (v !== orig[k] && !(orig[k] == null && v === "")) diff[k] = v;
  });
  return diff;
}
function bindForm(root, orig, save) {
  const refresh = () => {
    const diff = collectDiff(root, orig);
    $$("[data-k]", root).forEach((el) => {
      const wrap = el.closest(".tg, .seg, .field");
      if (wrap) wrap.classList.toggle("dirty", Object.prototype.hasOwnProperty.call(diff, el.dataset.k));
    });
    const n = Object.keys(diff).length;
    $("#savebar").hidden = !n;
    $("#savecount").textContent = `${n} 项未保存`;
  };
  saveCtx = { root, orig, save, refresh };
  refresh();
}
// 只挂一次，委托给当前页面的表单
const onFormEdit = (e) => { if (saveCtx && saveCtx.root.contains(e.target)) saveCtx.refresh(); };
document.addEventListener("input", onFormEdit);
document.addEventListener("change", onFormEdit);
$("#savego").onclick = act($("#savego"), async () => {
  const ctx = saveCtx;
  if (!ctx) return;
  const diff = collectDiff(ctx.root, ctx.orig);
  if (!Object.keys(diff).length) return;
  const res = await ctx.save(diff);
  Object.assign(ctx.orig, res && typeof res === "object" ? res : diff);
  if (saveCtx !== ctx) return;
  ctx.refresh();
  toast(`已保存 ${Object.keys(diff).length} 项`);
});
$("#saveundo").onclick = () => route();

// ---------- 图标（线条 SVG，跟随文字颜色） ----------
const ICON_PATHS = {
  home: '<path d="M3 10.5 12 3l9 7.5"/><path d="M5 9v11h14V9"/>',
  gear: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>',
  ban: '<circle cx="12" cy="12" r="9"/><path d="m5.6 5.6 12.8 12.8"/>',
  note: '<path d="M6 3h9l4 4v14H6z"/><path d="M14 3v5h5M9 13h6M9 17h6"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  users: '<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><path d="M16 4.5a3.5 3.5 0 0 1 0 7M18 14.5a6.5 6.5 0 0 1 3.5 5.5"/>',
  chart: '<path d="M4 20V10M10 20V4M16 20v-7M22 20H2"/>',
  list: '<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>',
  chat: '<path d="M21 12a8 8 0 0 1-11.6 7.1L3 21l1.9-6.4A8 8 0 1 1 21 12z"/>',
  shield: '<path d="M12 3 4 6v6c0 5 3.4 8.4 8 9 4.6-.6 8-4 8-9V6z"/>',
  alert: '<path d="M12 3 2 21h20z"/><path d="M12 10v5M12 18h.01"/>',
  plug: '<path d="M9 2v6M15 2v6M6 8h12v3a6 6 0 0 1-12 0zM12 17v5"/>',
  inbox: '<path d="M3 13h5l2 3h4l2-3h5"/><path d="M5 5h14l2 8v6H3v-6z"/>',
  flag: '<path d="M5 21V4"/><path d="M5 4h12l-2 4 2 4H5"/>',
  link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>',
  upload: '<path d="M12 16V4M7 9l5-5 5 5"/><path d="M4 16v4h16v-4"/>',
  copy: '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/>',
};
const icon = (k, size = 18) => `<svg class="icon" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICON_PATHS[k] || ""}</svg>`;

// ---------- 导航 ----------
const TABS = [
  { k: "overview", n: "概览", ic: "home" },
  { sep: "群管 oai_bot", t: "oai" },
  { k: "group", n: "群设置", ic: "gear", t: "oai", g: true, d: "入群验证、防护开关、锁定、欢迎语" },
  { k: "reports", n: "举报", ic: "flag", t: "oai", d: "群友举报的消息，在这里一键处理" },
  { k: "filters", n: "屏蔽词", ic: "ban", t: "oai", g: true, d: "命中后按设置的方式处理" },
  { k: "content", n: "笔记与自动回复", ic: "note", t: "oai", g: true, d: "#名字 查看笔记，关键词自动回复" },
  { k: "schedules", n: "定时消息", ic: "clock", t: "oai", g: true, d: "每天固定时间发送" },
  { k: "ads", n: "定时广告", ic: "flag", t: "oai", g: true, d: "Markdown + 图片 + 按钮，按时间或间隔自动发" },
  { k: "bioscan", n: "主页查重", ic: "link", t: "oai", g: true, d: "找出多人共用同一主页链接的疑似广告号，只报告不自动处罚" },
  { k: "punish", n: "处罚记录", ic: "shield", t: "oai", g: true, d: "机器人封禁、禁言、踢出的人，误封一键撤销" },
  { k: "people", n: "成员列表", ic: "users", t: "oai", g: true, d: "机器人见过的成员，搜索后直接禁言、封禁、解封" },
  { k: "learn", n: "AI 学习", ic: "note", t: "oai", d: "管理员确认的广告和误封消息，机器人照着学" },
  { k: "members", n: "白名单与封禁", ic: "users", t: "oai", g: true, d: "管理员白名单、免检白名单、警告、解封、全局封禁" },
  { k: "feds", n: "联邦封禁", ic: "link", t: "oai", d: "多个群共用一份封禁名单，一处封禁处处生效" },
  { k: "stats", n: "统计", ic: "chart", t: "oai", g: true, d: "近 30 天" },
  { k: "ai", n: "AI 设置", ic: "shield", t: "oai", d: "AI 审核用的接口地址、密钥和模型" },
  { k: "logs", n: "操作日志", ic: "list", t: "oai", d: "机器人做过的所有处理" },
  { sep: "私聊中继", t: "jack" },
  { k: "relay", n: "私聊中继", ic: "chat", t: "jack", d: "访客、验证与自动回复" },
];
const tabOf = (k) => TABS.find((t) => t.k === k);
const visible = (t) => !t.t || state.targets[t.t];

function renderNav() {
  $("#nav").innerHTML = TABS.filter(visible).map((t) => t.sep
    ? `<div class="sep">${esc(t.sep)}</div>`
    : `<a data-k="${t.k}" class="${t.k === state.tab ? "on" : ""}"><span class="ic">${icon(t.ic)}</span>${t.n}${t.k === "reports" && state.info && state.info.open_reports ? `<span class="badge">${state.info.open_reports}</span>` : ""}</a>`).join("");
  $("#nav").onclick = (e) => {
    const a = e.target.closest("a[data-k]");
    if (!a) return;
    state.tab = a.dataset.k;
    try { location.hash = state.tab; } catch { /* sandbox */ }
    route();
  };
}

function renderBar() {
  const t = tabOf(state.tab) || TABS[0];
  $("#title").textContent = t.n;
  $("#subtitle").textContent = t.d || "";
  let html = "";
  if (t.g && state.groups.length) {
    html += `<select id="gsel" title="当前群">${state.groups.map((g) => `<option value="${g.id}" ${String(g.id) === state.gid ? "selected" : ""}>${esc(g.title || g.id)}</option>`).join("")}</select>`;
  }
  html += `<button class="ghost" id="reload" title="刷新">⟳ 刷新</button>`;
  $("#barright").innerHTML = html;
  const s = $("#gsel");
  if (s) s.onchange = () => { state.gid = s.value; store.set("tgguard.gid", s.value); route(); };
  $("#reload").onclick = () => route();
}

async function route() {
  saveCtx = null;
  $("#savebar").hidden = true;
  const t = tabOf(state.tab);
  if (!t || !visible(t)) state.tab = "overview";
  renderNav();
  view.innerHTML = `<div class="skeleton"></div><div class="skeleton"></div>`;
  try {
    if (tabOf(state.tab).g) {
      state.groups = (await oai("GET", "/groups")) || [];
      if (!state.groups.find((g) => String(g.id) === state.gid)) state.gid = state.groups[0] ? String(state.groups[0].id) : "";
      renderBar();
      if (!state.gid) {
        view.innerHTML = `<div class="card"><div class="empty"><span class="e">${icon("users", 34)}</span>机器人还没有加入任何群<br><span class="muted">把 oai_bot 拉进群并设为管理员（删除消息、封禁用户、邀请用户），然后在群里发一次 /settings。</span></div></div>`;
        return;
      }
    } else {
      renderBar();
    }
    await PAGES[state.tab]();
  } catch (e) {
    view.innerHTML = `<div class="card"><div class="empty"><span class="e">${icon("alert", 34)}</span><b>加载失败</b><br><span class="muted">${esc(e.message)}</span><br><br><button id="retry">重试</button></div></div>`;
    on("#retry", route);
  }
}

// ---------- 页面 ----------
const PAGES = {};

PAGES.overview = async () => {
  if (!state.targets.oai && !state.targets.jack) {
    view.innerHTML = `<div class="card"><div class="card-h"><div><h2>还没有配置</h2><p>到「插件 → TG 群管与中继面板 → 配置」填写两个机器人的内网地址和管理 token，保存后重载插件。</p></div></div>
      <div class="fields"><div class="field"><span>oai_url</span><code>http://oai-bot:8090</code></div><div class="field"><span>jack_url</span><code>http://jack-bot:8090</code></div></div></div>`;
    return;
  }
  const [o, j] = await Promise.all([
    state.targets.oai ? oai("GET", "/status").catch((e) => ({ _err: e.message })) : null,
    state.targets.jack ? jack("GET", "/status").catch((e) => ({ _err: e.message })) : null,
  ]);
  let html = "";
  if (o) {
    if (!o._err) { state.info = o; $("#botname").textContent = "@" + (o.bot_username || ""); }
    const feat = (ok, n) => pill(ok ? "ok" : "", `${n}${ok ? "" : " 未配置"}`);
    html += o._err
      ? `<div class="card"><div class="svc"><div class="avatar">${icon("shield", 22)}</div><div><h2>群管 oai_bot</h2>${pill("bad", "离线")}</div></div><p class="muted">${esc(o._err)}</p></div>`
      : `<div class="card"><div class="card-h"><div class="svc"><div class="avatar">${icon("shield", 22)}</div><div><h2>群管 oai_bot ${pill("ok", "在线")}</h2><span class="muted">@${esc(o.bot_username)} · 已运行 ${fmtDur(o.uptime_sec)}</span></div></div></div>
          <div class="kpis"><div class="kpi"><b>${o.groups}</b><span>管理的群</span></div><div class="kpi"><b>${esc(o.timezone)}</b><span>时区</span></div></div>
          <div class="feat">${pill("ok", "内置验证码")}${feat(o.captcha, "网页验证")}${feat(o.ai, "AI 审核")}${feat(o.cas, "CAS 黑名单")}${pill(o.log_chat_id ? "ok" : "", o.log_chat_id ? "日志频道" : "日志只在面板")}</div></div>`;
    if (!o._err && o.observe_only) html += `<div class="notice"><div>部署待启用：自动群消息处理、审核、处罚与定时消息均已暂停。手动面板操作仍会执行。</div></div>`;
    if (!o._err && !o.ai) html += `<div class="notice info">${icon("alert")}<div>AI 审核未配置：在左侧「AI 设置」里填写接口地址、密钥和模型</div></div>`;
  }
  if (j) {
    html += j._err
      ? `<div class="card"><div class="svc"><div class="avatar">${icon("chat", 22)}</div><div><h2>私聊中继 jack_chuanhua_bot</h2>${pill("bad", "离线")}</div></div><p class="muted">${esc(j._err)}</p></div>`
      : `<div class="card"><div class="card-h"><div class="svc"><div class="avatar">${icon("chat", 22)}</div><div><h2>私聊中继 ${pill("ok", "在线")}</h2><span class="muted">已运行 ${fmtDur(j.uptime_sec)}</span></div></div></div>
          <div class="kpis"><div class="kpi"><b>${j.users}</b><span>访客</span></div><div class="kpi"><b>${j.verified}</b><span>已验证</span></div><div class="kpi"><b>${j.banned}</b><span>已封禁</span></div><div class="kpi"><b>${j.messages}</b><span>消息</span></div></div></div>`;
  }
  html += `<div class="card"><div class="card-h"><div><h2>群里常用命令</h2><p>只保留这几个，其它设置都在本面板或群里 /settings 的按钮里完成</p></div></div>
    <div class="fields">
      <div class="field"><span><code>/settings</code></span><small>群里弹出按钮面板</small></div>
      <div class="field"><span><code>/ban</code> <code>/ban 1d</code></span><small>封禁 / 临时封禁</small></div>
      <div class="field"><span><code>/mute</code> <code>/mute 2h</code></span><small>禁言 / 限时禁言</small></div>
      <div class="field"><span><code>/kick</code> <code>/warn</code></span><small>踢出 / 警告</small></div>
      <div class="field"><span><code>/unban</code></span><small>解封或解除禁言</small></div>
      <div class="field"><span><code>/del</code></span><small>删除回复的消息</small></div>
    </div></div>`;
  view.innerHTML = html;
};

const VERIFY = [["off", "关闭", "不做入群验证"], ["join", "入群申请验证", "推荐：群开启「需要审核」，验证后自动批准"], ["restrict", "进群先禁言", "开放群：验证后解除，超时踢出"]];
const CAPTCHAS = [["image", "图片验证码", "动态数字图 + 数字键盘（推荐）"], ["math", "算术题", "选出正确答案"], ["emoji", "点表情", "按文字点对应表情"], ["button", "点按钮", "最简单，群里直接点"], ["web", "网页验证", "Turnstile，需要域名"]];
const FILTER_ACTIONS = [["warn", "删除并警告"], ["delete", "只删除"], ["mute", "禁言 24 小时"], ["ban", "封禁"]];
const AI_ACTIONS = [["ban", "封禁"], ["mute", "禁言 24 小时"], ["warn", "删除并警告"]];
const ACTIONS = [["mute", "禁言 24 小时"], ["kick", "踢出"], ["ban", "封禁"]];
const PROTECT = [["cas", "CAS 黑名单", "进群时查共享黑名单"], ["profile_check", "入群资料检查", "昵称 / 简介带引流词、屏蔽词直接拒绝"], ["dup_check", "重复内容检测", "拦截复制粘贴的广告波"], ["night_on", "夜间模式", "时段内全员禁言（管理员除外）"]];
const LOCKS = [["lock_link", "链接"], ["lock_forward", "外部转发"], ["lock_button", "按钮消息"], ["lock_via_bot", "inline 机器人"], ["lock_story", "动态"], ["lock_sticker", "贴纸"], ["lock_gif", "GIF"], ["lock_media", "图片 / 视频 / 文件"], ["lock_voice", "语音 / 圆视频"], ["lock_invite", "其它群邀请链接"], ["lock_contact", "名片 / 位置"], ["lock_poll", "投票"], ["lock_game", "骰子 / 游戏"], ["lock_foreign", "外文消息", "阿拉伯文、俄文等为主的消息"]];

const toggle = (g, [k, n, d]) => `<label class="tg"><span class="t"><b>${n}</b>${d ? `<small>${d}</small>` : ""}</span><input type="checkbox" data-k="${k}" ${g[k] ? "checked" : ""}><span class="sw"></span></label>`;
const numField = (g, k, n, unit, min, max, hint) => `<label class="field"><span>${n}</span><div class="num"><input type="number" data-k="${k}" min="${min}" max="${max}" value="${esc(g[k])}"><em>${unit}</em></div>${hint ? `<small>${hint}</small>` : ""}</label>`;

PAGES.group = async () => {
  const groupID = state.gid;
  const g = await oai("GET", "/groups/" + groupID);
  const caps = state.info || (await oai("GET", "/status").catch(() => ({})));
  if (state.tab !== "group" || state.gid !== groupID) return;
  view.innerHTML = `
    ${!caps.captcha && g.captcha_type === "web" && g.verify_mode !== "off" ? `<div class="notice">${icon("alert")}<div>网页验证需要配置 Turnstile 和域名，现在不会生效。换成图片验证码等内置方式就行，不需要任何配置。</div></div>` : ""}
    <div class="card" id="f-verify"><div class="card-h"><div><h2>入群验证</h2><p>新人怎么证明自己不是机器人</p></div></div>
      <div class="seg">${VERIFY.map(([v, n, d]) => `<label><input type="radio" name="vm" data-k="verify_mode" value="${v}" ${g.verify_mode === v ? "checked" : ""}><b>${n}</b><small>${d}</small></label>`).join("")}</div>
      <div class="card-h" style="margin:18px 0 10px"><div><h2 style="font-size:14px">验证题目</h2><p>新人在私聊里完成（点按钮类型在群里直接点）；答错 3 次作废</p></div></div>
      <div class="seg">${CAPTCHAS.map(([v, n, d]) => `<label><input type="radio" name="ct" data-k="captcha_type" value="${v}" ${g.captcha_type === v ? "checked" : ""}><b>${n}</b><small>${d}${v === "web" && !caps.captcha ? "（未配置）" : ""}</small></label>`).join("")}</div>
      <div class="fields" style="margin-top:14px">${numField(g, "verify_timeout", "验证超时", "秒", 30, 86400, "超时拒绝申请 / 踢出")}</div>
    </div>
    <div class="card"><div class="card-h"><div><h2>AI 审核 ${caps.ai ? pill("ok", "已连接 " + esc(caps.ai_model || "")) : pill("warn", "未配置")}</h2><p>${caps.ai ? "用模型判断广告、博彩、诈骗等垃圾消息" : "在左侧「AI 设置」里填写接口地址、密钥和模型"}</p></div></div>
      <div class="toggles">${toggle(g, ["ai", "开启 AI 审核", "可疑消息交给 AI 判断"])}${toggle(g, ["ai_all", "审核所有人", "关闭时只审新人的前几条消息（省钱）"])}</div>
      <div class="fields" style="margin-top:14px"><label class="field"><span>判定为垃圾后</span><select data-k="ai_action">${AI_ACTIONS.map(([v, n]) => `<option value="${v}" ${g.ai_action === v ? "selected" : ""}>${n}</option>`).join("")}</select></label></div>
      <div class="card-h" style="margin:18px 0 10px"><div><h2 style="font-size:14px">测试一下</h2><p>输入一段文字，看 AI 会不会拦</p></div></div>
      <div class="row"><input type="text" id="aitext" class="grow" placeholder="例：加我微信 xxx，日结 500，稳赚不赔"><button id="aibtn">测试</button></div>
      <div id="aires" style="margin-top:10px"></div>
    </div>
    <div class="cols">
      <div class="card"><div class="card-h"><div><h2>防护</h2><p>自动拦截广告和刷屏</p></div></div>${g.ai && !g.ai_all && !g.newbie_msgs ? `<div class="notice warn" style="margin-bottom:12px">${icon("alert")}<div>AI 审核开着，但「新人严格检查」是 0 条、也没开「所有人都审」，所以 AI 实际不会审任何消息。把下面「新人严格检查」设成 5 左右。</div></div>` : ""}
        <div class="toggles" style="grid-template-columns:1fr">${PROTECT.map((x) => toggle(g, x)).join("")}</div>
        <div class="row" style="margin-top:12px"><span class="muted">夜间时段</span><input type="time" data-k="night_start" value="${esc(g.night_start)}"><span class="muted">至</span><input type="time" data-k="night_end" value="${esc(g.night_end)}"></div>
      </div>
      <div class="card"><div class="card-h"><div><h2>阈值与处罚</h2><p>0 表示关闭</p></div></div>
        <div class="fields">
          ${numField(g, "flood_count", "刷屏：条数", "条", 0, 100)}
          ${numField(g, "flood_second", "刷屏：时间窗", "秒", 1, 600)}
          ${numField(g, "warn_limit", "警告上限", "次", 1, 20)}
          ${numField(g, "warn_expire", "警告有效期", "小时", 0, 8760, "0 表示永久有效")}
          <label class="field"><span>警告满后</span><select data-k="warn_action">${ACTIONS.map(([v, n]) => `<option value="${v}" ${g.warn_action === v ? "selected" : ""}>${n}</option>`).join("")}</select></label>
          ${numField(g, "newbie_msgs", "新人严格检查", "条", 0, 100, "前 N 条消息禁链接 + AI 审核")}
          ${numField(g, "raid_limit", "防爆群", "人/分", 0, 500, "超过后 15 分钟拒绝新人")}
        </div>
      </div>
    </div>
    <div class="card" style="margin-top:16px"><div class="card-h"><div><h2>锁定</h2><p>打开后普通成员不能发这类消息（管理员不受限）</p></div></div>
      <div class="toggles">${LOCKS.map((x) => toggle(g, x)).join("")}</div></div>
    <div class="cols">
      <div class="card"><div class="card-h"><div><h2>欢迎语</h2><p><code>{name}</code> 新人名字，<code>{group}</code> 群名；末尾每行写 <code>按钮文字 - https://链接</code> 生成按钮，同行用 <code>|</code> 分隔</p></div></div>
        <label class="field"><textarea data-k="welcome" placeholder="欢迎 {name} 加入 {group}！&#10;群规 - https://t.me/xxx/1 | 官网 - https://example.com">${esc(g.welcome)}</textarea></label>
        <div class="fields" style="margin-top:12px">${numField(g, "welcome_ttl", "自动删除", "秒", 0, 86400, "0 表示不删")}</div>
        <div class="toggles" style="grid-template-columns:1fr;margin-top:12px">${toggle(g, ["clean_welcome", "只保留最新一条欢迎", "新人进来时删掉上一条"])}</div></div>
      <div class="card"><div class="card-h"><div><h2>群规</h2><p>群友发 /rules 查看</p></div></div>
        <label class="field"><textarea data-k="rules" placeholder="1. 禁止广告&#10;2. 友善交流">${esc(g.rules)}</textarea></label></div>
    </div>
    <div class="card" style="margin-top:16px" id="sendcard"><div class="card-h"><div><h2>以机器人身份发消息</h2><p>纯文字，发到当前群</p></div></div>
      <div class="row"><textarea id="sendtext" class="grow" style="min-height:60px" placeholder="输入要发送的内容"></textarea><button id="sendbtn">发送</button></div></div>`;
  bindForm(view, g, (diff) => oai("PATCH", "/groups/" + groupID, diff));
  const others = state.groups.filter((x) => String(x.id) !== String(groupID));
  view.insertAdjacentHTML("beforeend", `<div class="card" style="margin-top:16px"><div class="card-h"><div><h2>复制与备份</h2><p>把本群设置一键复制到其它群，或导出成文件备份</p></div></div>
    ${others.length ? `<div class="checks">${others.map((x) => `<label class="chk"><input type="checkbox" class="cpg" value="${x.id}"><span>${esc(x.title || x.id)}</span></label>`).join("")}</div>
    <div class="row" style="margin-top:12px"><label class="chk"><input type="checkbox" id="cpf" checked><span>同时复制屏蔽词</span></label><label class="chk"><input type="checkbox" id="cpt"><span>同时复制欢迎语和群规</span></label><span class="grow"></span><button class="pri" id="cpgo">复制到选中的群</button></div>` : `<p class="muted">机器人只在这一个群里，加入更多群后可以在这里同步设置。</p>`}
    <div class="row" style="margin-top:16px;padding-top:16px;border-top:1px solid var(--line)"><button id="exp">导出设置</button><label class="btn" for="impf">导入设置</label><input type="file" id="impf" accept=".json,application/json" hidden><span class="muted">导出文件包含全部设置和屏蔽词，可以导入到任意群</span></div></div>`);
  on("#cpgo", async () => {
    const targets = $$(".cpg").filter((x) => x.checked).map((x) => Number(x.value));
    if (!targets.length) throw new Error("先勾选要复制到的群");
    const filters = $("#cpf").checked, text = $("#cpt").checked;
    if (!(await ask(`复制到 ${targets.length} 个群？`, "这些群的对应设置会被覆盖成本群的设置。", { yes: "复制" }))) return;
    const r = await oai("POST", `/groups/${groupID}/copy`, { targets, filters, text });
    toast(`已复制到 ${r.groups} 个群${r.filters ? `，新增屏蔽词 ${r.filters} 条` : ""}`);
  });
  on("#exp", async () => {
    const data = await oai("GET", `/groups/${groupID}/export`);
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: "application/json" }));
    a.download = `tg-guard-${(g.title || groupID).replace(/[\\/:*?"<>|\s]+/g, "_")}.json`;
    document.body.appendChild(a); a.click(); a.remove();
    toast("已导出");
  });
  const impf = $("#impf");
  if (impf) impf.onchange = act(null, async () => {
    const f = impf.files[0];
    if (!f) return;
    let data;
    try { data = JSON.parse(await f.text()); } catch { throw new Error("文件不是有效的 JSON"); }
    impf.value = "";
    if (!(await ask("导入设置？", `本群设置会被 ${f.name} 覆盖，屏蔽词只增不删。`, { yes: "导入" }))) return;
    const r = await oai("POST", `/groups/${groupID}/import`, data);
    toast(`已导入 ${r.settings} 项设置，新增屏蔽词 ${r.filters} 条`);
    route();
  });
  const aitest = async () => {
    const text = $("#aitext").value.trim();
    if (!text) return;
    $("#aires").innerHTML = `<span class="muted">AI 判断中…</span>`;
    try {
      const r = await oai("POST", "/ai/test", { text });
      $("#aires").innerHTML = `${r.hit ? pill("bad", "会被拦截") : pill("ok", "放行")} <span class="muted">${esc(r.category || "")} · 置信度 ${Math.round((r.confidence || 0) * 100)}%（阈值 ${Math.round((r.threshold || 0) * 100)}%）· ${esc(r.reason || "")} · ${r.ms} ms</span>`;
    } catch (e) {
      $("#aires").innerHTML = `${pill("bad", "调用失败")} <span class="muted">${esc(e.message)}</span>`;
    }
  };
  on("#aibtn", aitest);
  onEnter("#aitext", aitest);
  on("#sendbtn", async () => {
    const text = $("#sendtext").value.trim();
    if (!text) return;
    if (!(await ask("发送到群里？", text.slice(0, 200), { yes: "发送" }))) return;
    await oai("POST", `/groups/${groupID}/send`, { text });
    if (state.tab === "group" && state.gid === groupID && $("#sendtext")) $("#sendtext").value = "";
    toast("已发送");
  });
};

PAGES.filters = async () => {
  const fs = await oai("GET", `/groups/${state.gid}/filters`);
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>添加屏蔽词</h2><p>普通词不区分大小写、包含即命中；正则按 Go 语法。昵称含普通屏蔽词的人进群会被拦截。</p></div></div>
      <div class="row"><input type="text" id="pat" class="grow" placeholder="屏蔽词或正则，回车添加"><select id="fa">${FILTER_ACTIONS.map(([v, n]) => `<option value="${v}">${n}</option>`).join("")}</select><label class="tg" style="padding:7px 12px"><span class="t">正则</span><input type="checkbox" id="rx"><span class="sw"></span></label><button class="pri" id="add">添加</button></div></div>
    <div class="card"><div class="card-h"><div><h2>已添加 <span class="pill">${fs.length}</span></h2></div></div>
      <div class="tbl-wrap"><table class="tbl"><tr><th>内容</th><th>类型</th><th>命中后</th><th></th></tr>
      ${fs.map((f) => `<tr><td><code>${esc(f.pattern)}</code></td><td>${f.regex ? pill("acc", "正则") : pill("", "关键词")}</td><td>${pill(f.action === "ban" ? "bad" : f.action === "mute" ? "warn" : "", (FILTER_ACTIONS.find((a) => a[0] === (f.action || "warn")) || [, "删除并警告"])[1])}</td><td class="act"><button class="sm danger" data-del="${f.id}">删除</button></td></tr>`).join("") || empty("inbox", "还没有屏蔽词", 4)}</table></div></div>`;
  const add = async () => {
    const pattern = $("#pat").value.trim();
    if (!pattern) return;
    await oai("POST", `/groups/${state.gid}/filters`, { pattern, regex: $("#rx").checked, action: $("#fa").value });
    toast("已添加");
    route();
  };
  on("#add", add);
  onEnter("#pat", act(null, add));
  $$("[data-del]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `/groups/${state.gid}/filters/${b.dataset.del}`); route(); })));
};

const preview = (c) => (c.msg_id ? pill("acc", "原消息") + " " : "") + esc((c.text || "").slice(0, 120));

PAGES.content = async () => {
  const [ns, rs] = await Promise.all([oai("GET", `/groups/${state.gid}/notes`), oai("GET", `/groups/${state.gid}/replies`)]);
  view.innerHTML = `<div class="cols">
    <div class="card"><div class="card-h"><div><h2>笔记 <span class="pill">${ns.length}</span></h2><p>群友发 <code>#名字</code> 查看。图片 / 带按钮的消息：群里回复它发 <code>/save 名字</code></p></div></div>
      <div class="stack"><input type="text" id="nn" placeholder="名字，如 faq" style="width:100%"><textarea id="nt" placeholder="内容"></textarea><button class="pri" id="na">保存笔记</button></div>
      <div class="tbl-wrap" style="margin-top:16px"><table class="tbl"><tr><th>名字</th><th>内容</th><th></th></tr>
      ${ns.map((n) => `<tr><td><code>#${esc(n.name)}</code></td><td class="clip">${preview(n)}</td><td class="act"><button class="sm danger" data-dn="${esc(n.name)}">删除</button></td></tr>`).join("") || empty("inbox", "暂无笔记", 3)}</table></div></div>
    <div class="card"><div class="card-h"><div><h2>自动回复 <span class="pill">${rs.length}</span></h2><p>消息包含关键词就回复，同一关键词 30 秒内只回一次</p></div></div>
      <div class="stack"><input type="text" id="rk" placeholder="关键词" style="width:100%"><textarea id="rt" placeholder="回复内容"></textarea><button class="pri" id="ra">保存回复</button></div>
      <div class="tbl-wrap" style="margin-top:16px"><table class="tbl"><tr><th>关键词</th><th>回复</th><th></th></tr>
      ${rs.map((r) => `<tr><td><b>${esc(r.keyword)}</b></td><td class="clip">${preview(r)}</td><td class="act"><button class="sm danger" data-dr="${esc(r.keyword)}">删除</button></td></tr>`).join("") || empty("inbox", "暂无自动回复", 3)}</table></div></div>
  </div>`;
  on("#na", async () => { await oai("POST", `/groups/${state.gid}/notes`, { name: $("#nn").value.trim(), text: $("#nt").value }); toast("已保存"); route(); });
  on("#ra", async () => { await oai("POST", `/groups/${state.gid}/replies`, { keyword: $("#rk").value.trim(), text: $("#rt").value }); toast("已保存"); route(); });
  $$("[data-dn]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `/groups/${state.gid}/notes`, null, { name: b.dataset.dn }); route(); })));
  $$("[data-dr]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `/groups/${state.gid}/replies`, null, { keyword: b.dataset.dr }); route(); })));
};

PAGES.schedules = async () => {
  const ss = await oai("GET", `/groups/${state.gid}/schedules`);
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>新建定时消息</h2><p>按机器人配置的时区，每天发送一次</p></div></div>
      <div class="row"><input type="time" id="at" value="09:00"><textarea id="st" class="grow" style="min-height:44px" placeholder="内容"></textarea><button class="pri" id="sa">添加</button></div></div>
    <div class="card"><div class="tbl-wrap"><table class="tbl"><tr><th>时间</th><th>内容</th><th>上次发送</th><th></th></tr>
      ${ss.map((s) => `<tr><td><b>${esc(s.at)}</b></td><td class="clip">${preview(s)}</td><td class="muted">${esc(s.last_run || "—")}</td><td class="act"><button class="sm danger" data-ds="${s.id}">删除</button></td></tr>`).join("") || empty("inbox", "还没有定时消息", 4)}</table></div></div>`;
  on("#sa", async () => { await oai("POST", `/groups/${state.gid}/schedules`, { at: $("#at").value, text: $("#st").value }); toast("已添加"); route(); });
  $$("[data-ds]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `/groups/${state.gid}/schedules/${b.dataset.ds}`); route(); })));
};

// ---------- AI 设置 ----------
PAGES.ai = async () => {
  const c = await oai("GET", "/ai");
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>AI 审核接口 ${c.enabled ? pill("ok", "已启用") : pill("warn", "未配置")}</h2><p>OpenAI 兼容接口（你的中转站、OpenAI、DeepSeek 等都行）。保存后立即生效，不用重启。${c.source === "config" && c.enabled ? "当前用的是配置文件里的设置，保存后以这里为准。" : ""}</p></div></div>
      <div class="fields">
        <label class="field"><span>接口地址</span><input type="text" id="ai_url" value="${esc(c.base_url)}" placeholder="https://你的中转站/v1"><small>填到 /v1 即可</small></label>
        <label class="field"><span>密钥</span><input type="password" id="ai_key" value="" placeholder="${c.has_key ? "已设置（" + esc(c.api_key) + "），不改就留空" : "sk-..."}" autocomplete="off"><small>${c.has_key ? '<a href="#" id="ai_clr">清空密钥</a>' : "只保存在机器人数据库里，面板不会回显"}</small></label>
        <label class="field"><span>模型</span><input type="text" id="ai_model" list="ai_models" value="${esc(c.model)}" placeholder="gpt-4o-mini"><datalist id="ai_models"></datalist><small><a href="#" id="ai_ls">从接口读取模型列表</a></small></label>
        <label class="field"><span>判定阈值</span><div class="num"><input type="number" id="ai_th" min="0.5" max="1" step="0.05" value="${c.threshold || 0.8}"><em>0.5 ~ 1</em></div><small>置信度达到多少才处理，越高越不容易误杀</small></label>
      </div>
      <div class="row" style="margin-top:16px"><button class="pri" id="ai_save">保存</button></div>
    </div>
    <div class="card"><div class="card-h"><div><h2>测试一下</h2><p>保存后输入一段文字，看 AI 会不会拦</p></div></div>
      <div class="row"><input type="text" id="aitext2" class="grow" placeholder="例：加我微信 xxx，日结 500，稳赚不赔"><button id="aibtn2">测试</button></div>
      <div id="aires2" style="margin-top:10px"></div>
    </div>
    <div class="notice">${icon("alert")}<div>接口配好后，还要到每个群的「群设置 → AI 审核」里打开开关才会生效。默认只审新人的前几条消息，比较省钱。</div></div>`;
  let clear = false;
  const clr = $("#ai_clr");
  if (clr) clr.onclick = (e) => { e.preventDefault(); clear = true; $("#ai_key").placeholder = "保存后会清空密钥"; };
  $("#ai_ls").onclick = act(null, async (e) => {
    e.preventDefault();
    const r = await oai("GET", "/ai/models");
    $("#ai_models").innerHTML = (r.models || []).map((m) => `<option value="${esc(m)}">`).join("");
    toast(`读到 ${(r.models || []).length} 个模型，点模型输入框选择`);
  });
  on("#ai_save", async () => {
    const body = { base_url: $("#ai_url").value.trim(), model: $("#ai_model").value.trim(), threshold: Number($("#ai_th").value) || 0.8 };
    const k = $("#ai_key").value.trim();
    if (k) body.api_key = k; else if (clear) body.api_key = "";
    const r = await oai("POST", "/ai", body);
    state.info = null;
    oai("GET", "/status").then((o) => (state.info = o)).catch(() => {});
    toast(r.enabled ? "已保存，AI 审核接口可用" : "已保存（地址、密钥、模型都填了才会启用）");
    route();
  });
  const t = async () => {
    const text = $("#aitext2").value.trim();
    if (!text) return;
    $("#aires2").innerHTML = `<span class="muted">AI 判断中…</span>`;
    try {
      const r = await oai("POST", "/ai/test", { text });
      $("#aires2").innerHTML = `${r.hit ? pill("bad", "会被拦截") : pill("ok", "放行")} <span class="muted">${esc(r.category || "")} · 置信度 ${Math.round((r.confidence || 0) * 100)}% · ${esc(r.reason || "")} · ${r.ms} ms</span>`;
    } catch (e) { $("#aires2").innerHTML = `${pill("bad", "调用失败")} <span class="muted">${esc(e.message)}</span>`; }
  };
  on("#aibtn2", t);
  onEnter("#aitext2", t);
};

// ---------- 定时广告 ----------
function readImage(file) {
  return new Promise((ok, bad) => {
    const fr = new FileReader();
    fr.onerror = () => bad(new Error("读取图片失败"));
    fr.onload = () => {
      if (file.type === "image/gif") return ok(fr.result);
      const img = new Image();
      img.onerror = () => bad(new Error("图片无法解析"));
      img.onload = () => {
        const k = Math.min(1, 1600 / Math.max(img.width, img.height));
        const c = document.createElement("canvas");
        c.width = Math.round(img.width * k); c.height = Math.round(img.height * k);
        c.getContext("2d").drawImage(img, 0, 0, c.width, c.height);
        ok(c.toDataURL("image/jpeg", 0.85));
      };
      img.src = fr.result;
    };
    fr.readAsDataURL(file);
  });
}

PAGES.ads = async () => {
  const groupID = state.gid;
  const as = await oai("GET", `/groups/${groupID}/ads`);
  const when = (a) => (a.times ? "每天 " + a.times : a.interval % 60 === 0 ? `每 ${a.interval / 60} 小时` : `每 ${a.interval} 分钟`);
  const ed = state.adEdit ? as.find((a) => a.id === state.adEdit) : null;
  const e = ed || { name: "", text: "", photo: "", buttons: "", times: "", interval: 120, enabled: true, delete_prev: true, pin: false };
  const copied = ed && ed.msg_id;
  const chk = (id, v, t, d) => `<label class="tg"><span class="t"><b>${t}</b><small>${d}</small></span><input type="checkbox" id="${id}" ${v ? "checked" : ""}><span class="sw"></span></label>`;
  view.innerHTML = `<div class="card"><div class="tbl-wrap"><table class="tbl"><tr><th>广告</th><th>时间</th><th>状态</th><th class="r">已发</th><th></th></tr>
      ${as.map((a) => `<tr><td><b>${esc(a.name)}</b> ${a.photo || a.msg_id ? '<span class="pill">图</span>' : ""} ${a.buttons ? '<span class="pill">按钮</span>' : ""}<br><div class="muted clip" style="margin-top:2px">${esc((a.text || "").slice(0, 60)) || "（复制原消息）"}</div></td>
        <td>${esc(when(a))}</td><td>${a.enabled ? pill("ok", "启用") : pill("", "暂停")}</td><td class="r">${a.sent}</td>
        <td class="act"><button class="sm" data-ae="${a.id}">编辑</button> <button class="sm" data-at="${a.id}">${a.enabled ? "暂停" : "启用"}</button> <button class="sm" data-ap="${a.id}">预览</button> <button class="sm" data-as="${a.id}">立即发送</button> <button class="sm danger" data-ad="${a.id}">删除</button></td></tr>`).join("") || empty("inbox", "还没有定时广告", 5)}</table></div></div>
    <div class="card"><div class="card-h"><div><h2>${ed ? "编辑广告 #" + ed.id : "新建广告"}</h2><p>${copied ? "这条是在群里用 /ad 回复消息创建的，发出时复制原消息；改了文字或图片后按下面的内容发。" : "支持 Markdown、图片和按钮"}</p></div></div>
      <div class="fields">
        <label class="field"><span>名字</span><input type="text" id="ad_name" value="${esc(e.name)}" placeholder="方便自己区分"></label>
        <label class="field"><span>每天固定时间</span><input type="text" id="ad_times" value="${esc(e.times)}" placeholder="09:00,21:00"><small>填了就忽略间隔</small></label>
        <label class="field"><span>或每隔</span><div class="num"><input type="number" id="ad_int" min="10" max="10080" value="${e.interval || 120}"><em>分钟</em></div><small>10 ~ 10080</small></label>
      </div>
      <div class="field" style="margin-top:14px"><span>内容（Markdown）</span><textarea id="ad_text" rows="7" placeholder="选中文字后点上面的按钮加效果，Ctrl+B 粗体、Ctrl+I 斜体、Ctrl+U 下划线">${esc(e.text)}</textarea><small>带图片时最多 1024 字（Telegram 限制）</small></div>
      <div class="field" style="margin-top:14px"><span>图片 ${e.photo ? pill("acc", "已有图片") : ""}</span>
        <div class="row"><label class="filebtn"><input type="file" id="ad_file" accept="image/*">${icon("upload", 16)}<span id="ad_fname">上传图片</span></label><input type="text" id="ad_photo" class="grow" value="${esc(e.photo && !e.photo.startsWith("local:") ? e.photo : "")}" placeholder="或填图片链接 https://…">${e.photo ? `<label class="tg" style="padding:7px 12px"><span class="t"><b>去掉图片</b></span><input type="checkbox" id="ad_nophoto"><span class="sw"></span></label>` : ""}</div>
        <small>上传会自动压缩到 1600px 以内</small></div>
      <label class="field" style="margin-top:14px"><span>按钮</span><textarea id="ad_btn" rows="3" placeholder="官网 - https://example.com | 客服 - https://t.me/xxx">${esc(e.buttons)}</textarea><small>每行一排，写成「文字 - 链接」，同一排多个用 | 隔开</small></label>
      <div class="toggles" style="margin-top:14px">${chk("ad_on", e.enabled, "启用", "按时间自动发送")}${chk("ad_dp", e.delete_prev, "删掉上一条", "发新的之前删除上次发的")}${chk("ad_pin", e.pin, "发送后置顶", "静默置顶，需要置顶权限")}</div>
      <div class="row" style="margin-top:16px"><button class="pri" id="ad_save">${ed ? "保存修改" : "添加"}</button><button id="ad_prev">私聊预览</button>${ed ? '<button id="ad_cancel">取消编辑</button>' : ""}<span class="muted">预览会私聊发给机器人主人和本群管理员白名单，不发到群里</span></div>
    </div>`;
  const formBody = async () => {
    const body = { name: $("#ad_name").value, times: $("#ad_times").value, interval: Number($("#ad_int").value), buttons: $("#ad_btn").value,
      enabled: $("#ad_on").checked, delete_prev: $("#ad_dp").checked, pin: $("#ad_pin").checked };
    if (!copied || $("#ad_text").value !== e.text) body.text = $("#ad_text").value;
    const f = $("#ad_file").files[0];
    const url = $("#ad_photo").value.trim();
    if (f) body.photo_data = await readImage(f);
    else if ($("#ad_nophoto") && $("#ad_nophoto").checked) body.photo = "";
    else if (url && url !== e.photo) body.photo = url;
    return body;
  };
  mdEditor($("#ad_text"));
  $("#ad_file").onchange = () => { const f = $("#ad_file").files[0]; $("#ad_fname").textContent = f ? f.name : "上传图片"; };
  on("#ad_prev", async () => {
    const body = await formBody();
    if (ed) body.id = ed.id;
    const r = await oai("POST", `/groups/${groupID}/ad-preview`, body);
    toast(`已私聊发给 ${r.sent} 人` + (r.failed && r.failed.length ? `，${r.failed.length} 人没发出去（需先私聊机器人 /start）` : ""));
  });
  on("#ad_save", async () => {
    const body = await formBody();
    if (ed) await oai("PATCH", `/groups/${groupID}/ads/${ed.id}`, body);
    else await oai("POST", `/groups/${groupID}/ads`, body);
    state.adEdit = null;
    toast("已保存");
    route();
  });
  on("#ad_cancel", async () => { state.adEdit = null; route(); });
  $$("[data-ae]").forEach((b) => (b.onclick = () => { state.adEdit = Number(b.dataset.ae); route(); }));
  $$("[data-at]").forEach((b) => (b.onclick = act(b, async () => {
    const a = as.find((x) => x.id === Number(b.dataset.at));
    await oai("PATCH", `/groups/${groupID}/ads/${a.id}`, { enabled: !a.enabled });
    route();
  })));
  $$("[data-ap]").forEach((b) => (b.onclick = act(b, async () => {
    const r = await oai("POST", `/groups/${groupID}/ad-preview`, { id: Number(b.dataset.ap) });
    toast(`预览已私聊发给 ${r.sent} 人` + (r.failed && r.failed.length ? `，${r.failed.length} 人没发出去` : ""));
  })));
  $$("[data-as]").forEach((b) => (b.onclick = act(b, async () => {
    if (!(await ask("现在发送？", "立即把这条广告发到群里。", { yes: "发送" }))) return;
    await oai("POST", `/groups/${groupID}/ads/${b.dataset.as}/send`, {});
    toast("已发送");
    route();
  })));
  $$("[data-ad]").forEach((b) => (b.onclick = act(b, async () => {
    if (!(await ask("删除这条广告？", "", { danger: true, yes: "删除" }))) return;
    await oai("DELETE", `/groups/${groupID}/ads/${b.dataset.ad}`);
    route();
  })));
};


// ---------- Markdown 编辑器（工具栏 + 预览） ----------
// 规则与后端 MarkdownToHTML 保持一致
function mdToHtml(md) {
  let s = esc(String(md || "").replace(/\r\n/g, "\n"));
  const keep = [];
  const hold = (v) => { keep.push(v); return `\u0000${keep.length - 1}\u0000`; };
  s = s.replace(/```(?:[a-zA-Z0-9_+-]*\n)?([\s\S]*?)```/g, (_, c) => hold(`<pre>${c.replace(/\n+$/, "")}</pre>`));
  s = s.replace(/`([^`\n]+)`/g, (_, c) => hold(`<code>${c}</code>`));
  s = s.replace(/\[([^\]\n]+)\]\(((?:https?|tg):\/\/[^)\s]+)\)/g, (_, t, u) => hold(`<a href="${u}" target="_blank" rel="noopener">`) + t + hold("</a>"));
  s = s.replace(/^#{1,6}\s+(.+)$/gm, "<b>$1</b>");
  s = s.replace(/\*\*([^*\n]+?)\*\*|__([^_\n]+?)__/g, (_, a, b) => `<b>${a || b}</b>`);
  s = s.replace(/(^|[^\w*])\*([^*\n]+?)\*(?=$|[^\w*])/gm, "$1<i>$2</i>").replace(/(^|[^\w])_([^_\n]+?)_(?=$|[^\w])/gm, "$1<i>$2</i>");
  s = s.replace(/~~([^~\n]+?)~~/g, "<s>$1</s>").replace(/\|\|([^|\n]+?)\|\|/g, '<span class="spoiler">$1</span>').replace(/\+\+([^+\n]+?)\+\+/g, "<u>$1</u>");
  s = s.replace(/(?:^&gt; ?.*(?:\n|$))+/gm, (m) => `<blockquote>${m.replace(/\n$/, "").split("\n").map((l) => l.replace(/^&gt; ?/, "")).join("\n")}</blockquote>${m.endsWith("\n") ? "\n" : ""}`);
  for (let i = keep.length - 1; i >= 0; i--) s = s.split(`\u0000${i}\u0000`).join(keep[i]);
  return s.replace(/\n/g, "<br>");
}

const MD_TOOLS = [
  ["B", "粗体", "**", "**", "font-weight:700"],
  ["I", "斜体", "*", "*", "font-style:italic"],
  ["U", "下划线", "++", "++", "text-decoration:underline"],
  ["S", "删除线", "~~", "~~", "text-decoration:line-through"],
  ["剧透", "剧透（点开才显示）", "||", "||", ""],
  ["</>", "行内代码", "`", "`", "font-family:ui-monospace,Menlo,monospace"],
  ["代码块", "代码块", "```\n", "\n```", ""],
  ["链接", "链接", "[", "](https://)", ""],
  ["引用", "引用（每行前加 >）", "> ", "", ""],
  ["标题", "标题（整行加粗）", "# ", "", ""],
];

function mdEditor(ta) {
  if (!ta || ta.dataset.md) return;
  ta.dataset.md = "1";
  const bar = document.createElement("div");
  bar.className = "mdbar";
  bar.innerHTML = MD_TOOLS.map(([t, tip, , , st], i) => `<button type="button" data-mi="${i}" title="${tip}" style="${st}">${esc(t)}</button>`).join("") +
    `<span class="grow"></span><button type="button" data-mp class="mdprev-btn">预览</button>`;
  const prev = document.createElement("div");
  prev.className = "mdpreview";
  prev.hidden = true;
  ta.before(bar);
  ta.after(prev);
  const render = () => { prev.innerHTML = mdToHtml(ta.value) || '<span class="muted">（空）</span>'; };
  ta.addEventListener("input", () => { if (!prev.hidden) render(); });
  bar.addEventListener("mousedown", (e) => e.preventDefault()); // 点按钮不让输入框失去选区
  bar.addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (!b) return;
    if (b.dataset.mp !== undefined) {
      prev.hidden = !prev.hidden;
      b.classList.toggle("on", !prev.hidden);
      if (!prev.hidden) render();
      return;
    }
    const [, tip, pre, post] = MD_TOOLS[Number(b.dataset.mi)];
    const st = ta.selectionStart, en = ta.selectionEnd, v = ta.value;
    let sel = v.slice(st, en);
    let out, cs, ce;
    if (pre === "> " || pre === "# ") {
      // 行级：给选中的每一行加前缀；再点一次去掉
      const ls = v.lastIndexOf("\n", st - 1) + 1;
      const le = v.indexOf("\n", en) === -1 ? v.length : v.indexOf("\n", en);
      const lines = v.slice(ls, le).split("\n");
      const has = lines.every((l) => l.startsWith(pre));
      const block = lines.map((l) => (has ? l.slice(pre.length) : pre + l)).join("\n");
      ta.setRangeText(block, ls, le, "select");
    } else {
      const wrapped = v.slice(st - pre.length, st) === pre && v.slice(en, en + post.length) === post;
      if (wrapped) { // 已经有效果：再点一次取消
        ta.setRangeText(sel, st - pre.length, en + post.length, "select");
      } else {
        if (!sel) sel = pre === "[" ? "链接文字" : tip.replace(/（.*/, "");
        out = pre + sel + post;
        ta.setRangeText(out, st, en, "end");
        cs = st + pre.length; ce = cs + sel.length;
        if (pre === "[") { cs = st + pre.length + sel.length + 2; ce = cs + 8; } // 选中 https:// 方便直接粘贴
        ta.setSelectionRange(cs, ce);
      }
    }
    ta.focus();
    ta.dispatchEvent(new Event("input"));
  });
  ta.addEventListener("keydown", (e) => {
    if (!(e.ctrlKey || e.metaKey)) return;
    const k = { b: 0, i: 1, u: 2 }[e.key.toLowerCase()];
    if (k === undefined) return;
    e.preventDefault();
    bar.querySelector(`[data-mi="${k}"]`).click();
  });
}

// ---------- 处罚记录 ----------
const PUN_ACT = { ban: ["bad", "封禁"], mute: ["warn", "禁言"], kick: ["", "踢出"] };
const PUN_SRC = { ai: "AI 判定", learned: "已学习样本", rule: "规则", warn: "警告满", flood: "刷屏", admin: "管理员", report: "举报", join: "入群检查", other: "其它" };
const userCell = (id, name) => `${esc(name || "") || '<span class="muted">未知</span>'}<div class="muted" style="font-size:12px"><code>${id}</code></div>`;

PAGES.punish = async () => {
  const groupID = state.gid;
  state.pst = state.pst || "active";
  const q = state.pq || "";
  const ps = await oai("GET", `/groups/${groupID}/punishments`, null, { status: state.pst, q, limit: 200 });
  view.innerHTML = `<div class="card"><div class="card-h"><div class="tabs">${[["active", "生效中"], ["undone", "已撤销"], ["all", "全部"]].map(([k, n]) => `<button class="${state.pst === k ? "on" : ""}" data-ps="${k}">${n}</button>`).join("")}</div>
      <div class="row" style="flex:1;justify-content:flex-end"><input type="text" id="pq" value="${esc(q)}" placeholder="搜用户 ID、名字、原因、消息" style="width:260px"><button id="pqb">搜索</button></div></div>
      <div class="notice info" style="margin-bottom:14px">${icon("alert")}<div>误封了就点「撤销」：封禁会解除（对方需要重新进群），禁言会恢复发言。勾上「让 AI 记住」，那条消息会存为正常样本，以后类似的不再拦。</div></div>
      <div class="tbl-wrap"><table class="tbl"><tr><th>时间</th><th>用户</th><th>处罚</th><th>原因</th><th>消息</th><th></th></tr>
      ${ps.map((p) => `<tr><td class="muted" style="white-space:nowrap">${fmtTime(p.created_at)}</td><td>${userCell(p.user_id, p.name)}</td>
        <td>${pill(...(PUN_ACT[p.action] || ["", p.action]))}${p.minutes ? `<div class="muted" style="font-size:12px">${fmtDur(p.minutes * 60)}</div>` : ""}</td>
        <td><span class="pill">${PUN_SRC[p.source] || esc(p.source)}</span><div class="muted clip" style="max-width:240px;font-size:12px;margin-top:2px" title="${esc(p.reason)}">${esc(p.reason) || "—"}${p.operator ? " · " + esc(p.operator) : ""}</div></td>
        <td class="clip" style="max-width:260px" title="${esc(p.text)}">${esc(p.text) || '<span class="muted">—</span>'}</td>
        <td class="act">${p.undone ? `<span class="muted" style="font-size:12px">已撤销${p.undone_by ? "（" + esc(p.undone_by) + "）" : ""}</span>` : `<button class="sm" data-pu="${p.id}">撤销</button>`}</td></tr>`).join("") || empty("inbox", state.pst === "active" ? "没有生效中的处罚" : "没有记录", 6)}</table></div>
      <p class="muted" style="font-size:12px;margin:10px 0 0">从这个版本开始记录；之前的处罚可以在「白名单与封禁」里按用户 ID 解封。</p></div>`;
  $$("[data-ps]").forEach((b) => (b.onclick = () => { state.pst = b.dataset.ps; route(); }));
  const search = () => { state.pq = $("#pq").value.trim(); route(); };
  on("#pqb", search);
  onEnter("#pq", search);
  $$("[data-pu]").forEach((b) => (b.onclick = act(b, async () => {
    const p = ps.find((x) => x.id === Number(b.dataset.pu));
    const box = document.createElement("div");
    box.innerHTML = `<label class="tg" style="margin-top:10px"><span class="t"><b>让 AI 记住这不是广告</b><small>${p.text ? "把那条消息存为正常样本" : "这条处罚没有消息内容，学不了"}</small></span><input type="checkbox" id="u_learn" ${p.text ? "checked" : "disabled"}><span class="sw"></span></label>
      <label class="tg" style="margin-top:8px"><span class="t"><b>加入免检白名单</b><small>以后不再检查这个人</small></span><input type="checkbox" id="u_ok"><span class="sw"></span></label>`;
    if (!(await ask(`撤销对 ${p.name || p.user_id} 的${(PUN_ACT[p.action] || [, "处罚"])[1]}？`, box, { yes: "撤销" }))) return;
    await oai("POST", `/groups/${groupID}/punishments/${p.id}/undo`, { learn: !!$("#u_learn", box)?.checked, approve: !!$("#u_ok", box)?.checked });
    toast(p.action === "ban" ? "已解封，对方需要重新进群" : "已撤销");
    route();
  })));
};

// ---------- 成员列表 ----------
PAGES.people = async () => {
  const groupID = state.gid;
  state.mf = state.mf || "in";
  const page = state.mp || 0, q = state.mq || "", size = 50;
  const r = await oai("GET", `/groups/${groupID}/members`, null, { q, filter: state.mf, limit: size, offset: page * size });
  const pages = Math.max(1, Math.ceil(r.total / size));
  const nm = (m) => [m.first_name, m.last_name].filter(Boolean).join(" ");
  view.innerHTML = `<div class="card"><div class="card-h"><div class="tabs">${[["in", "在群里"], ["new", "新人"], ["warned", "有警告"], ["left", "已退群"], ["all", "全部"]].map(([k, n]) => `<button class="${state.mf === k ? "on" : ""}" data-mf="${k}">${n}</button>`).join("")}</div>
      <div class="row" style="flex:1;justify-content:flex-end"><input type="text" id="mq" value="${esc(q)}" placeholder="搜用户 ID、名字、@用户名" style="width:240px"><button id="mqb">搜索</button></div></div>
      <div class="tbl-wrap"><table class="tbl"><tr><th>成员</th><th>用户名</th><th class="r">发言</th><th class="r">警告</th><th>入群</th><th>状态</th><th></th></tr>
      ${r.items.map((m) => `<tr><td>${userCell(m.user_id, nm(m))}</td><td>${m.username ? "@" + esc(m.username) : '<span class="muted">—</span>'}</td><td class="r">${m.msg_count}</td>
        <td class="r">${m.warns ? pill("warn", m.warns) : '<span class="muted">0</span>'}</td><td class="muted" style="white-space:nowrap">${fmtTime(m.joined_at)}</td>
        <td>${m.left ? pill("", "已退群") : m.newcomer && !m.verified ? pill("warn", "未验证") : m.newcomer ? pill("acc", "新人") : pill("ok", "成员")}</td>
        <td class="act"><select class="sm" data-ma="${m.user_id}" style="min-height:30px;padding:4px 28px 4px 10px;font-size:12.5px"><option value="">操作…</option><option value="mute:60">禁言 1 小时</option><option value="mute:1440">禁言 24 小时</option><option value="unmute">解除禁言</option><option value="kick">踢出</option><option value="ban">封禁</option><option value="unban">解封</option><option value="clearwarn">清空警告</option><option value="approve">加免检白名单</option><option value="unapprove">移出免检白名单</option></select></td></tr>`).join("") || empty("users", "没有符合条件的成员", 7)}</table></div>
      <div class="row" style="margin-top:12px"><span class="muted grow">共 ${r.total} 人 · 只有机器人见过的成员（发过言或进群时机器人在）</span><button class="sm" id="mprev" ${page ? "" : "disabled"}>上一页</button><span class="muted">${page + 1} / ${pages}</span><button class="sm" id="mnext" ${page + 1 < pages ? "" : "disabled"}>下一页</button></div></div>`;
  $$("[data-mf]").forEach((b) => (b.onclick = () => { state.mf = b.dataset.mf; state.mp = 0; route(); }));
  const search = () => { state.mq = $("#mq").value.trim(); state.mp = 0; route(); };
  on("#mqb", search);
  onEnter("#mq", search);
  on("#mprev", async () => { state.mp = page - 1; route(); });
  on("#mnext", async () => { state.mp = page + 1; route(); });
  $$("[data-ma]").forEach((sel) => (sel.onchange = async () => {
    const [a, min] = sel.value.split(":");
    if (!a) return;
    const label = sel.selectedOptions[0].textContent;
    sel.value = "";
    if (["ban", "kick"].includes(a) && !(await ask(`${label} ${sel.dataset.ma}？`, "", { danger: true, yes: label }))) return;
    try {
      const x = await oai("POST", `/groups/${groupID}/members/${sel.dataset.ma}/action`, { action: a, minutes: Number(min) || 0 });
      toast(x.text || "已完成");
      route();
    } catch (e) { toast(e.message, true); }
  }));
};

// ---------- AI 学习 ----------
PAGES.learn = async () => {
  state.lk = state.lk || "spam";
  const xs = await oai("GET", "/samples");
  const list = xs.filter((x) => x.kind === state.lk);
  const n = { spam: xs.filter((x) => x.kind === "spam").length, ham: xs.filter((x) => x.kind === "ham").length };
  const SRC = { report: "举报处理", command: "群命令", panel: "面板", unban: "撤销误封" };
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>AI 怎么学</h2><p>所有群共用一套样本。和广告样本几乎一样的消息直接处理，不用等 AI；和误封样本很像的直接放行；最近的样本还会作为例子发给 AI，让它学到你的尺度。</p></div></div>
      <div class="toggles">
        <div class="tg" style="cursor:default"><span class="t"><b>举报处理</b><small>举报里点「删除 / 禁言 / 封禁」，那条消息自动记为广告</small></span></div>
        <div class="tg" style="cursor:default"><span class="t"><b>群里 /spam</b><small>回复广告发 /spam：删除、封禁、记住</small></span></div>
        <div class="tg" style="cursor:default"><span class="t"><b>撤销误封</b><small>撤销 AI 的处罚时，那条消息记为正常；群里 /notspam 也行</small></span></div>
      </div></div>
    <div class="card"><div class="card-h"><div class="tabs"><button class="${state.lk === "spam" ? "on" : ""}" data-lk="spam">广告样本 ${n.spam}</button><button class="${state.lk === "ham" ? "on" : ""}" data-lk="ham">正常样本 ${n.ham}</button></div></div>
      <div class="row"><input type="text" id="lt" class="grow" placeholder="${state.lk === "spam" ? "粘贴一条广告，例：洗米日赚八千，私聊带你" : "粘贴一条被误判的正常消息"}"><button class="pri" id="la">添加${state.lk === "spam" ? "广告" : "正常"}样本</button></div>
      <div class="tbl-wrap" style="margin-top:14px"><table class="tbl"><tr><th>#</th><th>内容</th><th>来源</th><th class="r">命中</th><th>时间</th><th></th></tr>
      ${list.map((x) => `<tr><td class="muted">${x.id}</td><td class="clip" style="max-width:420px" title="${esc(x.text)}">${esc(x.text)}</td><td><span class="pill">${SRC[x.source] || esc(x.source)}</span>${x.by ? `<div class="muted" style="font-size:12px">${esc(x.by)}</div>` : ""}</td><td class="r">${x.hits}</td><td class="muted" style="white-space:nowrap">${fmtTime(x.created_at)}</td><td class="act"><button class="sm danger" data-ld="${x.id}">删除</button></td></tr>`).join("") || empty("inbox", "还没有样本", 6)}</table></div></div>`;
  $$("[data-lk]").forEach((b) => (b.onclick = () => { state.lk = b.dataset.lk; route(); }));
  const add = async () => {
    const text = $("#lt").value.trim();
    if (!text) return;
    await oai("POST", "/samples", { kind: state.lk, text, group_id: Number(state.gid) || 0 });
    toast("已添加");
    route();
  };
  on("#la", add);
  onEnter("#lt", act(null, add));
  $$("[data-ld]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `/samples/${b.dataset.ld}`); route(); })));
};

// ---------- 主页查重 ----------
PAGES.bioscan = async () => {
  const groupID = state.gid;
  const [g, r] = await Promise.all([oai("GET", "/groups/" + groupID), oai("GET", `/groups/${groupID}/bioscan`)]);
  const rep = r.report;
  const clusters = rep ? JSON.parse(rep.clusters || "[]") : [];
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>主页链接查重 ${r.running ? pill("warn", "扫描中") : ""}</h2><p>扫描机器人见过的成员的简介、个人频道，找出多人共用同一链接的疑似广告号。只报告，封不封由你决定。</p></div></div>
      <div class="toggles">${toggle(g, ["bio_scan", "定时扫描", "按下面的间隔自动扫，结果发到日志频道"])}</div>
      <div class="fields" style="margin-top:14px">${numField(g, "bio_scan_hours", "扫描间隔", "小时", 1, 168)}${numField(g, "bio_scan_min", "最少重复人数", "人", 2, 100, "同一链接至少几个人才报告")}</div>
      <div class="row" style="margin-top:16px"><button class="pri" id="bs_now" ${r.running ? "disabled" : ""}>立即扫描</button><span class="muted">Telegram 不给机器人完整成员列表，只能扫描机器人见过的成员</span></div>
    </div>
    <div class="card"><div class="card-h"><div><h2>最近一次结果</h2><p>${rep ? `${fmtTime(rep.created_at)} · 扫描 ${rep.scanned} 人${rep.failed ? `，${rep.failed} 人读取失败` : ""} · ${clusters.length} 组重复` : "还没有扫描过"}</p></div></div>
      ${rep ? `<div class="tbl-wrap"><table class="tbl"><tr><th>共用链接</th><th class="r">人数</th><th>成员</th><th></th></tr>
      ${clusters.map((c, i) => `<tr><td class="clip"><b>${esc(c.link)}</b></td><td class="r">${pill("warn", c.users.length)}</td><td class="muted">${c.users.map((u) => `${esc(u.name)}${u.username ? " @" + esc(u.username) : ""} <code>${u.id}</code>`).join("<br>")}</td><td class="act"><button class="sm danger" data-bc="${i}">封禁整组</button></td></tr>`).join("") || empty("inbox", "没有发现重复链接", 4)}</table></div>` : ""}
    </div>`;
  bindForm(view, g, (diff) => oai("PATCH", "/groups/" + groupID, diff));
  on("#bs_now", async () => { await oai("POST", `/groups/${groupID}/bioscan`, {}); toast("已开始扫描，人多时需要几分钟"); setTimeout(() => state.tab === "bioscan" && route(), 4000); });
  $$("[data-bc]").forEach((b) => (b.onclick = act(b, async () => {
    const c = clusters[Number(b.dataset.bc)];
    if (!(await ask(`封禁这 ${c.users.length} 人？`, c.link, { danger: true, yes: "封禁" }))) return;
    const x = await oai("POST", `/groups/${groupID}/bioscan`, { ban_cluster: { report_id: rep.id, index: Number(b.dataset.bc) } });
    toast(`已封禁 ${x.banned} 人`);
  })));
};

PAGES.members = async () => {
  const [g, ap, ws, gb] = await Promise.all([oai("GET", "/groups/" + state.gid), oai("GET", `/groups/${state.gid}/approved`), oai("GET", `/groups/${state.gid}/warns`), oai("GET", "/gbans")]);
  const uid = (sel) => { const v = Number($(sel).value.trim()); if (!Number.isInteger(v) || !v) throw new Error("请输入数字用户 ID（群里发 /id 可查）"); return v; };
  const wl = (g.bot_admins || "").split(/\s+/).filter(Boolean);
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>管理员白名单 <span class="pill">${wl.length}</span></h2><p>不是群管理员也能用机器人的管理命令、私聊设置面板、处理举报；不受反垃圾限制；广告预览也会私聊发给他们。</p></div></div>
      <label class="field"><span>用户 ID 或 @用户名</span><textarea id="wl" rows="3" placeholder="每行一个，或用空格 / 逗号隔开。@用户名需要对方在群里说过话">${esc(wl.join("\n"))}</textarea><small>机器人主人（配置文件 owner_ids）在所有群都是管理员，不用填</small></label>
      <div class="row" style="margin-top:14px"><button class="pri" id="wls">保存</button></div></div>
  <div class="cols">
    <div class="card"><div class="card-h"><div><h2>免检白名单 <span class="pill">${ap.length}</span></h2><p>只是不受反垃圾限制，没有管理权限</p></div></div>
      <div class="row"><input type="text" id="au" class="grow" placeholder="用户 ID"><button class="pri" id="aa">加入</button></div>
      <div class="tbl-wrap" style="margin-top:14px"><table class="tbl"><tr><th>用户</th><th></th></tr>
      ${ap.map((a) => `<tr><td>${esc(a.name || "")} <span class="muted">${a.user_id}</span></td><td class="act"><button class="sm" data-da="${a.user_id}">移除</button></td></tr>`).join("") || empty("inbox", "暂无", 2)}</table></div></div>
    <div class="card"><div class="card-h"><div><h2>解封 / 解除禁言</h2><p>只在当前群生效</p></div></div>
      <div class="row"><input type="text" id="ub" class="grow" placeholder="用户 ID"><button id="uba">解封</button></div>
      <div class="card-h" style="margin:22px 0 12px"><div><h2>警告记录</h2></div></div>
      <div class="tbl-wrap"><table class="tbl"><tr><th>用户 ID</th><th class="r">警告</th><th class="r">消息</th><th>入群</th></tr>
      ${ws.map((m) => `<tr><td>${m.user_id}</td><td class="r">${pill(m.warns >= 2 ? "warn" : "", m.warns)}</td><td class="r">${m.msg_count}</td><td class="muted">${fmtTime(m.joined_at)}</td></tr>`).join("") || empty("inbox", "没有人被警告", 4)}</table></div></div>
  </div>
  <div class="card" style="margin-top:16px"><div class="card-h"><div><h2>全局封禁 <span class="pill">${gb.length}</span></h2><p>所有群生效，进群或发言即封</p></div></div>
    <div class="row"><input type="text" id="gu" placeholder="用户 ID" style="width:180px"><input type="text" id="gr" class="grow" placeholder="原因（可选）"><button class="danger" id="ga">全局封禁</button></div>
    <div class="tbl-wrap" style="margin-top:14px"><table class="tbl"><tr><th>用户 ID</th><th>原因</th><th>时间</th><th></th></tr>
    ${gb.map((g) => `<tr><td>${g.user_id}</td><td>${esc(g.reason) || `<span class="muted">—</span>`}</td><td class="muted">${fmtTime(g.created_at)}</td><td class="act"><button class="sm" data-dg="${g.user_id}">解除</button></td></tr>`).join("") || empty("inbox", "暂无", 4)}</table></div></div>`;
  on("#wls", async () => { await oai("PATCH", "/groups/" + state.gid, { bot_admins: $("#wl").value }); toast("管理员白名单已保存"); route(); });
  on("#aa", async () => { await oai("POST", `/groups/${state.gid}/approved`, { user_id: uid("#au") }); toast("已加入白名单"); route(); });
  on("#uba", async () => { await oai("POST", `/groups/${state.gid}/unban`, { user_id: uid("#ub") }); toast("已解封"); });
  on("#ga", async () => {
    const id = uid("#gu");
    if (!(await ask("全局封禁？", `用户 ${id} 会在所有群被封禁。`, { danger: true, yes: "封禁" }))) return;
    await oai("POST", "/gbans", { user_id: id, reason: $("#gr").value.trim() });
    route();
  });
  $$("[data-da]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `/groups/${state.gid}/approved/${b.dataset.da}`); route(); })));
  $$("[data-dg]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `/gbans/${b.dataset.dg}`); route(); })));
};

const REPORT_STATUS = { open: ["warn", "待处理"], deleted: ["", "已删除消息"], muted: ["warn", "已禁言"], banned: ["bad", "已封禁"], ignored: ["", "已忽略"] };
PAGES.reports = async () => {
  state.rtab = state.rtab || "open";
  const [rs, gs] = await Promise.all([oai("GET", "/reports", null, { status: state.rtab === "open" ? "open" : "all" }), oai("GET", "/groups")]);
  const gname = (id) => { const x = (gs || []).find((g) => g.id === id); return x ? x.title : id; };
  const list = state.rtab === "open" ? rs : rs.filter((r) => r.status !== "open");
  if (state.info) state.info.open_reports = state.rtab === "open" ? rs.length : state.info.open_reports;
  renderNav();
  view.innerHTML = `<div class="card"><div class="card-h"><div class="tabs"><button class="${state.rtab === "open" ? "on" : ""}" data-rt="open">待处理</button><button class="${state.rtab === "done" ? "on" : ""}" data-rt="done">已处理</button></div></div>
    <div class="tbl-wrap"><table class="tbl"><tr><th>#</th><th>群</th><th>被举报</th><th>内容</th><th>举报人</th><th>时间</th><th></th></tr>
    ${list.map((r) => `<tr><td class="muted">${r.id}</td><td>${esc(gname(r.group_id))}</td><td><b>${esc(r.target)}</b><br><span class="muted">${r.target_id}</span></td><td class="clip">${esc(r.text) || `<span class="muted">（非文字消息）</span>`}</td>
      <td>${esc(r.reporter)}${r.count > 1 ? ` ${pill("warn", r.count + " 人")}` : ""}</td><td class="muted">${fmtTime(r.created_at)}</td>
      <td class="act">${r.status === "open" ? `<button class="sm" data-ra="del" data-id="${r.id}">删除</button> <button class="sm" data-ra="mute" data-id="${r.id}">禁言</button> <button class="sm danger" data-ra="ban" data-id="${r.id}">封禁</button> <button class="sm ghost" data-ra="ign" data-id="${r.id}">忽略</button>`
        : `${pill(...(REPORT_STATUS[r.status] || ["", r.status]))} <span class="muted">${esc(r.handled_by || "")}</span>`}</td></tr>`).join("") || empty("flag", state.rtab === "open" ? "没有待处理的举报" : "还没有处理记录", 7)}</table></div></div>`;
  $$("[data-rt]").forEach((b) => (b.onclick = () => { state.rtab = b.dataset.rt; route(); }));
  $$("[data-ra]").forEach((b) => (b.onclick = act(b, async () => {
    const r = await oai("POST", `/reports/${b.dataset.id}`, { action: b.dataset.ra });
    toast(r.text || "已处理");
    route();
  })));
};

PAGES.feds = async () => {
  const [fs, gs] = await Promise.all([oai("GET", "/feds"), oai("GET", "/groups")]);
  const uid = (el) => { const v = Number(el.value.trim()); if (!Number.isInteger(v) || !v) throw new Error("请输入数字用户 ID（群里发 /id 可查）"); return v; };
  state.fid = fs.find((f) => f.id === state.fid) ? state.fid : fs[0] && fs[0].id;
  const f = fs.find((x) => x.id === state.fid);
  const bans = f ? await oai("GET", `/feds/${f.id}/bans`) : [];
  const free = (gs || []).filter((g) => !fs.some((x) => x.groups.some((y) => y.id === g.id)));
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>联邦</h2><p>群主也可以在群里用 <code>/newfed 名称</code> 创建、<code>/joinfed 联邦ID</code> 加入；联邦管理员在任一成员群回复消息发 <code>/fban</code> 即全部封禁</p></div></div>
      <div class="row">${fs.map((x) => `<button class="${x.id === state.fid ? "pri" : ""}" data-fs="${x.id}">${esc(x.name)} <span class="muted">${x.groups.length} 群</span></button>`).join("")}
      <span class="grow"></span><input type="text" id="fn" placeholder="新联邦名称" style="width:180px"><button id="fc">创建联邦</button></div></div>
    ${f ? `<div class="cols">
      <div class="card"><div class="card-h"><div><h2>${esc(f.name)}</h2><p>ID <code>${esc(f.id)}</code> · 主人 ${f.owner_id || "—"}</p></div><button class="sm danger" id="fdel">删除联邦</button></div>
        <h3 class="sub">成员群 <span class="pill">${f.groups.length}</span></h3>
        <div class="tbl-wrap"><table class="tbl">${f.groups.map((g) => `<tr><td>${esc(g.title || g.id)}</td><td class="act"><button class="sm" data-fg="${g.id}">移出</button></td></tr>`).join("") || empty("users", "还没有群", 2)}</table></div>
        ${free.length ? `<div class="row" style="margin-top:12px"><select id="fga" class="grow">${free.map((g) => `<option value="${g.id}">${esc(g.title || g.id)}</option>`).join("")}</select><button id="fgo">加入联邦</button></div>` : ""}
        <h3 class="sub" style="margin-top:20px">联邦管理员 <span class="pill">${f.admins.length}</span></h3>
        <div class="tbl-wrap"><table class="tbl">${f.admins.map((u) => `<tr><td>${u}</td><td class="act"><button class="sm" data-fa="${u}">移除</button></td></tr>`).join("") || empty("users", "只有联邦主人", 2)}</table></div>
        <div class="row" style="margin-top:12px"><input type="text" id="fau" class="grow" placeholder="用户 ID"><button id="fao">添加管理员</button></div></div>
      <div class="card"><div class="card-h"><div><h2>封禁名单 <span class="pill">${bans.length}</span></h2><p>成员群全部生效，新群加入时自动同步</p></div></div>
        <div class="row"><input type="text" id="fbu" placeholder="用户 ID" style="width:150px"><input type="text" id="fbr" class="grow" placeholder="原因（可选）"><button class="danger" id="fbo">联邦封禁</button></div>
        <div class="tbl-wrap" style="margin-top:14px"><table class="tbl"><tr><th>用户 ID</th><th>原因</th><th>时间</th><th></th></tr>
        ${bans.map((b) => `<tr><td>${b.user_id}</td><td>${esc(b.reason) || `<span class="muted">—</span>`}</td><td class="muted">${fmtTime(b.created_at)}</td><td class="act"><button class="sm" data-fu="${b.user_id}">解除</button></td></tr>`).join("") || empty("inbox", "暂无", 4)}</table></div></div>
    </div>` : `<div class="card"><div class="empty"><span class="e">${icon("link", 34)}</span>还没有联邦<br><span class="muted">创建一个联邦，把你管理的群都加进去，封禁就能在所有群同步。</span></div></div>`}`;
  $$("[data-fs]").forEach((b) => (b.onclick = () => { state.fid = b.dataset.fs; route(); }));
  on("#fc", async () => {
    const name = $("#fn").value.trim();
    if (!name) throw new Error("先填联邦名称");
    const r = await oai("POST", "/feds", { name });
    state.fid = r.id;
    toast("已创建");
    route();
  });
  if (!f) return;
  const base = `/feds/${f.id}`;
  on("#fdel", async () => { if (await ask("删除联邦？", "封禁名单会一起删除，已封禁的人不会自动解封。", { danger: true, yes: "删除" })) { await oai("DELETE", base); state.fid = null; route(); } });
  on("#fgo", async () => { await oai("POST", base + "/groups", { group_id: Number($("#fga").value) }); toast("已加入，联邦封禁名单会同步到这个群"); route(); });
  on("#fao", async () => { await oai("POST", base + "/admins", { user_id: uid($("#fau")) }); route(); });
  on("#fbo", async () => {
    const id = uid($("#fbu"));
    if (!(await ask("联邦封禁？", `用户 ${id} 会在 ${f.groups.length} 个成员群被封禁。`, { danger: true, yes: "封禁" }))) return;
    const r = await oai("POST", base + "/bans", { user_id: id, reason: $("#fbr").value.trim() });
    toast(`已在 ${r.groups} 个群封禁`);
    route();
  });
  $$("[data-fg]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `${base}/groups/${b.dataset.fg}`); route(); })));
  $$("[data-fa]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `${base}/admins/${b.dataset.fa}`); route(); })));
  $$("[data-fu]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `${base}/bans/${b.dataset.fu}`); toast("已解除"); route(); })));
};

PAGES.stats = async () => {
  const ss = (await oai("GET", `/groups/${state.gid}/stats`, null, { days: 30 })) || [];
  const max = Math.max(1, ...ss.map((s) => s.messages || 0));
  const sum = (k) => ss.reduce((a, s) => a + (s[k] || 0), 0);
  const K = [["messages", "消息"], ["joins", "进群"], ["verified", "验证通过"], ["deleted", "删除"], ["banned", "封禁"], ["muted", "禁言"]];
  view.innerHTML = `<div class="card"><div class="kpis">${K.map(([k, n]) => `<div class="kpi"><b>${sum(k)}</b><span>${n}</span></div>`).join("")}</div></div>
    <div class="card"><div class="card-h"><div><h2>每日消息</h2></div></div>
      ${ss.length ? `<div class="chart">${ss.map((s) => `<div class="c" title="${esc(s.day)}：${s.messages} 条"><div class="b" style="height:${((s.messages || 0) / max) * 100}%"></div></div>`).join("")}</div>
      <div class="chart-x"><span>${esc(ss[0].day.slice(5))}</span><span>${esc(ss[ss.length - 1].day.slice(5))}</span></div>` : `<div class="empty"><span class="e">${icon("inbox", 34)}</span>还没有数据</div>`}</div>
    <div class="card"><div class="tbl-wrap"><table class="tbl"><tr><th>日期</th>${K.map(([, n]) => `<th class="r">${n}</th>`).join("")}</tr>
      ${ss.slice().reverse().map((s) => `<tr><td>${esc(s.day.slice(5))}</td>${K.map(([k]) => `<td class="r">${s[k] || 0}</td>`).join("")}</tr>`).join("") || empty("inbox", "暂无", 7)}</table></div></div>`;
};

PAGES.logs = async () => {
  const q = PAGES.logs.q || "";
  const ls = await oai("GET", "/logs", null, { limit: 200, q });
  view.innerHTML = `<div class="card"><div class="row"><input type="text" id="lq" class="grow" placeholder="搜索用户、群名、原因……" value="${esc(q)}"><button id="lb">搜索</button></div></div>
    <div class="card"><div class="tbl-wrap"><table class="tbl">${ls.map((l) => `<tr><td class="muted" style="white-space:nowrap;width:1%">${fmtTime(l.created_at)}</td><td><pre class="log">${esc(l.text)}</pre></td></tr>`).join("") || empty("inbox", q ? "没有匹配的日志" : "还没有日志")}</table></div></div>`;
  const go = () => { PAGES.logs.q = $("#lq").value.trim(); route(); };
  $("#lb").onclick = go;
  onEnter("#lq", go);
};

PAGES.relay = async () => {
  const st = PAGES.relay.st || (PAGES.relay.st = { q: "", banned: false, offset: 0 });
  const [s, us] = await Promise.all([jack("GET", "/settings"), jack("GET", "/users", null, { q: st.q, banned: st.banned ? 1 : "", offset: st.offset, limit: 30 })]);
  const sw = (k, n, d) => toggle(s, [k, n, d]);
  view.innerHTML = `<div id="relayform">
    <div class="cols">
      <div class="card"><div class="card-h"><div><h2>验证</h2></div></div>
        <div class="fields">
          <label class="field"><span>人机验证</span><select data-k="captcha_mode">${(s.captcha_modes || []).map((m) => `<option ${m === s.captcha_mode ? "selected" : ""}>${esc(m)}</option>`).join("")}</select></label>
          ${numField(s, "verify_ttl_days", "验证有效期", "天", 0, 3650, "0 表示永久")}
          ${numField(s, "max_verify_fail", "失败上限", "次", 1, 100)}
          ${numField(s, "max_unreplied", "未回复上限", "条", 0, 1000, "0 不限")}
        </div></div>
      <div class="card"><div class="card-h"><div><h2>行为</h2></div></div>
        <div class="toggles" style="grid-template-columns:1fr">${sw("welcome_confirm", "须知需确认", "访客先点确认再发消息")}${sw("reply_signature", "回复带署名")}${sw("delivery_receipt", "送达回执")}${sw("away_enabled", "离开模式", "自动回复离开消息")}</div>
        <div class="fields" style="margin-top:12px">${numField(s, "away_hours", "离开回复间隔", "小时", 1, 720)}</div></div>
    </div>
    <div class="cols" style="margin-top:16px">
      <div class="card"><div class="card-h"><div><h2>欢迎须知</h2><p>文字；图片等请在机器人里设置</p></div></div><label class="field"><textarea data-k="welcome">${esc(s.welcome)}</textarea></label></div>
      <div class="card"><div class="card-h"><div><h2>离开自动回复</h2></div></div><label class="field"><textarea data-k="away">${esc(s.away)}</textarea></label></div>
    </div></div>
    <div class="card" style="margin-top:16px"><div class="card-h"><div><h2>访客 <span class="pill">${us.total}</span></h2></div></div>
      <div class="row"><input type="text" id="uq" class="grow" placeholder="ID / 用户名 / 名字" value="${esc(st.q)}"><label class="tg" style="padding:7px 12px"><span class="t">只看封禁</span><input type="checkbox" id="ubn" ${st.banned ? "checked" : ""}><span class="sw"></span></label><button id="us">搜索</button></div>
      <div class="tbl-wrap" style="margin-top:14px"><table class="tbl"><tr><th>访客</th><th>状态</th><th class="r">未回复</th><th>最近活动</th><th></th></tr>
      ${(us.items || []).map((u) => `<tr><td><b>${esc(u.name)}</b> ${u.username ? `<span class="muted">@${esc(u.username)}</span>` : ""}<br><span class="muted">${u.id}</span></td>
        <td>${u.banned ? pill("bad", "封禁") : u.verified ? pill("ok", "已验证") : pill("", "未验证")}</td>
        <td class="r">${u.unreplied}</td><td class="muted">${fmtTime(u.updated_at)}</td>
        <td class="act">${u.unreplied ? `<button class="sm" data-rr="${u.id}">清零</button> ` : ""}${u.banned ? `<button class="sm" data-ub="${u.id}">解封</button>` : `<button class="sm danger" data-bn="${u.id}">封禁</button>`}</td></tr>`).join("") || empty("inbox", "没有访客", 5)}</table></div>
      <div class="row" style="justify-content:flex-end;margin-top:12px"><span class="muted">${us.total ? `${st.offset + 1}–${Math.min(st.offset + 30, us.total)} / ${us.total}` : ""}</span><button id="pv" ${st.offset ? "" : "disabled"}>上一页</button><button id="nx" ${st.offset + 30 < us.total ? "" : "disabled"}>下一页</button></div></div>`;
  bindForm($("#relayform"), s, (diff) => jack("PATCH", "/settings", diff));
  const search = () => { st.q = $("#uq").value.trim(); st.banned = $("#ubn").checked; st.offset = 0; route(); };
  $("#us").onclick = search;
  onEnter("#uq", search);
  $("#pv").onclick = () => { st.offset = Math.max(0, st.offset - 30); route(); };
  $("#nx").onclick = () => { st.offset += 30; route(); };
  $$("[data-bn]").forEach((b) => (b.onclick = act(b, async () => { if (await ask("封禁该访客？", `ID ${b.dataset.bn}`, { danger: true, yes: "封禁" })) { await jack("POST", `/users/${b.dataset.bn}/ban`); route(); } })));
  $$("[data-ub]").forEach((b) => (b.onclick = act(b, async () => { await jack("POST", `/users/${b.dataset.ub}/unban`); route(); })));
  $$("[data-rr]").forEach((b) => (b.onclick = act(b, async () => { await jack("POST", `/users/${b.dataset.rr}/reset`); route(); })));
};

// ---------- 启动 ----------
(async () => {
  try {
    const b = await bridge();
    const ctx = (b.getContext && b.getContext()) || {};
    if (ctx.isDark !== undefined && !document.documentElement.dataset.theme) document.documentElement.dataset.theme = ctx.isDark ? "dark" : "light";
    if (b.onContext) b.onContext((c) => { if (c && c.isDark !== undefined) document.documentElement.dataset.theme = c.isDark ? "dark" : "light"; });
    state.targets = unwrap(await b.apiGet("page/targets", {})) || state.targets;
  } catch (e) {
    view.innerHTML = `<div class="card"><div class="empty"><span class="e">${icon("plug", 34)}</span><b>无法连接插件</b><br><span class="muted">${esc(e.message)}</span></div></div>`;
    return;
  }
  $("#sidefoot").textContent = [state.targets.oai && "群管已连接", state.targets.jack && "中继已连接"].filter(Boolean).join("　") || "未配置";
  if (state.targets.oai) oai("GET", "/status").then((o) => { state.info = o; $("#botname").textContent = "@" + (o.bot_username || ""); }).catch(() => {});
  route();
})();
})();
