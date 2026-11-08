// TG 群管面板 v2（AstrBot 插件视图 / Telegram Mini App 共用）
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
  gid: new URLSearchParams(location.search).get("gid") || store.get("tgguard.gid"),
  tab: (location.hash || "").slice(1) || "home",
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
  if (el.type === "number" || el.dataset.num !== undefined) return Number(el.value);
  return el.value;
}
function collectDiff(root, orig) {
  const diff = {};
  $$("[data-k]", root).forEach((el) => {
    const v = readEl(el);
    if (v === undefined) return;
    const k = el.dataset.k;
    if (v !== orig[k] && !(orig[k] == null && (v === "" || v === false))) diff[k] = v;
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
  activity: '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
  layers: '<path d="m12 2 10 5-10 5L2 7z"/><path d="m2 17 10 5 10-5M2 12l10 5 10-5"/>',
  grid: '<rect x="3.5" y="3.5" width="7" height="7" rx="1.5"/><rect x="13.5" y="3.5" width="7" height="7" rx="1.5"/><rect x="3.5" y="13.5" width="7" height="7" rx="1.5"/><rect x="13.5" y="13.5" width="7" height="7" rx="1.5"/>',
  send: '<path d="M21 3 10 14"/><path d="m21 3-7 18-4-7-7-4z"/>',
  moon: '<path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>',
  bolt: '<path d="M13 2 4 14h7l-1 8 9-12h-7z"/>',
  chev: '<path d="m9 6 6 6-6 6"/>',
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
  star: '<path d="m12 3 2.8 5.7 6.2.9-4.5 4.4 1 6.2L12 17.3 6.5 20.2l1-6.2L3 9.6l6.2-.9z"/>',
  gift: '<rect x="3" y="8" width="18" height="4" rx="1"/><path d="M5 12v9h14v-9M12 8v13M12 8c-2-4-6-4-6-1.5S9 8 12 8zm0 0c2-4 6-4 6-1.5S15 8 12 8z"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
  refresh: '<path d="M3 12a9 9 0 0 1 15.5-6.3L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-15.5 6.3L3 16"/><path d="M8 16H3v5"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
  monitor: '<rect x="2.5" y="4" width="19" height="13" rx="2"/><path d="M8 21h8M12 17v4"/>',
  x: '<path d="M18 6 6 18M6 6l12 12"/>',
  down: '<path d="m6 9 6 6 6-6"/>',
  updown: '<path d="m7 15 5 5 5-5"/><path d="m7 9 5-5 5 5"/>',
  check: '<path d="M20 6 9 17l-5-5"/>',
  userplus: '<circle cx="9" cy="8" r="4"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><path d="M19 8v6M16 11h6"/>',
  copy: '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/>',
};
Object.assign(ICON_PATHS, {
  forward: '<path d="m15 14 5-5-5-5"/><path d="M20 9H9.5A5.5 5.5 0 0 0 4 14.5 5.5 5.5 0 0 0 9.5 20H13"/>',
  mouse: '<rect x="6" y="3" width="12" height="18" rx="6"/><path d="M12 7v4"/>',
  bot: '<rect x="4" y="8" width="16" height="12" rx="3"/><path d="M12 8V4M9 13v1M15 13v1"/>',
  story: '<circle cx="12" cy="12" r="9" stroke-dasharray="4 3"/><circle cx="12" cy="12" r="4"/>',
  sticker: '<path d="M15.5 3H6a3 3 0 0 0-3 3v12a3 3 0 0 0 3 3h9l6-6V6a3 3 0 0 0-3-3z"/><path d="M15 21v-3a3 3 0 0 1 3-3h3"/>',
  film: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 4v16M17 4v16M3 9h4M3 15h4M17 9h4M17 15h4"/>',
  image: '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-5-5L5 21"/>',
  mic: '<rect x="9" y="3" width="6" height="11" rx="3"/><path d="M5 11a7 7 0 0 0 14 0M12 18v3"/>',
  idcard: '<rect x="3" y="5" width="18" height="14" rx="2"/><circle cx="9" cy="11" r="2"/><path d="M6 16a3 3 0 0 1 6 0M15 10h3M15 14h3"/>',
  poll: '<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M8 16v-4M12 16V8M16 16v-6"/>',
  dice: '<rect x="3" y="3" width="18" height="18" rx="3"/><path d="M8 8h.01M16 8h.01M12 12h.01M8 16h.01M16 16h.01"/>',
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>',
  sparkles: '<path d="M12 3l1.8 4.7L18.5 9.5l-4.7 1.8L12 16l-1.8-4.7L5.5 9.5l4.7-1.8z"/><path d="M19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z"/>',
  user: '<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>',
  eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
  coin: '<circle cx="12" cy="12" r="9"/><path d="M9 8h6M12 8v8M8 12h8"/>',
  broom: '<path d="M14 4 10 8M4 20l6-6M8.5 9.5l6 6L11 21H3v-8z"/>',
  mega: '<path d="M3 11v2a1 1 0 0 0 1 1h3l5 4V6L7 10H4a1 1 0 0 0-1 1z"/><path d="M16 8a5 5 0 0 1 0 8M19 5a9 9 0 0 1 0 14"/>',
  lock: '<rect x="4" y="11" width="16" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>',
  hourglass: '<path d="M6 3h12M6 21h12M7 3c0 5 10 5 10 9s-10 4-10 9M17 3c0 5-10 5-10 9"/>',
});
const icon = (k, size = 18) => `<svg class="icon" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICON_PATHS[k] || ""}</svg>`;

// ---------- 通用组件：抽屉弹窗、从 Telegram 获取、媒体、话题下拉、选人 ----------
// sheet 右侧抽屉（手机上是底部弹层），放新建 / 编辑表单，不再把表单堆在列表下面
function sheet({ title, sub = "", body, wide = false, okText = "保存", onOk, onOpen, danger = false, footer = true }) {
  return new Promise((resolve) => {
    const wrap = document.createElement("div");
    wrap.className = "sheet-wrap";
    wrap.innerHTML = `<div class="sheet${wide ? " wide" : ""}" role="dialog">
      <div class="sheet-h"><div><h3>${title}</h3>${sub ? `<p>${sub}</p>` : ""}</div><button class="icon-btn" data-x title="关闭">${icon("x", 18)}</button></div>
      <div class="sheet-b">${body}</div>
      ${footer ? `<div class="sheet-f"><button class="ghost" data-x>取消</button><button class="${danger ? "danger-solid" : "pri"}" data-ok>${okText}</button></div>` : ""}</div>`;
    document.body.appendChild(wrap);
    requestAnimationFrame(() => wrap.classList.add("open"));
    const close = (v) => { wrap.classList.remove("open"); setTimeout(() => wrap.remove(), 180); resolve(v); };
    wrap.addEventListener("click", (e) => { if (e.target === wrap || e.target.closest("[data-x]")) close(null); });
    const ok = wrap.querySelector("[data-ok]");
    if (ok) ok.onclick = act(ok, async () => { const r = onOk ? await onOk(wrap) : true; if (r !== false) close(r ?? true); });
    if (onOpen) onOpen(wrap, close);
  });
}

// collapse 可展开的高级设置
const collapse = (title, body, open = false) => `<details class="fold"${open ? " open" : ""}><summary>${title}</summary><div class="fold-b">${body}</div></details>`;

// captureFlow 让管理员去 Telegram 私聊把内容发给机器人，面板自动拿到（文字格式、动态表情、图片都保留）
async function captureFlow(purpose = "any") {
  const r = await oai("POST", "/captures", { purpose });
  let timer = null;
  const got = await sheet({
    title: "从 Telegram 获取", sub: "动态表情、粗体链接这些格式手动不好输入，直接发给机器人最省事", footer: false,
    body: `<ol class="steps"><li>点下面按钮打开机器人私聊，点「开始」</li><li>把要保存的内容发过去（可以直接转发，带图片也行）</li><li>回到这里，内容会自动填好</li></ol>
      <a class="btn pri big" href="${esc(r.link)}" target="_blank" rel="noopener">${icon("chat", 16)} 打开 Telegram</a>
      <div class="copyline"><code>${esc(r.link)}</code><button class="sm" data-copy>复制链接</button></div>
      <div class="waiting"><span class="spin"></span> 等待你在 Telegram 里发送…</div>`,
    onOpen: (w, close) => {
      w.querySelector("[data-copy]").onclick = async () => { try { await navigator.clipboard.writeText(r.link); toast("已复制"); } catch { toast("复制失败，请手动选中复制", true); } };
      const t0 = Date.now();
      const poll = async () => {
        if (!document.body.contains(w)) return;
        try {
          const c = await oai("GET", "/captures/" + r.token);
          if (c && c.done) { close(c); return; }
        } catch { /* 网络抖动，继续等 */ }
        if (Date.now() - t0 > 10 * 60 * 1000) { w.querySelector(".waiting").textContent = "已超时，请关闭后重试"; return; }
        timer = setTimeout(poll, 2000);
      };
      timer = setTimeout(poll, 2000);
    },
  });
  clearTimeout(timer);
  if (got) toast("已获取：" + (got.preview || "内容"));
  return got;
}

const MEDIA_NAME = { photo: "图片", animation: "动图", video: "视频", document: "文件", sticker: "贴纸", audio: "音频", voice: "语音" };
const mediaLabel = (m) => { if (!m) return ""; const [k] = String(m).split(":"); return MEDIA_NAME[k] || (m.startsWith("local:") ? "上传的图片" : "图片"); };

// mediaField 媒体选择：显示当前媒体，「从 Telegram 获取」或清除。值存在隐藏 input 里（可带 data-k 进保存条）
function mediaField(id, value, { k = "", label = "配图 / 动图 / 视频（可选）" } = {}) {
  return `<div class="field media-field" data-media="${id}"><span>${label}</span>
    <div class="media-box"><input type="hidden" id="${id}" ${k ? `data-k="${k}"` : ""} value="${esc(value || "")}">
      <span class="media-now">${value ? pill("acc", mediaLabel(value)) : '<span class="muted">没有</span>'}</span>
      <button type="button" class="sm" data-mget>${icon("chat", 14)} 从 Telegram 获取</button>
      <button type="button" class="sm ghost" data-mclr ${value ? "" : "hidden"}>清除</button></div></div>`;
}
function bindMedia(root = document) {
  $$("[data-media]", root).forEach((box) => {
    if (box.dataset.bound) return;
    box.dataset.bound = 1;
    const inp = box.querySelector("input");
    const set = (v) => {
      inp.value = v || "";
      box.querySelector(".media-now").innerHTML = v ? pill("acc", mediaLabel(v)) : '<span class="muted">没有</span>';
      box.querySelector("[data-mclr]").hidden = !v;
      inp.dispatchEvent(new Event("input", { bubbles: true }));
    };
    box.querySelector("[data-mget]").onclick = act(box.querySelector("[data-mget]"), async () => {
      const c = await captureFlow("media");
      if (c && c.media) set(c.media); else if (c) toast("这条消息里没有图片 / 动图 / 视频", true);
    });
    box.querySelector("[data-mclr]").onclick = () => set("");
    box._set = set;
  });
}

// topics 话题下拉（话题群才显示）
const topicCache = {};
async function loadTopics(gid) {
  if (!topicCache[gid]) topicCache[gid] = await oai("GET", `/groups/${gid}/topics`).catch(() => []);
  return topicCache[gid];
}
function topicSelect(id, value, topics, { k = "", label = "发到哪个话题", none = "不指定（General）" } = {}) {
  if (!topics || !topics.length) return "";
  return `<label class="field"><span>${label}</span><select id="${id}" ${k ? `data-k="${k}" data-num` : ""}>
    <option value="0">${none}</option>${topics.map((t) => `<option value="${t.thread_id}" ${Number(value) === t.thread_id ? "selected" : ""}>${esc(t.name || "话题 #" + t.thread_id)}${t.closed ? "（已关闭）" : ""}</option>`).join("")}</select>
    <small>只列出机器人见过的话题；新话题里有人说话后就会出现</small></label>`;
}

// userPicker 输入名字 / @用户名 / ID，下拉选成员，不用手动查 ID
function userPicker(id, placeholder = "搜名字、@用户名或填 ID") {
  return `<div class="picker" data-picker="${id}"><input type="text" id="${id}" autocomplete="off" placeholder="${placeholder}"><div class="picker-list" hidden></div></div>`;
}
function bindPickers(root = document) {
  $$("[data-picker]", root).forEach((box) => {
    if (box.dataset.bound) return;
    box.dataset.bound = 1;
    const inp = box.querySelector("input"), list = box.querySelector(".picker-list");
    let t = null;
    inp.oninput = () => {
      inp.dataset.uid = /^\d+$/.test(inp.value.trim()) ? inp.value.trim() : "";
      clearTimeout(t);
      const q = inp.value.trim().replace(/^@/, "");
      if (!q || /^\d+$/.test(q) || !state.gid) { list.hidden = true; return; }
      t = setTimeout(async () => {
        const r = await oai("GET", `/groups/${state.gid}/members`, null, { q, filter: "all", limit: 8 }).catch(() => ({ items: [] }));
        const items = r.items || [];
        list.innerHTML = items.map((m) => `<button type="button" data-uid="${m.user_id}"><b>${esc([m.first_name, m.last_name].filter(Boolean).join(" ") || m.user_id)}</b><span class="muted">${m.username ? "@" + esc(m.username) + " · " : ""}${m.user_id}</span></button>`).join("") || `<div class="muted pad">没找到，可以直接填数字 ID</div>`;
        list.hidden = false;
      }, 250);
    };
    list.onclick = (e) => {
      const b = e.target.closest("[data-uid]");
      if (!b) return;
      inp.value = b.dataset.uid;
      inp.dataset.uid = b.dataset.uid;
      list.hidden = true;
    };
    inp.onblur = () => setTimeout(() => (list.hidden = true), 200);
  });
}
const pickedUID = (sel, root = document) => {
  const el = $(sel, root);
  const v = Number((el.dataset.uid || el.value || "").trim());
  if (!Number.isInteger(v) || !v) throw new Error("请从下拉里选一个成员，或填数字用户 ID（群里发 /id 可查）");
  return v;
};

// 下拉选项
const opt = (pairs, cur) => pairs.map(([v, n]) => `<option value="${esc(v)}" ${String(cur ?? "") === String(v) ? "selected" : ""}>${n}</option>`).join("");
const selField = (k, label, pairs, cur, hint = "") => `<label class="field fr"><span class="fl"><b>${label}</b>${hint ? `<small>${hint}</small>` : ""}</span><select data-k="${k}" ${typeof pairs[0][0] === "number" ? "data-num" : ""}>${opt(pairs, cur)}</select></label>`;

// ---------- 导航 ----------
// 结构：概览 →「当前群」（顶部切换群，下面都是这个群的设置）→「全局」（所有群共用）→ 私聊中继。
// 每个一级页面可以有若干二级标签（subs），避免一个页面塞几十个设置。
const TABS = [
  { k: "feed", n: "动态", ic: "activity", c: "#0a7cff", t: "oai", g: true, badge: "reports", def: "stats",
    subs: [["reports", "举报", "群友举报的消息，删除、禁言、封禁或忽略"], ["records", "处罚记录", "机器人和管理员做过的处罚，误封可以撤销"], ["stats", "30 天数据", "消息、进群、验证、拦截的趋势"], ["tools", "群工具", "清理僵尸号、导出等工具"]] },
  { k: "rules", n: "规则", ic: "shield", c: "#0a7cff", t: "oai", g: true, def: "mode",
    subs: [["mode", "验证方式", "新人进群怎么验证"], ["welcome", "欢迎语与群规", "进群后看到什么"], ["gate", "入群风控", "防爆群、资料检查、CAS 黑名单"], ["guard", "新人与防护", "新人限制、刷屏、重复内容、夜间模式"], ["locks", "消息锁定", "哪些类型的消息不允许发"], ["filters", "屏蔽词", "命中就删除并处罚"], ["ai", "AI 审核", "让 AI 判断广告和引流"], ["punish", "警告与处罚", "警告几次后怎么处罚"]] },
  { k: "people", n: "成员", ic: "users", c: "#0a7cff", t: "oai", g: true, def: "list",
    subs: [["list", "成员", "搜索成员，查看警告、禁言、封禁"], ["whitelist", "白名单", "不受防护限制的人"], ["access", "管理权限", "谁能在 Telegram 里管理机器人"], ["bioscan", "主页查重", "找出简介带引流的成员"], ["invites", "谁拉了谁", "邀请统计和明细"], ["ranks", "发言榜", "最活跃的成员"]] },
  { k: "content", n: "内容", ic: "layers", c: "#0a7cff", t: "oai", g: true, def: "notes",
    subs: [["send", "发一条消息", "从面板直接发到群里"], ["notes", "自动回复与笔记", "关键词回复、/save 笔记"], ["schedules", "定时消息", "按时间自动发送"], ["ads", "定时广告", "循环发送的广告"], ["lottery", "抽奖", "发起和管理抽奖"], ["points", "签到积分", "签到、积分规则"]] },
  { k: "bot", n: "机器人", ic: "gear", c: "#8e8e93", foot: true, def: "overview", d: "所有群共用的设置",
    subs: [["overview", "总览", "机器人状态和所有群", "oai"], ["api", "AI 接口", "接口地址、密钥和模型", "oai", true], ["learn", "学习样本", "AI 记住的正常和违规消息", "oai", true], ["gban", "全局封禁", "在所有群生效的封禁", "oai", true], ["feds", "联邦", "多个群共享封禁名单", "oai", true], ["logs", "操作日志", "面板和机器人的操作记录", "oai", true], ["relay", "私聊中继", "访客、验证与自动回复", "jack"]] },
];
const DOCK = ["feed", "rules", "people", "content"];
const VIEWS = {
  "feed/reports": "reports", "feed/records": "punish", "feed/stats": "stats", "feed/tools": "gTools",
  "rules/mode": "gVerify", "rules/welcome": "gWelcome", "rules/gate": "gGate", "rules/guard": "gProtect", "rules/locks": "gLocks", "rules/filters": "filters", "rules/ai": "gAI", "rules/punish": "gWarnRules",
  "people/list": "people", "people/whitelist": "members", "people/access": "access", "people/bioscan": "bioscan", "people/invites": "gInvites", "people/ranks": "gRanks",
  "content/send": "gSend", "content/notes": "content", "content/schedules": "schedules", "content/ads": "ads", "content/lottery": "lottery", "content/points": "gPoints",
  "bot/overview": "overview", "bot/api": "ai", "bot/learn": "learn", "bot/gban": "gbans", "bot/feds": "feds", "bot/logs": "logs", "bot/relay": "relay",
};
// 旧地址（v1.11–v1.15 的 #tab/sub 和更早的 #group 等）跳到新位置
const LEGACY = {
  home: "feed", "home/home": "feed", "home/stats": "feed/stats", "home/tools": "feed/tools", stats: "feed/stats", tools: "feed/tools",
  verify: "rules/mode", "verify/mode": "rules/mode", "verify/welcome": "rules/welcome", "verify/gate": "rules/gate", group: "rules/mode",
  protect: "rules/guard", "protect/guard": "rules/guard", "protect/locks": "rules/locks", "protect/filters": "rules/filters", "protect/ai": "rules/ai", "protect/rules": "rules/punish", filters: "rules/filters",
  punish: "feed/records", "punish/reports": "feed/reports", "punish/records": "feed/records", reports: "feed/reports",
  "people/list": "people/list", members: "people/whitelist", bioscan: "people/bioscan",
  events: "content/lottery", "events/lottery": "content/lottery", "events/points": "content/points", "events/invites": "people/invites", "events/ranks": "people/ranks", lottery: "content/lottery", growth: "people/invites",
  ads: "content/ads", schedules: "content/schedules",
  overview: "bot/overview", ai: "bot/api", aicfg: "bot/api", learn: "bot/learn", feds: "bot/feds", bans: "bot/gban", logs: "bot/logs", relay: "bot/relay",
};
const tabOf = (k) => TABS.find((t) => t.k === k);
const colOf = (k) => (tabOf(k) || {}).c || "#0a7cff";
// 彩色图标块（详情页里的开关、列表仍在用）
const sq = (ic, c, size = 18, cls = "sq") => `<span class="${cls}" style="--c:${c}">${icon(ic, size)}</span>`;
// Mini App 里不是机器人主人的管理员看不到全局设置
const visible = (t) => {
  if (t.k === "bot") return botSubs().length > 0;
  return (!t.t || state.targets[t.t]) && !(t.own && state.targets.owner === false);
};
const botSubs = () => tabOf("bot").subs.filter(([, , , tgt, own]) => (!tgt || state.targets[tgt]) && !(own && state.targets.owner === false));
const subsOf = (t) => (t.k === "bot" ? botSubs() : t.subs);
state.sub = {};
function parseHash(h) {
  let [k, s] = String(h || "").split("/");
  const key = s ? `${k}/${s}` : k;
  if (!tabOf(k) || (s && !VIEWS[key])) { const to = LEGACY[key] || LEGACY[k]; if (to) [k, s] = to.split("/"); }
  return [tabOf(k) ? k : "", s || ""];
}
(() => { const [k, s] = parseHash(state.tab); state.tab = k || "feed"; if (s) state.sub[state.tab] = s; })();
const wide = () => window.matchMedia("(min-width: 1024px)").matches;
// 当前详情页：手机上没选就是列表；电脑上没选就用默认页
const curSub = (t = tabOf(state.tab)) => {
  if (!t) return "";
  const s = state.sub[t.k];
  const ok = s && subsOf(t).some((x) => x[0] === s);
  return ok ? s : wide() ? (subsOf(t).some((x) => x[0] === t.def) ? t.def : (subsOf(t)[0] || [""])[0]) : "";
};
const viewKey = () => { const s = curSub(); return s ? `${state.tab}/${s}` : state.tab; };
const groupOf = (id) => state.groups.find((g) => String(g.id) === String(id));

// 有未保存的修改时离开页面先确认
async function leaveOK() {
  if ($("#savebar").hidden) return true;
  return ask("有未保存的修改", "离开后这些修改会丢失。", { danger: true, yes: "放弃修改" });
}
async function go(tab, sub = "") {
  if (!(await leaveOK())) return;
  state.tab = tab;
  state.sub[tab] = sub;
  try { location.hash = sub ? `${tab}/${sub}` : tab; } catch { /* sandbox */ }
  route();
  if (!wide()) window.scrollTo(0, 0);
}

// 群头像：取群名第一个字，颜色由群 ID 决定（同一个群永远同一个颜色）
const GAV_COLORS = ["#2f80ed", "#7c5cff", "#e5484d", "#12a594", "#f76b15", "#d6409f", "#3e63dd", "#30a46c", "#ad7f58", "#0090ff"];
function gav(g, size = 32) {
  const name = String((g && (g.title || g.id)) || "?").trim();
  const ch = Array.from(name.replace(/^[\s\p{P}\p{S}]+/u, ""))[0] || "#";
  let h = 0; for (const c of String(g && g.id) + name) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return `<span class="gav" style="width:${size}px;height:${size}px;font-size:${Math.round(size * 0.44)}px;border-radius:${Math.round(size * 0.28)}px;background:${GAV_COLORS[h % GAV_COLORS.length]}">${esc(ch)}</span>`;
}
const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || "");

// ---------- 外壳：顶部群切换、分段导航、标签栏 ----------
const openReports = () => (state.info && state.info.open_reports) || 0;
function renderGSwitch() {
  const b = $("#gsw");
  const grp = groupOf(state.gid);
  if (!state.targets.oai || !grp) {
    b.innerHTML = `<span class="gsw-av bot">${icon("bot", 15)}</span><span class="gsw-n">${esc((state.info && state.info.bot_username && "@" + state.info.bot_username) || "TG 群管")}</span>${icon("down", 14)}`;
  } else {
    b.innerHTML = `${gav(grp, 26)}<span class="gsw-n">${esc(grp.title || grp.id)}</span>${icon("down", 14)}`;
  }
  b.onclick = pickGroup;
}
function renderNav() {
  const tabs = DOCK.map(tabOf).filter((t) => visible(t) && state.groups.length);
  const n = openReports();
  const badge = (t) => (t.badge === "reports" && n ? `<span class="nbadge">${n > 99 ? "99+" : n}</span>` : "");
  $("#tnav").innerHTML = tabs.map((t) => `<button data-k="${t.k}" class="${t.k === state.tab ? "on" : ""}">${t.n}${badge(t)}</button>`).join("");
  $("#tnav").hidden = tabs.length < 2;
  const dock = $("#dock");
  dock.innerHTML = tabs.map((t) => `<button data-k="${t.k}" class="${t.k === state.tab ? "on" : ""}"><span class="di">${icon(t.ic, 25)}${badge(t)}</span><span>${t.n}</span></button>`).join("");
  dock.hidden = tabs.length < 2;
  document.body.classList.toggle("nodock", tabs.length < 2);
  const click = (e) => { const b = e.target.closest("[data-k]"); if (b) go(b.dataset.k); };
  $("#tnav").onclick = click; dock.onclick = click;
  $("#searchbtn").innerHTML = `${icon("search", 15)}<span>搜索设置</span><kbd>${isMac ? "⌘" : "Ctrl"} K</kbd>`;
  $("#searchbtn").onclick = openSearch;
  $("#msearch").innerHTML = icon("search", 19);
  $("#msearch").onclick = openSearch;
  renderGSwitch();
}
// 兼容旧调用（举报页处理完会刷新角标）
function renderBar() { renderNav(); paintList(); }

// 点顶部群名：切群 + 机器人设置
function pickGroup() {
  const n = (g) => (g.open_reports ? `<span class="nbadge">${g.open_reports}</span>` : "");
  const gl = state.targets.oai && state.groups.length ? `<div class="gl-h">我管理的群</div><div class="gl">${state.groups.map((g) => `<a class="gl-r${g.bot_left ? " off" : ""}" data-gid="${g.id}">${gav(g, 34)}<span class="t"><b>${esc(g.title || g.id)}</b><small>${g.bot_left ? "机器人已离开" : esc(g.id)}</small></span>${n(g)}${String(g.id) === state.gid ? `<span class="ck">${icon("check", 20)}</span>` : ""}</a>`).join("")}</div>` : "";
  const bs = botSubs();
  const bl = bs.length ? `<div class="gl-h">机器人</div><div class="gl">${bs.map(([k, nm, d]) => `<a class="gl-r" data-bot="${k}"><span class="t"><b>${nm}</b><small>${esc(d)}</small></span>${icon("chev", 15)}</a>`).join("")}
    <a class="gl-r" data-theme-row><span class="t"><b>外观</b></span><span class="v">${(THEMES.find((x) => x[0] === (store.get("tgguard.theme") || "auto")) || THEMES[0])[1]}</span>${icon("chev", 15)}</a></div>` : "";
  sheet({ title: state.groups.length > 1 ? "切换群" : "群与机器人", footer: false, body: `<div class="gsheet">${gl}${bl}</div>`,
    onOpen: (w, close) => {
      w.querySelector(".gsheet").onclick = async (e) => {
        const a = e.target.closest("a");
        if (!a) return;
        if (a.dataset.themeRow !== undefined) { cycleTheme(); a.querySelector(".v").textContent = (THEMES.find((x) => x[0] === (store.get("tgguard.theme") || "auto")) || THEMES[0])[1]; return; }
        close();
        if (a.dataset.bot) { go("bot", a.dataset.bot); return; }
        if (a.dataset.gid === state.gid && state.tab !== "bot") return;
        if (!(await leaveOK())) return;
        state.gid = a.dataset.gid; store.set("tgguard.gid", state.gid);
        if (state.tab === "bot") state.tab = "feed";
        route();
      };
    } });
}
// ⌘K 命令面板：所有页面和二级页面都能直接搜到
function openSearch() {
  if ($(".cmdk-wrap")) return;
  const items = [];
  let grp = "";
  for (const t of TABS.filter(visible)) {
    if (t.hide) continue;
    if (t.sep) { grp = t.sep; continue; }
    if (t.g && !state.groups.length) continue;
    const g = t.foot ? "机器人" : t.n;
    if (t.subs) subsOf(t).forEach(([s, n]) => items.push({ k: t.k, s, n, p: t.n, ic: t.ic, c: t.c, g }));
    else items.push({ k: t.k, n: t.n, p: t.d || "", ic: t.ic, c: t.c, g });
  }
  const wrap = document.createElement("div");
  wrap.className = "cmdk-wrap";
  wrap.innerHTML = `<div class="cmdk" role="dialog"><div class="cmdk-in">${icon("search", 18)}<input type="text" placeholder="搜索功能，例如：屏蔽词、白名单、欢迎语" autocomplete="off"><kbd>Esc</kbd></div><div class="cmdk-list"></div>
    <div class="cmdk-foot"><span><kbd>↑</kbd><kbd>↓</kbd> 选择</span><span><kbd>Enter</kbd> 打开</span></div></div>`;
  document.body.appendChild(wrap);
  const inp = $("input", wrap), list = $(".cmdk-list", wrap);
  let cur = 0, shown = [];
  const draw = () => {
    const q = inp.value.trim().toLowerCase();
    shown = items.filter((x) => !q || (x.n + x.p + x.g).toLowerCase().includes(q));
    cur = Math.min(cur, Math.max(0, shown.length - 1));
    let last = "", html = "";
    shown.forEach((x, i) => {
      if (x.g !== last) { html += `<div class="cmdk-g">${esc(x.g)}</div>`; last = x.g; }
      html += `<div class="cmdk-i${i === cur ? " on" : ""}" data-i="${i}">${sq(x.ic, x.c, 17)}<b>${esc(x.n)}</b><small>${esc(x.s ? x.p : "")}</small></div>`;
    });
    list.innerHTML = html || `<div class="empty sm">没有匹配的功能</div>`;
    const on = $(".cmdk-i.on", list); if (on) on.scrollIntoView({ block: "nearest" });
  };
  const close = () => { wrap.remove(); document.removeEventListener("keydown", key, true); };
  const pick = (i) => { const x = shown[i]; if (!x) return; close(); go(x.k, x.s); };
  const key = (e) => {
    if (e.key === "Escape") { e.preventDefault(); close(); }
    else if (e.key === "ArrowDown") { e.preventDefault(); cur = Math.min(cur + 1, shown.length - 1); draw(); }
    else if (e.key === "ArrowUp") { e.preventDefault(); cur = Math.max(cur - 1, 0); draw(); }
    else if (e.key === "Enter") { e.preventDefault(); pick(cur); }
  };
  document.addEventListener("keydown", key, true);
  inp.oninput = () => { cur = 0; draw(); };
  list.onclick = (e) => { const r = e.target.closest("[data-i]"); if (r) pick(Number(r.dataset.i)); };
  list.onmousemove = (e) => { const r = e.target.closest("[data-i]"); if (r && Number(r.dataset.i) !== cur) { cur = Number(r.dataset.i); $$(".cmdk-i", list).forEach((el) => el.classList.toggle("on", Number(el.dataset.i) === cur)); } };
  wrap.onclick = (e) => { if (e.target === wrap) close(); };
  draw();
  setTimeout(() => inp.focus(), 10);
}
document.addEventListener("keydown", (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); openSearch(); }
  else if (e.key === "/" && !/INPUT|TEXTAREA|SELECT/.test((document.activeElement || {}).tagName || "")) { e.preventDefault(); openSearch(); }
});

// 主题：跟随系统 / 浅色 / 深色
document.documentElement.dataset.skin = "ios"; // v1.16 统一用苹果原生组件语言
window.matchMedia("(prefers-color-scheme: dark)").addEventListener?.("change", () => applyTheme(store.get("tgguard.theme")));
const THEMES = [["auto", "跟随系统", "monitor"], ["light", "浅色", "sun"], ["dark", "深色", "moon"]];
function applyTheme(pref) {
  const el = document.documentElement;
  if (pref === "light" || pref === "dark") el.dataset.theme = pref;
  else if (state.ctxDark !== undefined) el.dataset.theme = state.ctxDark ? "dark" : "light";
  else el.dataset.theme = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  const cur = THEMES.find((x) => x[0] === (pref || "auto")) || THEMES[0];
  const b = $("#themebtn");
  if (b) { b.innerHTML = icon(cur[2], 16); b.title = "主题：" + cur[1]; }
}
function cycleTheme() {
  const i = THEMES.findIndex((x) => x[0] === (store.get("tgguard.theme") || "auto"));
  const next = THEMES[(i + 1) % THEMES.length][0];
  store.set("tgguard.theme", next);
  applyTheme(next);
}
$("#back").onclick = () => go(state.tab);
applyTheme(store.get("tgguard.theme"));

// ---------- 左栏列表：每个入口的总览（手机上是第一层，电脑上是左栏） ----------
const CHEV = () => icon("chev", 15);
const lrow = (tab, sub, name, val = "", small = "") => {
  const on = state.tab === tab && curSub() === sub && wide();
  return `<a class="lr${on ? " on" : ""}" data-sub="${sub}"><span class="t"><b>${name}</b>${small ? `<small>${small}</small>` : ""}</span>${val !== "" ? `<span class="v">${val}</span>` : ""}${CHEV()}</a>`;
};
const lsw = (g, k, name, small = "") => `<label class="lr sw-r"><span class="t"><b>${name}</b>${small ? `<small>${small}</small>` : ""}</span><input type="checkbox" data-q="${k}" ${g && g[k] ? "checked" : ""}><span class="lsw"></span></label>`;
const lgrp = (title, rows, extra = "") => (rows.filter(Boolean).length ? `${title ? `<div class="lh"><span>${title}</span>${extra}</div>` : ""}<div class="lg">${rows.filter(Boolean).join("")}</div>` : "");
const subName = (t, s) => (subsOf(t).find((x) => x[0] === s) || [s, s])[1];

const LISTS = {
  async feedLoad(id) {
    const [g, ss, rs, ps] = await Promise.all([
      oai("GET", "/groups/" + id),
      oai("GET", `/groups/${id}/stats`, null, { days: 2 }).catch(() => []),
      oai("GET", "/reports", null, { status: "open" }).catch(() => []),
      oai("GET", `/groups/${id}/punishments`, null, { status: "all", limit: 4 }).catch(() => []),
    ]);
    if (state.info) state.info.open_reports = (rs || []).length;
    return { g, today: (ss || []).slice(-1)[0] || {}, rs: (rs || []).filter((r) => String(r.group_id) === String(id)), ps: ps || [] };
  },
  feed(d) {
    const T = d.today || {};
    $("#llead").innerHTML = `今天 <b>${(T.messages || 0).toLocaleString()}</b> 条消息 · <b>${T.joins || 0}</b> 人进群 · 拦下 <b>${T.deleted || 0}</b> 次`;
    const g = d.g || {};
    const todo = [];
    if (g.raid_until && new Date(g.raid_until) > new Date()) todo.push(["rules", "gate", "防爆群已触发，正在拒绝新人"]);
    if (g.verify_mode === "off") todo.push(["rules", "mode", "入群验证没开，广告号可以直接进群"]);
    if (g.ai && state.info && !state.info.ai) todo.push(state.targets.owner === false ? ["rules", "ai", "AI 审核开着，但接口还没配置"] : ["bot", "api", "AI 审核开着，但接口还没配置"]);
    const reps = d.rs.slice(0, 3).map((r) => { const who = r.target || r.target_id || "被举报的消息"; return `<div class="lr rep"><span class="uav">${esc(Array.from(String(who).replace(/^[\s\p{P}\p{S}]+/u, ""))[0] || "?")}</span><span class="t">
      <span class="rh"><b>${esc(who)}</b><span class="tm">${fmtTime(r.created_at)}</span></span>
      <small class="q">${r.count > 1 ? `${r.count} 人举报：` : "举报："}${r.text ? `“${esc(r.text)}”` : "（非文字消息）"}</small>
      <span class="acts"><button class="ab red" data-ra="ban" data-id="${r.id}">封禁</button><button class="ab" data-ra="del" data-id="${r.id}">删消息</button><button class="ab" data-ra="ign" data-id="${r.id}">忽略</button></span></span></div>`; });
    const todoRows = todo.map(([k, s, txt]) => `<a class="lr alert" data-go="${k}/${s}"><span class="t"><b>${txt}</b></span>${CHEV()}</a>`);
    const pend = reps.length + todoRows.length;
    const more = d.rs.length > 3 ? lrow("feed", "reports", `全部 ${d.rs.length} 条举报`).replace('class="lr', 'class="lr more') : "";
    const recs = d.ps.map((p) => {
      const what = `${(PUN_ACT[p.action] || [, p.action])[1]} ${esc(p.name || p.user_id)}`;
      const why = [PUN_SRC[p.source] || p.source, p.reason || p.text].filter(Boolean).map((x) => esc(String(x).slice(0, 40))).join(" · ");
      return `<a class="lr" data-sub="records"><span class="t"><b>${what}${p.undone ? `<span class="muted"> · 已撤销</span>` : ""}</b>${why ? `<small>${why}</small>` : ""}</span><span class="v sm">${fmtTime(p.created_at)}</span></a>`;
    });
    return lgrp("待处理", pend ? [...todoRows, ...reps, more] : [`<div class="lr calm"><span class="t"><b>没有要处理的事</b><small>举报和异常会出现在这里</small></span></div>`], pend ? `<span class="nbadge">${pend}</span>` : "")
      + lgrp("机器人处理记录", recs.length ? [...recs, lrow("feed", "records", "全部记录").replace('class="lr', 'class="lr more')] : [`<div class="lr calm"><span class="t"><b>还没有处罚记录</b></span></div>`])
      + lgrp("更多", [lrow("feed", "reports", "举报", d.rs.length ? d.rs.length + " 条待处理" : ""), lrow("feed", "stats", "30 天数据"), lrow("feed", "tools", "群工具")]);
  },
  async rulesLoad(id) { return { g: await oai("GET", "/groups/" + id) }; },
  rules(d) {
    $("#llead").textContent = "";
    const g = d.g || {};
    const vm = Object.fromEntries(VERIFY.map(([v, n]) => [v, n]));
    const cm = Object.fromEntries(CAPTCHAS.map(([v, n]) => [v, n]));
    const locks = LOCKS.filter(([k]) => g[k]).length;
    const newbie = g.newbie_msgs ? `前 ${g.newbie_msgs} 条严查` : g.newbie_hours ? `${g.newbie_hours} 小时只发文字` : "";
    const raid = g.raid_until && new Date(g.raid_until) > new Date();
    return lgrp("进群", [lrow("rules", "mode", "验证方式", g.verify_mode === "off" ? "关闭" : esc(cm[g.captcha_type] || vm[g.verify_mode] || g.verify_mode || "")), lrow("rules", "welcome", "欢迎语与群规"), lrow("rules", "gate", "入群风控", raid ? "防爆群中" : "")])
      + lgrp("每条消息", [lrow("rules", "guard", "新人与防护", newbie ? esc(newbie) : ""), lrow("rules", "locks", "消息锁定", locks ? `${locks} 类` : "未锁定"), lrow("rules", "filters", "屏蔽词"), lrow("rules", "ai", "AI 审核", g.ai ? "开" : "关")])
      + lgrp("违规之后", [lrow("rules", "punish", "警告与处罚", g.warn_limit ? `${g.warn_limit} 次后${{ mute: "禁言", kick: "踢出", ban: "封禁" }[g.warn_action] || esc(g.warn_action || "")}` : "")])
      + lgrp("快捷开关", [lsw(g, "lock_link", "禁止发链接"), lsw(g, "dup_check", "重复内容检测"), lsw(g, "night_on", "夜间全员禁言")]);
  },
  people() { $("#llead").textContent = ""; return lgrp("", [lrow("people", "list", "成员", "", "搜索、警告、禁言、封禁")]) + lgrp("名单", [lrow("people", "whitelist", "白名单"), lrow("people", "access", "管理权限"), lrow("people", "bioscan", "主页查重")]) + lgrp("统计", [lrow("people", "invites", "谁拉了谁"), lrow("people", "ranks", "发言榜")]); },
  content() {
    $("#llead").textContent = "";
    const u = state.info && state.info.bot_username;
    const add = u ? `<a class="lr add" href="https://t.me/${esc(u)}" target="_blank" rel="noopener">${icon("send", 19)}<span class="t"><b>发给机器人新建</b></span>${CHEV()}</a>` : "";
    return (add ? `<div class="lg first">${add}</div><div class="lft">图片、文件、按钮都会原样保存。</div>` : "")
      + lgrp("消息", [lrow("content", "notes", "自动回复与笔记"), lrow("content", "send", "发一条消息"), lrow("content", "schedules", "定时消息"), lrow("content", "ads", "定时广告")])
      + lgrp("活动", [lrow("content", "lottery", "抽奖"), lrow("content", "points", "签到积分")]);
  },
  bot() { $("#llead").textContent = "所有群共用"; return lgrp("", botSubs().map(([k, n]) => lrow("bot", k, n))); },
};
state.L = {};
function paintList() {
  const t = tabOf(state.tab);
  const box = $("#list");
  if (!t || !box) return;
  const c = state.L[t.k];
  const key = t.g ? state.gid : "";
  const loaded = !LISTS[t.k + "Load"] || (c && c.key === key);
  if (!loaded) { box.innerHTML = `<div class="lg"><div class="lr sk"></div><div class="lr sk"></div><div class="lr sk"></div></div>`; return; }
  box.innerHTML = LISTS[t.k](c ? c.data : null);
  box.onclick = async (e) => {
    const ra = e.target.closest("[data-ra]");
    if (ra) {
      ra.disabled = true;
      try { const r = await oai("POST", `/reports/${ra.dataset.id}`, { action: ra.dataset.ra }); toast(r.text || "已处理"); route(); } catch (err) { ra.disabled = false; toast(err.message, true); }
      return;
    }
    const a = e.target.closest("[data-sub],[data-go]");
    if (!a || a.tagName === "LABEL") return;
    if (a.dataset.go) { const [k, s] = a.dataset.go.split("/"); go(k, s); return; }
    go(t.k, a.dataset.sub);
  };
  $$("[data-q]", box).forEach((c) => (c.onchange = async () => {
    try { await oai("PATCH", "/groups/" + state.gid, { [c.dataset.q]: c.checked }); toast(c.checked ? "已开启" : "已关闭"); if (c2 && c2.data && c2.data.g) c2.data.g[c.dataset.q] = c.checked; } catch (err) { c.checked = !c.checked; toast(err.message, true); }
  }));
  const c2 = state.L[t.k];
}
async function loadList(seq, t) {
  const fn = LISTS[t.k + "Load"];
  if (!fn) return;
  const key = t.g ? state.gid : "";
  const data = await fn(state.gid);
  if (!alive(seq)) return;
  state.L[t.k] = { key, data };
  paintList();
  if (t.k === "feed") renderNav();
}

let routeSeq = 0;
// alive 页面异步加载完时检查用户是否已经切走（切走了就不再渲染）
const alive = (seq) => seq === routeSeq;
const emptyCard = (ic, t, d) => `<div class="lg"><div class="lr calm big"><span class="t"><b>${t}</b><small>${d}</small></span></div></div>`;
async function route() {
  const seq = ++routeSeq;
  saveCtx = null;
  $("#savebar").hidden = true;
  let t = tabOf(state.tab);
  if (!t || !visible(t)) { state.tab = state.targets.oai ? "feed" : "bot"; t = tabOf(state.tab); }
  try {
    if (state.targets.oai && (t.g || !state.groups.length)) {
      state.groups = (await oai("GET", "/groups").catch((e) => { if (t.g) throw e; return []; })) || [];
      if (!groupOf(state.gid)) state.gid = state.groups[0] ? String(state.groups[0].id) : "";
    }
    if (!alive(seq)) return;
    if (t.g && !state.gid && visible(tabOf("bot")) && !state.groups.length && state.targets.owner !== false && !state.sub.feed) { /* 没有群：仍停在动态，给出提示 */ }
    renderNav();
    const sub = t.g && !state.gid ? "" : curSub(t);
    document.body.dataset.mode = sub ? "detail" : "list";
    document.body.dataset.tab = t.k;
    $("#ltitle").textContent = t.k === "bot" ? "机器人" : t.n;
    $("#backlbl").textContent = t.k === "bot" ? "机器人" : t.n;
    if (t.g && !state.gid) {
      $("#llead").textContent = "";
      $("#list").innerHTML = state.targets.owner === false
        ? emptyCard("users", "你现在没有可以管理的群", "需要是群管理员，或者被群主加进「管理权限」名单。")
        : emptyCard("users", "机器人还没有加入任何群", "把 oai_bot 拉进群并设为管理员（删除消息、封禁用户、邀请用户），然后在群里发一次 /settings。");
      view.innerHTML = "";
      return;
    }
    paintList();
    const lp = loadList(seq, t).catch((e) => { if (alive(seq)) $("#list").innerHTML = emptyCard("alert", "加载失败", esc(e.message)); });
    if (sub) {
      const d = subsOf(t).find((x) => x[0] === sub) || [sub, sub, ""];
      $("#dtitle").textContent = d[1];
      $("#dsub").textContent = d[2] || "";
      view.innerHTML = `<div class="skeleton"></div><div class="skeleton"></div>`;
      view.classList.remove("enter"); void view.offsetWidth; view.classList.add("enter");
      clearTimeout(route.enterT); route.enterT = setTimeout(() => alive(seq) && view.classList.remove("enter"), 1600);
      await PAGES[VIEWS[`${t.k}/${sub}`]](seq);
      const grp = t.g ? groupOf(state.gid) : null;
      if (alive(seq) && grp && grp.bot_left) view.insertAdjacentHTML("afterbegin", `<div class="notice">${icon("alert")}<div>机器人已经不在「${esc(grp.title || grp.id)}」里了，这里的设置不会生效。</div></div>`);
    } else view.innerHTML = "";
    await lp;
  } catch (e) {
    if (!alive(seq)) return;
    document.body.dataset.mode = "detail";
    $("#dtitle").textContent = "加载失败";
    $("#dsub").textContent = "";
    view.innerHTML = `<div class="card"><div class="empty"><span class="e">${icon("alert", 34)}</span><b>加载失败</b><br><span class="muted">${esc(e.message)}</span><br><br><button id="retry">重试</button></div></div>`;
    on("#retry", route);
  }
}
let wasWide = wide();
window.addEventListener("resize", () => { if (wide() !== wasWide) { wasWide = wide(); route(); } });
window.addEventListener("hashchange", () => {
  const [k, s] = parseHash(location.hash.slice(1));
  if (!k || (k === state.tab && (s || "") === (state.sub[k] || ""))) return;
  state.tab = k; state.sub[k] = s; route();
});
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
  if (state.targets.oai && state.groups.length) {
    const vm = Object.fromEntries(VERIFY.map(([v, n]) => [v, n]));
    html += `<div class="card"><div class="card-h"><div><h2>管理的群 <span class="pill">${state.groups.length}</span></h2><p>点一个群进入它的设置</p></div></div>
      <div class="glist">${state.groups.map((g) => `<a class="gitem${g.bot_left ? " off" : ""}" data-gid="${g.id}">${gav(g, 38)}<span class="gi-t"><b>${esc(g.title || g.id)}</b>
        <span class="muted">${g.bot_left ? "机器人已离开" : `验证：${esc(vm[g.verify_mode] || g.verify_mode)} · AI ${g.ai ? "开" : "关"}${g.night_on ? " · 夜间模式" : ""}`}</span></span>${icon("chev", 16)}</a>`).join("")}</div></div>`;
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
  $$(".gitem").forEach((a) => (a.onclick = () => { state.gid = a.dataset.gid; store.set("tgguard.gid", state.gid); go("feed"); }));
};

// ---------- 群首页：一眼看清状态 + 常用开关 + 待处理 ----------
PAGES.gHome = async (seq) => {
  const id = state.gid;
  const [g, ss, rs, ps] = await Promise.all([
    oai("GET", "/groups/" + id),
    oai("GET", `/groups/${id}/stats`, null, { days: 7 }).catch(() => []),
    oai("GET", "/reports", null, { group: id, status: "open" }).catch(() => []),
    oai("GET", `/groups/${id}/punishments`, null, { status: "all", limit: 6 }).catch(() => []),
  ]);
  if (!alive(seq)) return;
  const st = ss || [];
  const today = st.length ? st[st.length - 1] : {};
  const yest = st.length > 1 ? st[st.length - 2] : null;
  const sum = (k) => st.reduce((a, s) => a + (s[k] || 0), 0);
  const max = Math.max(1, ...st.map((s) => s.messages || 0));
  const vm = Object.fromEntries(VERIFY.map(([v, n]) => [v, n]));
  const cm = Object.fromEntries(CAPTCHAS.map(([v, n]) => [v, n]));
  const locks = LOCKS.filter(([k]) => g[k]).length;
  const raid = g.raid_until && new Date(g.raid_until) > new Date();
  const grp = groupOf(id) || g;
  const todo = [];
  if ((rs || []).length) todo.push(["flag", `${rs.length} 条举报等你处理`, "punish", "reports", "bad"]);
  if (raid) todo.push(["alert", "防爆群已触发，正在拒绝新人", "verify", "gate", "warn"]);
  if (g.verify_mode === "off") todo.push(["shield", "入群验证没开，广告号可以直接进群", "verify", "mode", "warn"]);
  if (g.ai && state.info && !state.info.ai) todo.push(["plug", "AI 审核开着，但 AI 接口还没配置", state.targets.owner === false ? "protect" : "bot", state.targets.owner === false ? "ai" : "api", "warn"]);
  const QUICK = [["ai", "AI 审核", "新人消息交给 AI 判断"], ["lock_link", "禁止发链接", "普通成员发链接直接删"], ["dup_check", "重复内容检测", "拦截复制粘贴的广告波"], ["night_on", "夜间模式", "时段内全员禁言"]];
  const status = [
    ["userplus", "入群验证", g.verify_mode === "off" ? "关闭" : `${vm[g.verify_mode] || g.verify_mode} · ${cm[g.captcha_type] || g.captcha_type}`, "verify", "mode"],
    ["ban", "消息锁定", locks ? `已锁定 ${locks} 类消息` : "未锁定", "protect", "locks"],
    ["users", "新人限制", [g.newbie_msgs ? `前 ${g.newbie_msgs} 条严查` : "", g.newbie_hours ? `${g.newbie_hours} 小时只发文字` : ""].filter(Boolean).join(" · ") || "关闭", "protect", "guard"],
    ["flag", "警告规则", `${g.warn_limit} 次后${{ mute: "禁言", kick: "踢出", ban: "封禁" }[g.warn_action] || g.warn_action}`, "protect", "rules"],
  ];
  const delta = (k) => {
    if (!yest) return `<span>今天</span>`;
    const a = today[k] || 0, b = yest[k] || 0;
    if (a === b) return `<span>与昨天持平</span>`;
    const up = a > b, pct = b ? Math.round(Math.abs(a - b) / b * 100) : 100;
    return `<span class="${up ? "up" : "down"}">${up ? "↑" : "↓"} ${pct}%</span><span>较昨天</span>`;
  };
  const STATS = [["messages", "今日消息", "chat", "#0a7cff"], ["joins", "新进群", "userplus", "#34c759"], ["verified", "验证通过", "check", "#af52de"], ["deleted", "已拦截", "shield", "#ff3b30"]];
  const row = ([go, ic, n, v, c]) => `<a data-go="${go}">${sq(ic, c || colOf(go.split("/")[0]), 19)}<span class="st"><b>${n}</b><span class="v">${v}</span></span>${icon("chev", 18)}</a>`;
  const PIC = { ban: ["ban", "#ff3b30"], mute: ["lock", "#ff9500"], kick: ["x", "#8e8e93"] };
  const recent = (ps || []).slice(0, 5).map((p) => {
    const [ic, c] = PIC[p.action] || ["flag", "#ff9500"];
    const what = `${(PUN_ACT[p.action] || [, p.action])[1]} ${esc(p.name || p.user_id)}`;
    const why = [PUN_SRC[p.source] || p.source, p.reason || p.text].filter(Boolean).map((x) => esc(String(x).slice(0, 40))).join(" · ");
    return `<a data-go="punish/records">${sq(ic, c, 19)}<span class="st"><b>${what}${p.undone ? ` <span class="pill">已撤销</span>` : ""}</b><span class="v">${why}</span></span><span class="when">${fmtTime(p.created_at)}</span></a>`;
  });
  const ACTS = [["search", "找成员", "people/list", "#af52de"], ["ban", "屏蔽词", "protect/filters", "#ff3b30"], ["mega", "发公告", "content/send", "#32ade6"]];
  const KPI = [["messages", "消息"], ["joins", "进群"], ["verified", "通过验证"], ["deleted", "拦截"]];
  const alert = todo.length ? todo.map(([ic, t, k, sub, cls]) => `<a class="hm-alert ${cls}" data-go="${k}/${sub}">${sq(ic, cls === "bad" ? "#ff3b30" : "#ff9500", 20)}<span class="st"><b>${t}</b></span><span class="go">去处理</span></a>`).join("")
    : `<div class="hm-alert ok">${sq("check", "#34c759", 20)}<span class="st"><b>一切正常</b><span class="v">没有需要你处理的事</span></span></div>`;
  view.innerHTML = `<div class="hm">
    ${alert}
    <div class="gt">今天${today.day ? "" : "（还没有数据）"}</div>
    <a class="kpi" data-go="home/stats">${KPI.map(([k, n]) => `<div><b>${(today[k] || 0).toLocaleString()}</b><span>${n}</span>${yest ? `<i class="${(today[k] || 0) >= (yest[k] || 0) ? "up" : "down"}">${(today[k] || 0) === (yest[k] || 0) ? "持平" : ((today[k] || 0) > (yest[k] || 0) ? "↑ " : "↓ ") + Math.abs((today[k] || 0) - (yest[k] || 0))}</i>` : ""}</div>`).join("")}</a>
    <div class="gt">常用</div>
    <div class="grp slist">${ACTS.map(([ic, n, go, c]) => `<a data-go="${go}">${sq(ic, c, 19)}<span class="st"><b>${n}</b></span>${icon("chev", 18)}</a>`).join("")}</div>
    <div class="gt">最近处理</div>
    <div class="grp slist">${recent.length ? recent.join("") : `<div class="empty sm">还没有处罚记录</div>`}</div>
    <div class="gt">当前设置</div>
    <div class="grp slist">${status.map(([ic, n, v, k, sub]) => row([`${k}/${sub}`, ic, n, esc(v)])).join("")}</div>
  </div>`;
  $$("[data-go]", view).forEach((a) => (a.onclick = () => { const [k, sub] = a.dataset.go.split("/"); go(k, sub); }));
  $$("#quick [data-q]").forEach((c) => (c.onchange = async () => {
    try { await oai("PATCH", "/groups/" + id, { [c.dataset.q]: c.checked }); toast(c.checked ? "已开启" : "已关闭"); } catch (e) { c.checked = !c.checked; toast(e.message, true); }
  }));
};

const VERIFY = [["off", "关闭", "不做入群验证"], ["join", "入群申请验证", "推荐：群开启「需要审核」，验证后自动批准"], ["restrict", "进群先禁言", "开放群：验证后解除，超时踢出"]];
const CAPTCHAS = [["image", "动态图片验证码", "看动图里的数字，点选正确选项（推荐）"], ["math", "算术题", "选出正确答案"], ["emoji", "点表情", "按文字点对应表情"], ["button", "点按钮", "最简单，群里直接点"], ["web", "网页验证", "Turnstile，需要域名"]];
const FILTER_ACTIONS = [["warn", "删除并警告"], ["delete", "只删除"], ["mute", "禁言 24 小时"], ["ban", "封禁"]];
const AI_ACTIONS = [["ban", "封禁"], ["mute", "禁言 24 小时"], ["warn", "删除并警告"]];
const ACTIONS = [["mute", "禁言 24 小时"], ["kick", "踢出"], ["ban", "封禁"]];
const PROTECT = [["cas", "CAS 黑名单", "进群时查共享黑名单"], ["profile_check", "入群资料检查", "昵称 / 简介带引流词、屏蔽词直接拒绝"], ["dup_check", "重复内容检测", "拦截复制粘贴的广告波"], ["night_on", "夜间模式", "时段内全员禁言（管理员除外）"]];
const LOCKS = [["lock_link", "链接"], ["lock_forward", "外部转发"], ["lock_button", "按钮消息"], ["lock_via_bot", "inline 机器人"], ["lock_story", "动态"], ["lock_sticker", "贴纸"], ["lock_gif", "GIF"], ["lock_media", "图片 / 视频 / 文件"], ["lock_voice", "语音 / 圆视频"], ["lock_invite", "其它群邀请链接"], ["lock_contact", "名片 / 位置"], ["lock_poll", "投票"], ["lock_game", "骰子 / 游戏"], ["lock_foreign", "外文消息", "阿拉伯文、俄文等为主的消息"]];

// 每个开关配一个图标（模块色）
const TG_ICON = {
  cas: ["shield", "#34c759"], profile_check: ["idcard", "#32ade6"], dup_check: ["copy", "#34c759"], night_on: ["moon", "#5856d6"],
  lock_link: ["link", "#0a7cff"], lock_forward: ["forward", "#34c759"], lock_button: ["mouse", "#ff9500"], lock_via_bot: ["bot", "#5856d6"],
  lock_story: ["story", "#ff2d55"], lock_sticker: ["sticker", "#ff9500"], lock_gif: ["film", "#ff2d55"], lock_media: ["image", "#32ade6"],
  lock_voice: ["mic", "#af52de"], lock_invite: ["users", "#ff3b30"], lock_contact: ["idcard", "#8e8e93"], lock_poll: ["poll", "#0a7cff"],
  lock_game: ["dice", "#ff9500"], lock_foreign: ["globe", "#5ac8fa"], ai: ["sparkles", "#af52de"], ai_all: ["users", "#af52de"], ai_image: ["image", "#af52de"],
  points_on: ["star", "#ffb300"], invite_on: ["userplus", "#34c759"], invite_notify: ["chat", "#0a7cff"], bio_scan: ["search", "#af52de"], clean_welcome: ["broom", "#34c759"],
  usdt_on: ["coin", "#f0b90b"],
};
const toggle = (g, [k, n, d]) => `<label class="tg">${TG_ICON[k] ? sq(TG_ICON[k][0], TG_ICON[k][1], 20, "tg-ic") : ""}<span class="t"><b>${n}</b>${d ? `<small>${d}</small>` : ""}</span><input type="checkbox" data-k="${k}" ${g[k] ? "checked" : ""}><span class="sw"></span></label>`;
const numField = (g, k, n, unit, min, max, hint) => `<label class="field fr"><span class="fl"><b>${n}</b>${hint ? `<small>${hint}</small>` : ""}</span><div class="num"><input type="number" inputmode="numeric" data-k="${k}" min="${min}" max="${max}" value="${esc(g[k])}"><em>${unit}</em></div></label>`;

// ---------- 群设置（拆成几个小页面，共用同一个保存条） ----------
// groupForm 加载当前群设置 → 渲染 → 绑定底部保存条。render(g, caps) 返回 HTML
async function groupForm(seq, render, after) {
  const groupID = state.gid;
  const [g, caps] = await Promise.all([oai("GET", "/groups/" + groupID), state.info ? Promise.resolve(state.info) : oai("GET", "/status").catch(() => ({}))]);
  if (!alive(seq)) return;
  if (!state.info && caps && caps.bot_username) state.info = caps;
  view.innerHTML = render(g, caps || {});
  bindForm(view, g, (diff) => oai("PATCH", "/groups/" + groupID, diff));
  if (after) after(g, caps || {}, groupID);
}
const tzName = () => (state.info && state.info.timezone) || "机器人配置的时区";
const card = (title, desc, body, extra = "") => `<div class="card"${extra}><div class="card-h"><div><h2>${title}</h2>${desc ? `<p>${desc}</p>` : ""}</div></div>${body}</div>`;

PAGES.gVerify = (seq) => groupForm(seq, (g, caps) => `
  ${!caps.captcha && g.captcha_type === "web" && g.verify_mode !== "off" ? `<div class="notice">${icon("alert")}<div>网页验证需要配置 Turnstile 和域名，现在不会生效。换成图片验证码等内置方式就行，不需要任何配置。</div></div>` : ""}
  ${card("验证方式", "新人怎么证明自己不是机器人", `<div class="seg">${VERIFY.map(([v, n, d]) => `<label><input type="radio" name="vm" data-k="verify_mode" value="${v}" ${g.verify_mode === v ? "checked" : ""}><b>${n}</b><small>${d}</small></label>`).join("")}</div>
    ${g.verify_mode === "join" ? `<p class="muted" style="margin:10px 0 0;font-size:12.5px">入群申请验证需要在 Telegram 群设置里打开「申请入群需管理员批准」（或邀请链接开启审核）。直接通过不需要审核的链接进群的人，会自动改为「进群先禁言」验证。</p>` : ""}`)}
  ${card("验证题目", "新人在私聊里完成（「点按钮」在群里直接点）；答错 3 次本次入群作废", `<div class="seg">${CAPTCHAS.map(([v, n, d]) => `<label><input type="radio" name="ct" data-k="captcha_type" value="${v}" ${g.captcha_type === v ? "checked" : ""}><b>${n}</b><small>${d}${v === "web" && !caps.captcha ? "（未配置）" : ""}</small></label>`).join("")}</div>
    <div class="fields" style="margin-top:14px">${selField("verify_timeout", "验证超时", [[60, "1 分钟"], [120, "2 分钟"], [300, "5 分钟"], [600, "10 分钟"], [1800, "30 分钟"], [3600, "1 小时"]].concat([60, 120, 300, 600, 1800, 3600].includes(g.verify_timeout) ? [] : [[g.verify_timeout, g.verify_timeout + " 秒"]]), g.verify_timeout, "超时拒绝申请 / 踢出")}</div>`)}`);

PAGES.gGate = (seq) => groupForm(seq, (g) => `
  ${card("入群风控", "在验证之前就挡掉明显的广告号", `<div class="toggles" style="grid-template-columns:1fr">${toggle(g, ["cas", "CAS 共享黑名单", "进群时查 cas.chat 黑名单，命中直接拒绝"])}${toggle(g, ["profile_check", "入群资料检查", "昵称带链接 / @ / 引流词，或简介带引流词、屏蔽词，直接拒绝"])}</div>`)}
  ${card("防爆群", "短时间大量进群（机器人号攻击）时自动关门", `<div class="fields">${selField("raid_limit", "1 分钟内进群超过", [[0, "关闭"], [5, "5 人"], [10, "10 人"], [15, "15 人"], [30, "30 人"], [50, "50 人"], [100, "100 人"]].concat([0, 5, 10, 15, 30, 50, 100].includes(g.raid_limit) ? [] : [[g.raid_limit, g.raid_limit + " 人"]]), g.raid_limit, "触发后 15 分钟内拒绝所有新人。群里 /antiraid stop 可提前解除")}</div>`)}`);

PAGES.gLocks = (seq) => groupForm(seq, (g) => card("消息锁定", "打开后普通成员不能发这类消息，发了删除并警告（管理员和白名单不受限）", `<div class="toggles">${LOCKS.map((x) => toggle(g, x)).join("")}</div>`));

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

function readFileData(file) {
  return new Promise((ok, bad) => {
    const fr = new FileReader();
    fr.onerror = () => bad(new Error("读取文件失败"));
    fr.onload = () => ok(fr.result);
    fr.readAsDataURL(file);
  });
}
const LT_STATUS = { open: ["ok", "进行中"], drawn: ["acc", "已开奖"], cancelled: ["", "已取消"] };
const fmtSize = (n) => (n >= 1 << 20 ? (n / (1 << 20)).toFixed(1) + " MB" : Math.max(1, Math.round(n / 1024)) + " KB");

// ---------- Markdown 编辑器（工具栏 + 预览） ----------
// 规则与后端 MarkdownToHTML 保持一致
function mdToHtml(md) {
  let s = esc(String(md || "").replace(/\r\n/g, "\n"));
  const keep = [];
  const hold = (v) => { keep.push(v); return `\u0000${keep.length - 1}\u0000`; };
  s = s.replace(/```(?:[a-zA-Z0-9_+-]*\n)?([\s\S]*?)```/g, (_, c) => hold(`<pre>${c.replace(/\n+$/, "")}</pre>`));
  s = s.replace(/`([^`\n]+)`/g, (_, c) => hold(`<code>${c}</code>`));
  s = s.replace(/!\[([^\]\n]+)\]\(tg:\/\/emoji\?id=(\d+)\)/g, (_, e) => hold(`<span class="cemoji" title="动态表情（Telegram 里会动）">${e}</span>`));
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
    `<span class="grow"></span><button type="button" data-mcap class="mdcap-btn" hidden>从 Telegram 获取</button><button type="button" data-mp class="mdprev-btn">预览</button>`;
  const prev = document.createElement("div");
  prev.className = "mdpreview";
  prev.hidden = true;
  // 最省事的方式放最前面：在 Telegram 里写好发给机器人
  const CMD = { welcome_ta: "/setwelcome", rules_ta: "/setrules" }[ta.id];
  const cap = document.createElement("div");
  cap.className = "tgcap";
  cap.innerHTML = `${sq("send", "#0a7cff", 20)}<div class="tc-t"><b>在 Telegram 里写</b><small>写好的消息原样保存：格式、动态表情、图片、按钮都保留${CMD ? `。也可以在群里回复一条消息发 <code>${CMD}</code>` : ""}</small></div><button type="button" class="pri" data-tgcap>${icon("send", 17)} 发给机器人</button>`;
  cap.querySelector("[data-tgcap]").onclick = () => bar.querySelector("[data-mcap]").click();
  ta.before(cap);
  ta.before(bar);
  ta.after(prev);
  const render = () => { prev.innerHTML = mdToHtml(ta.value) || '<span class="muted">（空）</span>'; };
  ta.addEventListener("input", () => { if (!prev.hidden) render(); });
  bar.addEventListener("mousedown", (e) => e.preventDefault()); // 点按钮不让输入框失去选区
  bar.addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (!b) return;
    if (b.dataset.mcap !== undefined) {
      captureFlow(ta.dataset.mediaTarget ? "any" : "text").then((c) => {
        if (!c) return;
        if (c.markdown) {
          const st = ta.selectionStart ?? ta.value.length;
          ta.setRangeText(c.markdown, st, ta.selectionEnd ?? st, "end");
          ta.dispatchEvent(new Event("input", { bubbles: true }));
        }
        const mt = ta.dataset.mediaTarget && document.querySelector(`[data-media="${ta.dataset.mediaTarget}"]`);
        if (c.media && mt && mt._set) mt._set(c.media);
        else if (c.media && !mt) toast("这里只能保存文字，图片没有用上", true);
      }).catch((e) => toast(e.message, true));
      return;
    }
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
  on("#bs_now", async () => { await oai("POST", `/groups/${groupID}/bioscan`, {}); toast("已开始扫描，人多时需要几分钟"); setTimeout(() => viewKey() === "people/bioscan" && route(), 4000); });
  $$("[data-bc]").forEach((b) => (b.onclick = act(b, async () => {
    const c = clusters[Number(b.dataset.bc)];
    if (!(await ask(`封禁这 ${c.users.length} 人？`, c.link, { danger: true, yes: "封禁" }))) return;
    const x = await oai("POST", `/groups/${groupID}/bioscan`, { ban_cluster: { report_id: rep.id, index: Number(b.dataset.bc) } });
    toast(`已封禁 ${x.banned} 人`);
  })));
};

PAGES.gbans = async (seq) => {
  const gb = await oai("GET", "/gbans");
  if (!alive(seq)) return;
  const uid = (sel) => { const v = Number($(sel).value.trim()); if (!Number.isInteger(v) || !v) throw new Error("请输入数字用户 ID（群里发 /id 可查）"); return v; };
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>全局封禁 <span class="pill">${gb.length}</span></h2><p>机器人所在的所有群都生效：进群、申请入群、发言都会被封。群里机器人主人可以 /gban</p></div></div>
    <div class="row"><input type="text" id="gu" placeholder="用户 ID" style="width:180px"><input type="text" id="gr" class="grow" placeholder="原因（可选）"><button class="danger" id="ga">全局封禁</button></div>
    <div class="tbl-wrap" style="margin-top:14px"><table class="tbl"><tr><th>用户 ID</th><th>原因</th><th>时间</th><th></th></tr>
    ${gb.map((x) => `<tr><td><code>${x.user_id}</code></td><td>${esc(x.reason) || `<span class="muted">—</span>`}</td><td class="muted">${fmtTime(x.created_at)}</td><td class="act"><button class="sm" data-dg="${x.user_id}">解除</button></td></tr>`).join("") || empty("inbox", "暂无", 4)}</table></div></div>`;
  on("#ga", async () => {
    const id = uid("#gu");
    if (!(await ask("全局封禁？", `用户 ${id} 会在所有群被封禁。`, { danger: true, yes: "封禁" }))) return;
    await oai("POST", "/gbans", { user_id: id, reason: $("#gr").value.trim() });
    route();
  });
  $$("[data-dg]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `/gbans/${b.dataset.dg}`); route(); })));
};

const REPORT_STATUS = { open: ["warn", "待处理"], deleted: ["", "已删除消息"], muted: ["warn", "已禁言"], banned: ["bad", "已封禁"], ignored: ["", "已忽略"] };
PAGES.reports = async () => {
  state.rtab = state.rtab || "open";
  const rs = await oai("GET", "/reports", null, { status: state.rtab === "open" ? "open" : "all" });
  const gname = (id) => { const x = groupOf(id); return x ? x.title : id; };
  const mine = state.rall ? rs : rs.filter((r) => String(r.group_id) === String(state.gid));
  const list = state.rtab === "open" ? mine : mine.filter((r) => r.status !== "open");
  if (state.info && state.rtab === "open") { state.info.open_reports = rs.length; renderNav(); renderBar(); }
  view.innerHTML = `<div class="card"><div class="card-h"><div class="tabs"><button class="${state.rtab === "open" ? "on" : ""}" data-rt="open">待处理</button><button class="${state.rtab === "done" ? "on" : ""}" data-rt="done">已处理</button></div>
    <label class="chk"><input type="checkbox" id="rall" ${state.rall ? "checked" : ""}><span>显示所有群的举报</span></label></div>
    <div class="tbl-wrap"><table class="tbl"><tr><th>#</th><th>群</th><th>被举报</th><th>内容</th><th>举报人</th><th>时间</th><th></th></tr>
    ${list.map((r) => `<tr><td class="muted">${r.id}</td><td>${esc(gname(r.group_id))}</td><td><b>${esc(r.target)}</b><br><span class="muted">${r.target_id}</span></td><td class="clip">${esc(r.text) || `<span class="muted">（非文字消息）</span>`}</td>
      <td>${esc(r.reporter)}${r.count > 1 ? ` ${pill("warn", r.count + " 人")}` : ""}</td><td class="muted">${fmtTime(r.created_at)}</td>
      <td class="act">${r.status === "open" ? `<button class="sm" data-ra="del" data-id="${r.id}">删除</button> <button class="sm" data-ra="mute" data-id="${r.id}">禁言</button> <button class="sm danger" data-ra="ban" data-id="${r.id}">封禁</button> <button class="sm ghost" data-ra="ign" data-id="${r.id}">忽略</button>`
        : `${pill(...(REPORT_STATUS[r.status] || ["", r.status]))} <span class="muted">${esc(r.handled_by || "")}</span>`}</td></tr>`).join("") || empty("flag", state.rtab === "open" ? "没有待处理的举报" : "还没有处理记录", 7)}</table></div></div>`;
  $$("[data-rt]").forEach((b) => (b.onclick = () => { state.rtab = b.dataset.rt; route(); }));
  $("#rall").onchange = () => { state.rall = $("#rall").checked; route(); };
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

// ======================= v1.7 页面 =======================

// 内容编辑器：Markdown 文本 + 从 Telegram 获取 + 媒体 + 按钮（笔记、自动回复、定时消息、发消息共用）
function contentEditor(prefix, c = {}, { media = true, buttons = true, rows = 6, placeholder = "支持 **粗体** *斜体* [链接](https://) 和动态表情" } = {}) {
  return `<label class="field"><span>内容</span><textarea id="${prefix}_text" rows="${rows}" data-media-target="${prefix}_media" placeholder="${placeholder}">${esc(c.text || "")}</textarea></label>
    ${media ? mediaField(prefix + "_media", c.media) : ""}
    ${buttons ? collapse("按钮（可选）", `<label class="field"><textarea id="${prefix}_btn" rows="3" placeholder="官网 - https://example.com | 客服 - https://t.me/xxx">${esc(c.buttons || "")}</textarea><small>每行一排，「文字 - 链接」，同一排多个用 | 隔开</small></label>`, !!c.buttons) : ""}`;
}
function initEditor(root, prefix) {
  mdEditor($(`#${prefix}_text`, root));
  bindMedia(root);
}
const editorVal = (root, prefix) => ({
  text: ($(`#${prefix}_text`, root) || {}).value || "",
  media: ($(`#${prefix}_media`, root) || {}).value || "",
  buttons: ($(`#${prefix}_btn`, root) || {}).value || "",
});
const cprev = (c) => `${c.msg_id ? pill("acc", "原消息") + " " : ""}${c.media ? pill("", mediaLabel(c.media)) + " " : ""}${esc((c.text || "").replace(/!\[([^\]]+)\]\(tg:\/\/emoji\?id=\d+\)/g, "$1").slice(0, 80))}`;

// ---------- 欢迎语与群规 ----------
PAGES.gWelcome = async (seq) => {
  const topics = await loadTopics(state.gid);
  await groupForm(seq, (g) => `
  <div class="card"><div class="card-h"><div><h2>欢迎语</h2><p>新人通过验证后发。可以配图 / 动图，可以带按钮；留空不发</p></div><button class="sm" id="wprev">${icon("chat", 14)} 私聊预览</button></div>
    <label class="field"><textarea data-k="welcome" id="welcome_ta" rows="6" data-media-target="wmedia" placeholder="欢迎 {name} 加入 **{group}**！&#10;群规 - https://t.me/xxx/1 | 官网 - https://example.com">${esc(g.welcome)}</textarea>
      <small>占位符：<code>{name}</code> 新人（可点击）<code>{first}</code> 名字 <code>{username}</code> @用户名 <code>{group}</code> 群名 <code>{id}</code>；末尾每行 <code>按钮文字 - https://链接</code> 生成按钮</small></label>
    ${mediaField("wmedia", g.welcome_media, { k: "welcome_media" })}
    <div class="fields" style="margin-top:14px">
      ${selField("welcome_ttl", "自动删除", [[0, "不删"], [60, "1 分钟后"], [120, "2 分钟后"], [300, "5 分钟后"], [600, "10 分钟后"], [3600, "1 小时后"], [86400, "1 天后"]], g.welcome_ttl)}
      ${topicSelect("wthread", g.welcome_thread, topics, { k: "welcome_thread", label: "发到哪个话题" })}
    </div>
    <div class="toggles" style="margin-top:12px">${toggle(g, ["clean_welcome", "只保留最新一条欢迎", "新人进来时删掉上一条，避免刷屏"])}</div></div>
  <div class="card"><div class="card-h"><div><h2>群规</h2><p>群友发 /rules 查看，同样支持格式和按钮</p></div></div>
    <label class="field"><textarea data-k="rules" id="rules_ta" rows="6" placeholder="1. 禁止广告&#10;2. 友善交流">${esc(g.rules)}</textarea></label></div>`,
  () => {
    mdEditor($("#welcome_ta")); mdEditor($("#rules_ta")); bindMedia(view);
    on("#wprev", async () => {
      if (!$("#savebar").hidden) throw new Error("先保存修改，再预览");
      const r = await oai("POST", `/groups/${state.gid}/welcome-preview`, {});
      toast(`已私聊发给 ${r.sent} 人` + (r.failed && r.failed.length ? `，${r.failed.length} 人没发出去（需要先私聊机器人）` : ""));
    });
  });
};

// ---------- 防护开关 ----------
PAGES.gProtect = (seq) => groupForm(seq, (g) => `
  ${card("新人限制", "广告号几乎都是新进群的人：对新人从严、老成员从宽", `<div class="fields">
    ${selField("newbie_msgs", "前几条消息严格检查", [[0, "关闭"], [3, "前 3 条"], [5, "前 5 条"], [10, "前 10 条"], [20, "前 20 条"]], g.newbie_msgs, "禁止发链接，并交给 AI 审核")}
    ${selField("newbie_hours", "进群后只能发文字", [[0, "关闭"], [1, "1 小时"], [6, "6 小时"], [24, "24 小时"], [72, "3 天"], [168, "7 天"]], g.newbie_hours, "这段时间不能发图片、视频、文件、贴纸和链接，到时间自动解除")}</div>`)}
  ${card("刷屏和广告", "", `<div class="fields">${numField(g, "flood_count", "刷屏：超过几条", "条", 0, 100, "超过就删掉并禁言 10 分钟，0 关闭")}${numField(g, "flood_second", "刷屏：在多少秒内", "秒", 1, 600)}</div>
    <div class="toggles one" style="border-top:1px solid var(--line)">${toggle(g, ["dup_check", "重复内容检测", "同一人连发 3 次，或 3 人发同一段带链接的内容"])}</div>`)}
  ${card("夜间模式", `时段内全员禁言（管理员除外），按 ${esc(tzName())}，可以跨零点`, `<div class="toggles one">${toggle(g, ["night_on", "开启夜间模式"])}</div>
    <div class="fields" style="border-top:1px solid var(--line)"><label class="field fr"><span class="fl"><b>开始</b></span><input class="tm" type="time" data-k="night_start" value="${esc(g.night_start)}"></label><label class="field fr"><span class="fl"><b>结束</b></span><input class="tm" type="time" data-k="night_end" value="${esc(g.night_end)}"></label></div>`)}`);

// ---------- AI 审核（群） ----------
PAGES.gAI = (seq) => groupForm(seq, (g, caps) => `
  ${!caps.ai ? `<div class="notice info">${icon("alert")}<div>AI 接口还没配置：先到左侧「AI 接口与学习」填写接口地址、密钥和模型。</div></div>` : ""}
  ${g.ai && !g.ai_all && !g.newbie_msgs && !g.newbie_hours ? `<div class="notice warn">${icon("alert")}<div>AI 审核开着，但没有开「新人严格检查」也没开「审核所有人」，AI 实际不会审任何消息。</div></div>` : ""}
  ${card(`本群 AI 审核 ${caps.ai ? pill("ok", "已连接 " + esc(caps.ai_model || "")) : pill("warn", "未配置")}`, "用模型判断广告、博彩、诈骗等垃圾消息", `
    <div class="toggles">${toggle(g, ["ai", "开启 AI 审核", "可疑消息交给 AI 判断"])}${toggle(g, ["ai_all", "审核所有人", "关闭时只审新人（省钱）"])}${toggle(g, ["ai_image", "AI 看图片", "新人发的图片也给 AI 看，需要模型支持图片"])}</div>
    <div class="fields" style="margin-top:14px">${selField("ai_action", "判定为垃圾后", AI_ACTIONS, g.ai_action)}</div>`)}
  ${card("测试一下", "输入一段文字，看 AI 会不会拦", `<div class="row"><input type="text" id="aitext" class="grow" placeholder="例：加我微信 xxx，日结 500，稳赚不赔"><button id="aibtn">测试</button></div><div id="aires" style="margin-top:10px"></div>`)}`,
  () => {
    const t = async () => {
      const text = $("#aitext").value.trim();
      if (!text) return;
      $("#aires").innerHTML = `<span class="muted">AI 判断中…</span>`;
      try {
        const r = await oai("POST", "/ai/test", { text });
        $("#aires").innerHTML = `${r.hit ? pill("bad", "会被拦截") : pill("ok", "放行")} <span class="muted">${esc(r.category || "")} · 置信度 ${Math.round((r.confidence || 0) * 100)}%（阈值 ${Math.round((r.threshold || 0) * 100)}%）· ${esc(r.reason || "")} · ${r.ms} ms</span>`;
      } catch (e) { $("#aires").innerHTML = `${pill("bad", "调用失败")} <span class="muted">${esc(e.message)}</span>`; }
    };
    on("#aibtn", t);
    onEnter("#aitext", t);
  });

// ---------- 警告规则 ----------
PAGES.gWarnRules = (seq) => groupForm(seq, (g) => `
  ${card("警告规则", "规则命中（锁定、屏蔽词、新人发链接等）会删除消息并警告一次；管理员也可以 /warn", `<div class="fields">
    ${selField("warn_limit", "警告上限", [[1, "1 次"], [2, "2 次"], [3, "3 次"], [5, "5 次"], [10, "10 次"]], g.warn_limit)}
    ${selField("warn_action", "满了以后", ACTIONS, g.warn_action)}
    ${selField("warn_expire", "警告有效期", [[0, "永久"], [24, "1 天"], [72, "3 天"], [168, "7 天"], [720, "30 天"]], g.warn_expire)}</div>`)}
  ${card("群友自治", "管理员不在线时，让群友自己处理明显的广告", `<div class="fields">
    ${selField("report_auto", "举报自动禁言", [[0, "关闭"], [2, "2 人举报"], [3, "3 人举报"], [5, "5 人举报"], [10, "10 人举报"]], g.report_auto, "同一条消息被这么多不同的人 /report，先禁言 24 小时等管理员处理")}
    ${selField("vote_kick", "投票踢人", [[0, "关闭"], [3, "3 票"], [5, "5 票"], [10, "10 票"], [20, "20 票"]], g.vote_kick, "回复某人发 /votekick，10 分钟内满票踢出")}</div>`)}`);

// ---------- 私聊管理权限 ----------
PAGES.access = async (seq) => {
  const groupID = state.gid;
  const [g, admins] = await Promise.all([oai("GET", "/groups/" + groupID), oai("GET", `/groups/${groupID}/admins`).catch(() => [])]);
  if (!alive(seq)) return;
  const ids = new Set(String(g.panel_admins || "").split(/\s+/).filter(Boolean));
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>谁能在 Telegram 里管理本群</h2><p>指私聊里的按钮面板和群里 /settings 弹出的面板。机器人主人（配置 owner_ids）总是可以</p></div></div>
    <div class="seg"><label><input type="radio" name="pmode" value="" ${g.panel_mode !== "list" ? "checked" : ""}><b>所有管理员</b><small>群里有「封禁用户」权限的管理员 + 管理员白名单</small></label>
      <label><input type="radio" name="pmode" value="list" ${g.panel_mode === "list" ? "checked" : ""}><b>只限指定的人</b><small>只有下面勾选的人能管理，其他管理员点按钮会被拒绝</small></label></div>
    <div id="plist" ${g.panel_mode === "list" ? "" : "hidden"} style="margin-top:16px">
      <h3 class="sub">群里的管理员</h3>
      <div class="checks">${admins.map((a) => `<label class="chk"><input type="checkbox" class="pa" value="${a.id}" ${ids.has(String(a.id)) ? "checked" : ""}><span>${esc(a.name)}${a.owner ? ` ${pill("acc", "群主")}` : ""}${a.username ? ` <small class="muted">@${esc(a.username)}</small>` : ""}${!a.can_restrict && !a.owner ? ` <small class="muted">无封禁权限</small>` : ""}</span></label>`).join("") || `<p class="muted">读取不到管理员列表（机器人需要是管理员）</p>`}</div>
      <h3 class="sub" style="margin-top:16px">其他人（不是群管理员也可以）</h3>
      <div class="row">${userPicker("pextra")}<button id="padd">添加</button></div>
      <div class="chips" id="pchips">${[...ids].filter((id) => !admins.some((a) => String(a.id) === id)).map((id) => `<span class="chip" data-id="${id}">${id} <button data-rm>✕</button></span>`).join("")}</div>
    </div>
    <div class="row" style="margin-top:18px"><button class="pri" id="psave">保存</button></div></div>`;
  bindPickers(view);
  $$('input[name="pmode"]').forEach((r) => (r.onchange = () => ($("#plist").hidden = r.value !== "list" || !r.checked)));
  $("#pchips").onclick = (e) => { const b = e.target.closest("[data-rm]"); if (b) b.parentElement.remove(); };
  on("#padd", async () => {
    const id = pickedUID("#pextra");
    $("#pchips").insertAdjacentHTML("beforeend", `<span class="chip" data-id="${id}">${id} <button data-rm>✕</button></span>`);
    $("#pextra").value = "";
  });
  on("#psave", async () => {
    const mode = $('input[name="pmode"]:checked').value;
    const list = [...$$(".pa").filter((x) => x.checked).map((x) => x.value), ...$$("#pchips .chip").map((x) => x.dataset.id)];
    if (mode === "list" && !list.length && !(await ask("一个人都没选", "保存后只有机器人主人能在 Telegram 里管理本群，确定吗？", { danger: true, yes: "确定" }))) return;
    await oai("PATCH", "/groups/" + groupID, { panel_mode: mode, panel_admins: list.join(" ") });
    toast("已保存");
    route();
  });
};

// ---------- 积分签到 ----------
PAGES.gPoints = async (seq) => {
  const groupID = state.gid;
  await groupForm(seq, (g) => `
    ${card("签到积分", "群友发 /checkin 或「签到」得分；/me 查看自己，/ptop 积分榜。积分可以当抽奖门槛或参与费用", `
      <div class="toggles one">${toggle(g, ["points_on", "开启积分签到"])}</div>
      <div class="fields" style="margin-top:14px">
        ${numField(g, "checkin_points", "每次签到", "分", 1, 10000)}
        ${numField(g, "streak_bonus", "连续签到每天额外", "分", 0, 1000, "连续第 2 天起每天多加，最多累加 7 天")}
        ${selField("msg_points", "发言积分", [[0, "不加"], [1, "每条 +1"], [2, "每条 +2"], [5, "每条 +5"]], g.msg_points, "同一人 1 分钟内只算一次，少于 2 个字不算")}
        ${numField(g, "msg_points_cap", "每天发言积分上限", "分", 0, 100000)}
      </div>`)}
    ${card("自动发榜", `到点把发言榜（开了积分和邀请也带上积分榜、邀请榜）发到群里，按 ${esc(tzName())}`, `<div class="fields">
      ${selField("rank_post", "频率", [["", "不发"], ["daily", "每天"], ["weekly", "每周日"]], g.rank_post)}
      <label class="field"><span>时间</span><input type="time" data-k="rank_at" value="${esc(g.rank_at || "21:00")}"></label></div>`)}`);
  if (!alive(seq)) return;
  const r = await oai("GET", `/groups/${groupID}/points`, null, { limit: 50 });
  view.insertAdjacentHTML("beforeend", `<div class="card"><div class="card-h"><div><h2>积分榜 <span class="pill">${r.total}</span></h2><p>可以给某人加减积分（活动奖励、违规扣分）</p></div><button class="sm" id="padj">加减积分</button></div>
    <div class="tbl-wrap"><table class="tbl"><tr><th>#</th><th>成员</th><th class="r">积分</th><th></th></tr>
    ${(r.items || []).map((x, i) => `<tr><td class="muted">${i + 1}</td><td>${userCell(x.user_id, [x.first_name, x.last_name].filter(Boolean).join(" ") + (x.username ? " @" + x.username : ""))}</td><td class="r"><b>${x.value}</b></td><td class="act"><button class="sm" data-pa="${x.user_id}">加减</button></td></tr>`).join("") || empty("inbox", "还没有人有积分", 4)}</table></div></div>`);
  const adjust = async (uid) => {
    await sheet({ title: "加减积分", body: `<div class="stack"><label class="field"><span>成员</span>${userPicker("pu")}</label><label class="field"><span>积分</span><input type="number" id="pd" placeholder="正数加分，负数扣分，例如 -10"></label></div>`,
      onOpen: (w) => { bindPickers(w); if (uid) { $("#pu", w).value = uid; $("#pu", w).dataset.uid = uid; } },
      onOk: async (w) => {
        const delta = Number($("#pd", w).value);
        if (!delta) throw new Error("填一个不为 0 的数");
        const x = await oai("POST", `/groups/${groupID}/points`, { user_id: pickedUID("#pu", w), delta });
        toast(`当前 ${x.points} 分`);
        route();
      } });
  };
  on("#padj", () => adjust());
  $$("[data-pa]").forEach((b) => (b.onclick = () => adjust(b.dataset.pa)));
};

// ---------- 邀请统计 ----------
const INVITE_TPL = "🔗 你在「{group}」的专属邀请链接：\n{link}\n\n通过这个链接进群并完成验证的人会算在你名下，当前已邀请 {count} 人。";
PAGES.gInvites = async (seq) => {
  const groupID = state.gid;
  const r = await oai("GET", `/groups/${groupID}/invites`, null, { limit: 200 });
  if (!alive(seq)) return;
  const items = r.items || [];
  const sum = items.reduce((a, x) => ({ valid: a.valid + x.valid, pending: a.pending + x.pending, left: a.left + x.left }), { valid: 0, pending: 0, left: 0 });
  const nm = (x) => [x.first_name, x.last_name].filter(Boolean).join(" ") || (x.username ? "@" + x.username : String(x.user_id));
  const parts = (x) => [`有效 ${x.valid}`, x.pending ? `待验证 ${x.pending}` : "", x.left ? `退群 ${x.left}` : ""].filter(Boolean).join("　");
  await groupForm(seq, (g) => `
    <a class="kpi" style="cursor:default"><div><b>${r.total}</b><span>邀请人</span></div><div><b>${sum.valid}</b><span>有效</span></div><div><b>${sum.pending}</b><span>待验证</span></div><div><b>${sum.left}</b><span>已退群</span></div></a>
    ${card("设置", "", `<div class="toggles one">${toggle(g, ["invite_on", "邀请统计", "成员可以领自己的专属邀请链接"])}${toggle(g, ["invite_notify", "通知邀请人", "拉来的人通过验证时私聊告诉他"])}</div>
      <div class="fields" style="border-top:1px solid var(--line)">${numField(g, "invite_points", "每邀请 1 人奖励", "积分", 0, 10000, "需要同时开启积分签到")}</div>
      <div class="slist" style="border-top:1px solid var(--line)"><a id="invtext_row">${sq("send", "#32ade6", 19)}<span class="st"><b>领链接时的文案</b><span class="v">${g.invite_text ? "已自定义" : "默认文案"}</span></span>${icon("chev", 18)}</a></div>
      <textarea id="invtext_hidden" hidden>${esc(g.invite_text || "")}</textarea>`)}
    <div class="gt">谁拉了谁</div>
    <div class="grp slist">${items.map((x) => `<a data-inv="${x.user_id}" data-n="${esc(nm(x))}" data-l="${esc(x.link || "")}"><span class="sq" style="--c:#8e8e93;font-weight:700">${esc([...nm(x)][0] || "?")}</span><span class="st"><b>${esc(nm(x))}</b><span class="v">${parts(x)}</span></span><span class="cnt">${x.total}</span>${icon("chev", 18)}</a>`).join("") || `<div class="empty sm">还没有人领过邀请链接</div>`}</div>
    <div class="gt">成员怎么用</div>
    <div class="grp slist">
      <a>${sq("link", "#0a7cff", 19)}<span class="st"><b>领链接</b><span class="v">私聊机器人发「邀请链接」</span></span></a>
      <a>${sq("users", "#34c759", 19)}<span class="st"><b>看自己拉的人</b><span class="v">私聊机器人发「我邀请的人」</span></span></a>
      <a>${sq("star", "#ff9500", 19)}<span class="st"><b>邀请榜</b><span class="v">群里发 /invites</span></span></a>
    </div>`);
  if (!alive(seq)) return;
  const row = $("#invtext_row"), hid = $("#invtext_hidden");
  if (row) row.onclick = () => sheet({ title: "领链接时的文案", sub: "占位符 {link} 链接、{count} 已邀请人数、{group} 群名、{name} 成员；末尾每行「按钮文字 - 链接」加按钮", wide: true,
    body: `<textarea id="invtext_ta" rows="7" placeholder="${esc(INVITE_TPL)}">${esc(hid.value)}</textarea><div class="row" style="margin-top:12px"><button type="button" class="ghost sm" id="invtext_reset">恢复默认</button></div>`,
    onOpen: (w) => { mdEditor($("#invtext_ta", w)); $("#invtext_reset", w).onclick = () => { $("#invtext_ta", w).value = ""; }; },
    onOk: async (w) => {
      const v = $("#invtext_ta", w).value;
      await oai("PATCH", "/groups/" + groupID, { invite_text: v });
      hid.value = v; $(".v", row).textContent = v ? "已自定义" : "默认文案"; toast("已保存");
    } });
  $$("[data-inv]").forEach((b) => (b.onclick = async () => {
    const d = await oai("GET", `/groups/${groupID}/invites/${b.dataset.inv}`, null, { limit: 500 });
    const st = (x) => x.left ? pill("", "已退群") : x.verified ? pill("ok", "有效") : pill("warn", "待验证");
    await sheet({ title: `${esc(b.dataset.n)} 拉来的人`, sub: `共 ${d.total} 人`, wide: true, footer: false,
      body: `${b.dataset.l ? `<div class="row" style="margin-bottom:14px"><input class="grow" readonly value="${b.dataset.l}"><button class="sm" id="invcopy">${icon("copy", 16)} 复制</button></div>` : ""}
        <div class="slist">${(d.items || []).map((x) => `<a><span class="sq" style="--c:#8e8e93;font-weight:700">${esc([...nm(x)][0] || "?")}</span><span class="st"><b>${esc(nm(x))}</b><span class="v">${fmtTime(x.created_at)} 进群</span></span>${st(x)}</a>`).join("") || `<div class="empty sm">还没有人通过他的链接进群</div>`}</div>`,
      onOpen: (w) => { const c = $("#invcopy", w); if (c) c.onclick = () => navigator.clipboard.writeText(b.dataset.l).then(() => toast("已复制")); } });
  }));
};

PAGES.gRanks = async (seq) => {
  const groupID = state.gid;
  state.rk = state.rk || "day";
  const r = await oai("GET", `/groups/${groupID}/ranks`, null, { kind: state.rk === "week" ? "week" : "day" });
  if (!alive(seq)) return;
  view.innerHTML = `<div class="card"><div class="card-h"><div class="tabs"><button data-rk="day" class="${state.rk === "day" ? "on" : ""}">今天</button><button data-rk="week" class="${state.rk === "week" ? "on" : ""}">近 7 天</button></div><span class="muted">群里发 /top、/top week 也能看；管理员发言也计入</span></div>
    <pre class="rank">${esc(r.text)}</pre></div>`;
  $$("[data-rk]").forEach((b) => (b.onclick = () => { state.rk = b.dataset.rk; route(); }));
};

// ---------- 笔记与自动回复 ----------
const MATCHES = [["", "包含关键词"], ["exact", "完全一致"], ["prefix", "以关键词开头"], ["regex", "正则"]];
const NOTE_KIND = { photo: ["image", "#32ade6", "图片"], video: ["film", "#ff2d55", "视频"], animation: ["film", "#ff9500", "动图"], document: ["copy", "#5856d6", "文件"], audio: ["mic", "#af52de", "音频"], voice: ["mic", "#af52de", "语音"], sticker: ["sticker", "#ff9500", "贴纸"] };
const noteKind = (n) => NOTE_KIND[String(n.media || "").split(":")[0]] || ["note", "#34c759", "文字"];
const COOLDOWNS = [[0, "30 秒（默认）"], [-1, "不限制"], [10, "10 秒"], [60, "1 分钟"], [300, "5 分钟"], [600, "10 分钟"], [1800, "30 分钟"], [3600, "1 小时"], [21600, "6 小时"], [86400, "1 天"]];
const CHANCES = [[0, "每次都回"], [75, "75%"], [50, "50%"], [25, "25%"], [10, "10%"]];
const cdText = (s) => s >= 86400 && s % 86400 === 0 ? `${s / 86400} 天` : s >= 3600 && s % 3600 === 0 ? `${s / 3600} 小时` : s >= 60 && s % 60 === 0 ? `${s / 60} 分钟` : `${s} 秒`;
const freqText = (r) => (r.cooldown < 0 ? "不限频率" : `${r.per_user ? "每人" : "全群"} ${cdText(r.cooldown || 30)}内回 1 次`) + (r.chance > 0 && r.chance < 100 ? `，${r.chance}% 概率` : "");
const plain = (t) => String(t || "").replace(/!\[([^\]]+)\]\(tg:\/\/emoji\?id=\d+\)/g, "$1").replace(/[*_`~|>#]/g, "").trim();

PAGES.content = async (seq) => {
  const groupID = state.gid;
  const [ns, rs] = await Promise.all([oai("GET", `/groups/${groupID}/notes`), oai("GET", `/groups/${groupID}/replies`)]);
  if (!alive(seq)) return;
  const noteRow = (n) => { const [ic, c, kn] = noteKind(n); const sub = [kn, n.file_name || plain(n.text).slice(0, 40), n.views ? `看过 ${n.views} 次` : ""].filter(Boolean).join(" · ");
    return `<a data-note="${n.id}">${sq(ic, c, 19)}<span class="st"><b>#${esc(n.name)}</b><span class="v">${esc(sub)}</span></span>${icon("chev", 18)}</a>`; };
  const replyRow = (r) => `<a data-reply="${esc(r.keyword)}">${sq("chat", "#0a7cff", 19)}<span class="st"><b>${esc(r.keyword)}</b><span class="v">${esc([(MATCHES.find((m) => m[0] === (r.match || "")) || MATCHES[0])[1], freqText(r), r.delete_after ? `${r.delete_after} 秒后删` : ""].filter(Boolean).join(" · "))}</span></span>${icon("chev", 18)}</a>`;
  view.innerHTML = `
    <div class="gh"><div class="gt">笔记 · ${ns.length} 条</div><button class="pri sm" id="nnew">＋ 新建</button></div>
    <div class="grp slist">${ns.map(noteRow).join("") || `<div class="empty sm">还没有笔记。群里回复一条消息发 /save 名字 就能存</div>`}</div>
    ${ns.length ? `<div class="grp slist"><a id="nlogs">${sq("clock", "#8e8e93", 19)}<span class="st"><b>取用记录</b><span class="v">谁在私聊里取过哪条笔记</span></span>${icon("chev", 18)}</a></div>` : ""}
    <p class="gfoot">笔记只在私聊里发：群友点目录按钮或发 <code>#名字</code>，机器人不在群里发内容，只引导他私信获取。</p>
    <div class="gh"><div class="gt">自动回复 · ${rs.length} 条</div><button class="pri sm" id="rnew">＋ 新建</button></div>
    <div class="grp slist">${rs.map(replyRow).join("") || `<div class="empty sm">还没有自动回复</div>`}</div>
    <p class="gfoot">每条都可以单独设频率：多久回一次、按全群还是按每个人算、多大概率回。</p>`;

  const noteSheet = (n) => sheet({ title: n ? "编辑笔记 #" + esc(n.name) : "新建笔记", wide: true,
    body: `<div class="stack"><label class="field"><span>名字</span><input type="text" id="nn" value="${esc(n ? n.name : "")}" ${n ? "readonly" : ""} placeholder="例如 faq，群友发 #faq 查看"></label>${contentEditor("ne", n)}</div>`,
    onOpen: (w) => initEditor(w, "ne"),
    onOk: async (w) => { await oai("POST", `/groups/${groupID}/notes`, { name: $("#nn", w).value.trim(), ...editorVal(w, "ne") }); toast("已保存"); route(); } });

  // 取用记录：谁、哪条、什么时候
  const logRows = (ls, showNote) => ls.map((l) => `<div class="lrow"><span class="st"><b>${esc(l.name || "用户 " + l.user_id)}${l.username ? ` <span class="muted">@${esc(l.username)}</span>` : ""}</b><span class="v">${[showNote ? "#" + esc(l.note_name) : "", "ID " + l.user_id].filter(Boolean).join(" · ")}</span></span><span class="v">${fmtTime(l.created_at)}</span></div>`).join("");
  const logsSheet = async () => {
    const ls = await oai("GET", `/groups/${groupID}/notes/logs`, null, { limit: 300 });
    sheet({ title: "取用记录", sub: `最近 ${ls.length} 次`, wide: true, footer: false,
      body: `<div class="grp slist">${logRows(ls, true) || `<div class="empty sm">还没有人在私聊里取过笔记</div>`}</div>` });
  };
  const lb = $("#nlogs", view); if (lb) lb.onclick = act(lb, logsSheet);

  // 笔记详情：图片 / 视频 / 音频直接显示，文件给下载
  const noteView = (n) => { const [ic, c, kn] = noteKind(n);
    sheet({ title: "#" + esc(n.name), sub: [kn, n.created_by ? `${esc(n.created_by)} 保存` : "", n.updated_at && !String(n.updated_at).startsWith("0001") ? fmtTime(n.updated_at) : "", `看过 ${n.views || 0} 次`].filter(Boolean).join(" · "), wide: true, footer: false,
      body: `${n.media ? `<div class="npv" id="npv"><div class="npv-load">${sq(ic, c, 22)}<span>正在从 Telegram 取${kn}…</span></div></div>` : n.msg_id ? `<div class="npv"><div class="npv-load">${sq("note", "#8e8e93", 22)}<span>这条是旧版存的「原消息复制」，面板看不到图片。群里回复原消息再 /save ${esc(n.name)} 一次就能预览</span></div></div>` : ""}
        ${n.text ? `<div class="mdpreview npv-text">${mdToHtml(n.text)}</div>` : ""}
        ${n.buttons ? `<div class="muted small" style="margin-top:10px">按钮：${esc(n.buttons)}</div>` : ""}
        <div class="row" style="margin-top:18px"><button class="sm" id="nedit">编辑</button><button class="sm danger" id="ndel">删除</button></div>
        <div class="gh" style="margin-top:22px"><div class="gt">取用记录</div></div><div class="grp slist" id="nlog"><div class="empty sm">加载中…</div></div>`,
      onOpen: async (w, close) => {
        $("#nedit", w).onclick = () => { close(); noteSheet(n); };
        $("#ndel", w).onclick = act($("#ndel", w), async () => { if (await ask("删除笔记？", "#" + n.name, { danger: true, yes: "删除" })) { await oai("DELETE", `/groups/${groupID}/notes`, null, { name: n.name }); close(); route(); } });
        oai("GET", `/groups/${groupID}/notes/logs`, null, { note: n.id, limit: 100 }).then((ls) => { const el = $("#nlog", w); if (el) el.innerHTML = logRows(ls, false) || `<div class="empty sm">还没有人取过这条</div>`; }).catch(() => {});
        const box = $("#npv", w);
        if (!box) return;
        try {
          const d = await oai("GET", "/media", null, { ref: n.media });
          const nm = esc(n.file_name || d.name || "文件");
          if (d.too_big || !d.data) { box.innerHTML = `<div class="npv-load">${sq(ic, c, 22)}<span>${esc(d.message || "无法预览")}${d.size ? `（${fmtSize(d.size)}）` : ""}</span></div>`; return; }
          const k = String(n.media).split(":")[0];
          if (k === "photo" || k === "sticker" || d.mime.startsWith("image/")) box.innerHTML = `<img src="${d.data}" alt="${nm}">`;
          else if (k === "video" || k === "animation" || d.mime.startsWith("video/")) box.innerHTML = `<video src="${d.data}" controls playsinline ${k === "animation" ? "autoplay loop muted" : ""}></video>`;
          else if (k === "audio" || k === "voice" || d.mime.startsWith("audio/")) box.innerHTML = `<div class="npv-file">${sq(ic, c, 22)}<span class="st"><b>${nm}</b><span class="v">${fmtSize(d.size)}</span></span></div><audio src="${d.data}" controls style="width:100%;margin-top:12px"></audio>`;
          else box.innerHTML = `<div class="npv-file">${sq(ic, c, 22)}<span class="st"><b>${nm}</b><span class="v">${esc(d.mime)} · ${fmtSize(d.size)}</span></span><a class="btn sm pri" href="${d.data}" download="${nm}">下载</a></div>`;
        } catch (e) { box.innerHTML = `<div class="npv-load">${sq("alert", "#ff9500", 22)}<span>${esc(e.message)}</span></div>`; }
      } }); };

  const replySheet = (r) => sheet({ title: r ? "编辑自动回复" : "新建自动回复", wide: true,
    body: `<div class="stack"><div class="fields">
      <label class="field"><span>关键词</span><input type="text" id="rk" value="${esc(r ? r.keyword : "")}" ${r ? "readonly" : ""}></label>
      <label class="field fr"><span class="fl"><b>匹配方式</b></span><select id="rm">${opt(MATCHES, r && r.match)}</select></label>
      <label class="field fr"><span class="fl"><b>多久回一次</b><small>同一个关键词在这段时间里只回 1 次</small></span><select id="rcd">${opt(COOLDOWNS, r ? r.cooldown || 0 : 0)}</select></label>
      <label class="field fr"><span class="fl"><b>按谁计算</b><small>选「每人」：每个人各自计时，适合「价格」这类常问词</small></span><select id="rpu">${opt([["0", "全群一起算"], ["1", "每个人分别算"]], r && r.per_user ? "1" : "0")}</select></label>
      <label class="field fr"><span class="fl"><b>回复概率</b><small>不想每次都回，可以调低</small></span><select id="rch">${opt(CHANCES, r ? r.chance || 0 : 0)}</select></label>
      <label class="field fr"><span class="fl"><b>回复后自动删除</b></span><select id="rd">${opt([[0, "不删"], [30, "30 秒"], [60, "1 分钟"], [300, "5 分钟"]], r && r.delete_after)}</select></label></div>
      ${contentEditor("re", r, { placeholder: "回复内容；多条随机回复用单独一行 --- 分隔" })}
      ${r ? `<div class="row"><button type="button" class="sm danger" id="rdel">删除这条自动回复</button></div>` : ""}</div>`,
    onOpen: (w, close) => { initEditor(w, "re"); const d = $("#rdel", w); if (d) d.onclick = act(d, async () => { if (await ask("删除自动回复？", r.keyword, { danger: true, yes: "删除" })) { await oai("DELETE", `/groups/${groupID}/replies`, null, { keyword: r.keyword }); close(); route(); } }); },
    onOk: async (w) => { await oai("POST", `/groups/${groupID}/replies`, { keyword: $("#rk", w).value.trim(), match: $("#rm", w).value, delete_after: Number($("#rd", w).value),
      cooldown: Number($("#rcd", w).value), per_user: $("#rpu", w).value === "1", chance: Number($("#rch", w).value), ...editorVal(w, "re") }); toast("已保存"); route(); } });

  on("#nnew", () => noteSheet());
  on("#rnew", () => replySheet());
  $$("[data-note]").forEach((b) => (b.onclick = () => noteView(ns.find((n) => String(n.id) === b.dataset.note))));
  $$("[data-reply]").forEach((b) => (b.onclick = () => replySheet(rs.find((r) => r.keyword === b.dataset.reply))));
};

// ---------- 定时消息 ----------
PAGES.schedules = async (seq) => {
  const groupID = state.gid;
  const [ss, topics] = await Promise.all([oai("GET", `/groups/${groupID}/schedules`), loadTopics(groupID)]);
  if (!alive(seq)) return;
  const tname = (id) => (topics.find((t) => t.thread_id === id) || {}).name;
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>定时消息 <span class="pill">${ss.length}</span></h2><p>每天固定时间发一次，按 ${esc(tzName())}；机器人卡顿时 10 分钟内会补发。要按间隔发、自动删上一条、置顶，用「定时广告」</p></div><button class="pri sm" id="snew">＋ 新建</button></div>
    <div class="tbl-wrap"><table class="tbl"><tr><th>时间</th><th>内容</th><th>上次发送</th><th></th></tr>
    ${ss.map((s) => `<tr><td><b class="big-time">${esc(s.at)}</b>${s.thread_id ? `<div class="muted small">→ ${esc(tname(s.thread_id) || "话题 #" + s.thread_id)}</div>` : ""}</td><td class="clip">${cprev(s)}</td><td class="muted">${esc(s.last_run || "—")}</td><td class="act"><button class="sm danger" data-ds="${s.id}">删除</button></td></tr>`).join("") || empty("clock", "还没有定时消息", 4)}</table></div></div>`;
  on("#snew", () => sheet({ title: "新建定时消息", wide: true,
    body: `<div class="stack"><div class="fields"><label class="field"><span>每天几点</span><input type="time" id="sat" value="09:00"></label>${topicSelect("sth", 0, topics)}</div>${contentEditor("se")}</div>`,
    onOpen: (w) => initEditor(w, "se"),
    onOk: async (w) => { await oai("POST", `/groups/${groupID}/schedules`, { at: $("#sat", w).value, thread_id: Number(($("#sth", w) || {}).value || 0), ...editorVal(w, "se") }); toast("已添加"); route(); } }));
  $$("[data-ds]").forEach((b) => (b.onclick = act(b, async () => { if (await ask("删除这条定时消息？", "", { danger: true, yes: "删除" })) { await oai("DELETE", `/groups/${groupID}/schedules/${b.dataset.ds}`); route(); } })));
};

// ---------- 发一条消息 ----------
PAGES.gSend = async (seq) => {
  const groupID = state.gid;
  const topics = await loadTopics(groupID);
  if (!alive(seq)) return;
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>以机器人身份发消息</h2><p>支持格式、动态表情、图片 / 动图和按钮</p></div></div>
    <div class="stack">${contentEditor("sd", {}, { rows: 7 })}${topicSelect("sdth", 0, topics)}<div class="row"><button class="pri" id="sdgo">发送到群里</button></div></div></div>`;
  initEditor(view, "sd");
  on("#sdgo", async () => {
    const body = { ...editorVal(view, "sd"), thread_id: Number(($("#sdth") || {}).value || 0) };
    if (!body.text.trim() && !body.media) throw new Error("内容是空的");
    if (!(await ask("发送到群里？", body.text.slice(0, 200) || mediaLabel(body.media), { yes: "发送" }))) return;
    await oai("POST", `/groups/${groupID}/send`, body);
    toast("已发送");
    route();
  });
};

// ---------- 定时广告 ----------
PAGES.ads = async (seq) => {
  const groupID = state.gid;
  const [as, topics] = await Promise.all([oai("GET", `/groups/${groupID}/ads`), loadTopics(groupID)]);
  if (!alive(seq)) return;
  const when = (a) => (a.times ? "每天 " + a.times : a.interval % 60 === 0 ? `每 ${a.interval / 60} 小时` : `每 ${a.interval} 分钟`);
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>定时广告 <span class="pill">${as.length}</span></h2><p>按固定时间或间隔自动发，可以删掉上一条、自动置顶；夜间模式期间不发</p></div><button class="pri sm" id="anew">＋ 新建广告</button></div>
    <div class="ad-grid">${as.map((a) => `<div class="ad-card${a.enabled ? "" : " off"}"><div class="ad-top"><b>${esc(a.name || "未命名")}</b>${a.enabled ? pill("ok", "启用") : pill("", "暂停")}</div>
      <div class="ad-body clip2">${cprev({ text: a.text, media: a.photo && a.photo.includes(":") && !a.photo.startsWith("local:") && !a.photo.startsWith("http") ? a.photo : "", msg_id: a.msg_id }) || '<span class="muted">（复制原消息）</span>'}</div>
      <div class="ad-meta">${icon("clock", 14)} ${esc(when(a))} · 已发 ${a.sent} 次${a.photo ? " · 带图" : ""}${a.pin ? " · 置顶" : ""}</div>
      <div class="ad-act"><button class="sm" data-ae="${a.id}">编辑</button><button class="sm" data-at="${a.id}">${a.enabled ? "暂停" : "启用"}</button><button class="sm" data-ap="${a.id}">预览</button><button class="sm" data-as="${a.id}">立即发</button><button class="sm danger" data-ad="${a.id}">删除</button></div></div>`).join("") || `<div class="empty"><span class="e">${icon("flag", 30)}</span>还没有定时广告</div>`}</div></div>`;
  const edit = (ed) => {
    const e = ed || { name: "", text: "", photo: "", buttons: "", times: "", interval: 120, enabled: true, delete_prev: true, pin: false, thread_id: 0 };
    const mode0 = e.times ? "times" : "interval";
    const capMedia = e.photo && /^(photo|animation|video|document):/.test(e.photo) ? e.photo : "";
    const ivals = [[30, "30 分钟"], [60, "1 小时"], [120, "2 小时"], [180, "3 小时"], [360, "6 小时"], [720, "12 小时"], [1440, "1 天"]];
    if (!ivals.some((x) => x[0] === e.interval)) ivals.push([e.interval, e.interval + " 分钟"]);
    return sheet({ title: ed ? "编辑广告" : "新建广告", sub: ed && ed.msg_id ? "这条是在群里用 /ad 回复消息创建的，发出时复制原消息；改了文字或图片后按这里的内容发" : "", wide: true,
      body: `<div class="stack">
        <label class="field"><span>名字</span><input type="text" id="ad_name" value="${esc(e.name)}" placeholder="方便自己区分"></label>
        <div class="seg two"><label><input type="radio" name="ad_mode" value="times" ${mode0 === "times" ? "checked" : ""}><b>每天固定时间</b><small>例如 09:00 和 21:00</small></label><label><input type="radio" name="ad_mode" value="interval" ${mode0 === "interval" ? "checked" : ""}><b>按间隔</b><small>例如每 2 小时</small></label></div>
        <div id="ad_tw" class="field" ${mode0 === "times" ? "" : "hidden"}><span>时间（按 ${esc(tzName())}）</span><div class="chips" id="ad_tchips">${(e.times ? e.times.split(",") : []).map((t) => `<span class="chip" data-t="${t}">${t} <button data-rm>✕</button></span>`).join("")}</div><div class="row"><input type="time" id="ad_tnew" value="09:00"><button type="button" class="sm" id="ad_tadd">＋ 添加时间</button></div></div>
        <label id="ad_iw" class="field" ${mode0 === "interval" ? "" : "hidden"}><span>间隔</span><select id="ad_int">${opt(ivals, e.interval)}</select></label>
        <label class="field"><span>内容（Markdown）</span><textarea id="ad_text" rows="7" data-media-target="ad_media">${esc(e.text)}</textarea><small>带图片时最多 1024 字（Telegram 限制）</small></label>
        ${mediaField("ad_media", capMedia, { label: "图片 / 动图 / 视频" })}
        ${collapse("或者上传本地图片 / 填图片链接", `<div class="row"><label class="filebtn"><input type="file" id="ad_file" accept="image/*">${icon("upload", 16)}<span id="ad_fname">上传图片</span></label><input type="text" id="ad_photo" class="grow" value="${esc(e.photo && /^https?:/.test(e.photo) ? e.photo : "")}" placeholder="https://…"></div>${e.photo && !capMedia ? `<label class="chk" style="margin-top:10px"><input type="checkbox" id="ad_nophoto"><span>去掉现在的图片</span></label>` : ""}`)}
        ${collapse("按钮", `<label class="field"><textarea id="ad_btn" rows="3" placeholder="官网 - https://example.com | 客服 - https://t.me/xxx">${esc(e.buttons)}</textarea><small>每行一排，「文字 - 链接」，同一排多个用 | 隔开</small></label>`, !!e.buttons)}
        ${topicSelect("ad_th", e.thread_id, topics)}
        <div class="toggles"><label class="tg"><span class="t"><b>启用</b></span><input type="checkbox" id="ad_on" ${e.enabled ? "checked" : ""}><span class="sw"></span></label>
          <label class="tg"><span class="t"><b>删掉上一条</b><small>发新的之前删除上次发的</small></span><input type="checkbox" id="ad_dp" ${e.delete_prev ? "checked" : ""}><span class="sw"></span></label>
          <label class="tg"><span class="t"><b>发送后置顶</b><small>静默置顶，需要置顶权限</small></span><input type="checkbox" id="ad_pin" ${e.pin ? "checked" : ""}><span class="sw"></span></label></div>
        <div class="row"><button type="button" class="sm" id="ad_prev">${icon("chat", 14)} 私聊预览</button><span class="muted small">预览私聊发给机器人主人和管理员白名单</span></div></div>`,
      onOpen: (w) => {
        mdEditor($("#ad_text", w)); bindMedia(w);
        $$('input[name="ad_mode"]', w).forEach((r) => (r.onchange = () => { const t = $('input[name="ad_mode"]:checked', w).value === "times"; $("#ad_tw", w).hidden = !t; $("#ad_iw", w).hidden = t; }));
        $("#ad_tadd", w).onclick = () => { const t = $("#ad_tnew", w).value; if (t && !$(`[data-t="${t}"]`, w)) $("#ad_tchips", w).insertAdjacentHTML("beforeend", `<span class="chip" data-t="${t}">${t} <button data-rm>✕</button></span>`); };
        $("#ad_tchips", w).onclick = (ev) => { const b = ev.target.closest("[data-rm]"); if (b) b.parentElement.remove(); };
        $("#ad_file", w).onchange = () => { const f = $("#ad_file", w).files[0]; $("#ad_fname", w).textContent = f ? f.name : "上传图片"; };
        $("#ad_prev", w).onclick = act($("#ad_prev", w), async () => { const body = await form(w); if (ed) body.id = ed.id; const r = await oai("POST", `/groups/${groupID}/ad-preview`, body); toast(`已私聊发给 ${r.sent} 人` + (r.failed && r.failed.length ? `，${r.failed.length} 人没发出去` : "")); });
      },
      onOk: async (w) => {
        const body = await form(w);
        if (ed) await oai("PATCH", `/groups/${groupID}/ads/${ed.id}`, body); else await oai("POST", `/groups/${groupID}/ads`, body);
        toast("已保存"); route();
      } });
    async function form(w) {
      const timesMode = $('input[name="ad_mode"]:checked', w).value === "times";
      const times = $$("#ad_tchips .chip", w).map((c) => c.dataset.t).sort().join(",");
      if (timesMode && !times) throw new Error("至少添加一个时间");
      const body = { name: $("#ad_name", w).value, times: timesMode ? times : "", interval: Number($("#ad_int", w).value), buttons: $("#ad_btn", w).value,
        enabled: $("#ad_on", w).checked, delete_prev: $("#ad_dp", w).checked, pin: $("#ad_pin", w).checked, thread_id: Number(($("#ad_th", w) || {}).value || 0) };
      if (!(ed && ed.msg_id) || $("#ad_text", w).value !== e.text) body.text = $("#ad_text", w).value;
      const f = $("#ad_file", w).files[0], url = $("#ad_photo", w).value.trim(), cm = $("#ad_media", w).value;
      if (f) body.photo_data = await readImage(f);
      else if (cm !== capMedia) body.photo = cm;
      else if ($("#ad_nophoto", w) && $("#ad_nophoto", w).checked) body.photo = "";
      else if (url && url !== e.photo) body.photo = url;
      return body;
    }
  };
  on("#anew", () => edit());
  $$("[data-ae]").forEach((b) => (b.onclick = () => edit(as.find((a) => a.id === Number(b.dataset.ae)))));
  $$("[data-at]").forEach((b) => (b.onclick = act(b, async () => { const a = as.find((x) => x.id === Number(b.dataset.at)); await oai("PATCH", `/groups/${groupID}/ads/${a.id}`, { enabled: !a.enabled }); route(); })));
  $$("[data-ap]").forEach((b) => (b.onclick = act(b, async () => { const r = await oai("POST", `/groups/${groupID}/ad-preview`, { id: Number(b.dataset.ap) }); toast(`预览已私聊发给 ${r.sent} 人` + (r.failed && r.failed.length ? `，${r.failed.length} 人没发出去` : "")); })));
  $$("[data-as]").forEach((b) => (b.onclick = act(b, async () => { if (!(await ask("现在发送？", "立即把这条广告发到群里。", { yes: "发送" }))) return; await oai("POST", `/groups/${groupID}/ads/${b.dataset.as}/send`, {}); toast("已发送"); route(); })));
  $$("[data-ad]").forEach((b) => (b.onclick = act(b, async () => { if (!(await ask("删除这条广告？", "", { danger: true, yes: "删除" }))) return; await oai("DELETE", `/groups/${groupID}/ads/${b.dataset.ad}`); route(); })));
};

// ---------- 群抽奖 ----------
PAGES.lottery = async (seq) => {
  const groupID = state.gid;
  const [ls, topics] = await Promise.all([oai("GET", `/groups/${groupID}/lotteries`), loadTopics(groupID)]);
  if (!alive(seq)) return;
  const rule = (l) => [l.draw_at ? fmtTime(l.draw_at) + " 开奖" : "", l.draw_count ? `满 ${l.draw_count} 人` : ""].filter(Boolean).join(" 或 ") || "手动";
  const conds = (l) => [l.require_chat ? "需加入 " + l.require_chat : "", l.min_msgs ? `发言≥${l.min_msgs}` : "", l.min_points ? `积分≥${l.min_points}` : "", l.cost_points ? `消耗 ${l.cost_points} 分` : "", l.min_invites ? `邀请≥${l.min_invites}` : "", l.old_only ? "仅老成员" : "", l.repeat ? { daily: "每天一期", weekly: "每周一期" }[l.repeat] : ""].filter(Boolean);
  const tiersOf = (l) => { try { return JSON.parse(l.tiers || "[]"); } catch { return []; } };
  const prize = (l) => (l.prize_type === "file" ? `文件 ${esc(l.file_name)}（${fmtSize(l.file_size)}）` : `卡密 ${l.codes} 条`);
  const openL = ls.filter((l) => l.status === "open"), doneL = ls.filter((l) => l.status !== "open");
  const lcard = (l) => `<div class="lt-card ${l.status}"><div class="lt-top"><b>#${l.id} ${esc(l.title)}</b>${pill(...(LT_STATUS[l.status] || ["", l.status]))}</div>
    <div class="lt-stats"><div><b>${l.entries}</b><span>参与</span></div><div><b>${l.winners}</b><span>中奖</span></div><div><b class="sm">${esc(rule(l))}</b><span>开奖</span></div></div>
    <div class="muted small">${prize(l)}${tiersOf(l).length ? " · " + tiersOf(l).map((t) => `${esc(t.name)}×${t.winners}`).join(" ") : ""}</div>
    ${conds(l).length ? `<div class="chips small">${conds(l).map((c) => `<span class="chip">${esc(c)}</span>`).join("")}</div>` : ""}
    <div class="lt-act"><button class="sm" data-lv="${l.id}">详情</button>${l.status === "open" ? `<button class="sm" data-ld="${l.id}">立即开奖</button><button class="sm danger" data-lc="${l.id}">取消</button>` : ""}${l.status === "drawn" ? `<button class="sm" data-lr="${l.id}">补发奖品</button>` : ""}</div></div>`;
  view.innerHTML = `<div class="card"><div class="card-h"><div><h2>进行中 <span class="pill">${openL.length}</span></h2><p>群里点按钮参与，到时间或满人数自动开奖；开奖按公开种子排序，可公开验证</p></div><button class="pri" id="lnew">＋ 发起抽奖</button></div>
      <div class="lt-grid">${openL.map(lcard).join("") || `<div class="empty"><span class="e">${icon("gift", 30)}</span>没有进行中的抽奖</div>`}</div></div>
    ${doneL.length ? `<div class="card">${collapse(`历史抽奖（${doneL.length}）`, `<div class="lt-grid">${doneL.map(lcard).join("")}</div>`)}</div>` : ""}`;
  on("#lnew", () => newLottery(groupID, topics));
  $$("[data-lv]").forEach((b) => (b.onclick = act(b, () => lotteryDetail(groupID, b.dataset.lv))));
  $$("[data-ld]").forEach((b) => (b.onclick = act(b, async () => { if (!(await ask("现在开奖？", "立即抽出中奖者，并私聊发奖。", { yes: "开奖" }))) return; const r = await oai("POST", `/groups/${groupID}/lotteries/${b.dataset.ld}/draw`, {}); toast(`已开奖，${r.winners} 人中奖`); route(); })));
  $$("[data-lc]").forEach((b) => (b.onclick = act(b, async () => { if (!(await ask("取消这个抽奖？", "群消息会标记为已取消，不会发奖。", { danger: true, yes: "取消抽奖" }))) return; await oai("POST", `/groups/${groupID}/lotteries/${b.dataset.lc}/cancel`, {}); route(); })));
  $$("[data-lr]").forEach((b) => (b.onclick = act(b, async () => { const r = await oai("POST", `/groups/${groupID}/lotteries/${b.dataset.lr}/resend`, {}); toast(r.sent || r.failed ? `补发成功 ${r.sent} 人，失败 ${r.failed} 人` : "所有中奖者都已收到"); })));
};

function newLottery(groupID, topics) {
  const d = new Date(Date.now() + 86400000), pad = (n) => String(n).padStart(2, "0");
  const defAt = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T20:00`;
  const tierRow = (i, t = {}) => `<div class="tier" data-tier><div class="fields"><label class="field"><span>奖项名</span><input type="text" class="tn" value="${esc(t.name || (i + 1) + " 等奖")}"></label><label class="field"><span>中奖人数</span><input type="number" class="tw" min="1" value="${t.winners || 1}"></label></div>
    <label class="field tc-w"><span>这个奖项的卡密</span><textarea class="tc" rows="3" placeholder="每行一条；只填一条表示这个奖项共用"></textarea></label><button type="button" class="sm ghost" data-rmtier>删除这个奖项</button></div>`;
  return sheet({ title: "发起抽奖", sub: "发出后群里出现「参与抽奖」按钮；奖品只在中奖后私聊发出", wide: true, okText: "发到群里",
    body: `<div class="stack">
      <div class="fields"><label class="field"><span>标题</span><input type="text" id="lt_title" maxlength="100" placeholder="例：周末福利 $10 余额"></label>
        <label class="field"><span>开奖时间</span><input type="datetime-local" id="lt_at" value="${defAt}"><small>按你电脑的时区（${esc(Intl.DateTimeFormat().resolvedOptions().timeZone || "本地")}）；留空只按人数</small></label>
        <label class="field"><span>或满多少人开奖</span><div class="num"><input type="number" id="lt_cnt" min="0" value="0"><em>人</em></div><small>0 不按人数；两个都填先到先开</small></label></div>
      <label class="field"><span>说明（可选）</span><textarea id="lt_desc" rows="3" placeholder="奖品说明、参与方式等"></textarea></label>
      <h3 class="sub">奖品</h3>
      <div class="seg two"><label><input type="radio" name="lt_type" value="code" checked><b>卡密</b><small>每个中奖者分一条；只填一条则共用</small></label><label><input type="radio" name="lt_type" value="file"><b>文件</b><small>每个中奖者收到同一个文件，最大 10MB</small></label></div>
      <div id="lt_single"><div class="fields"><label class="field"><span>中奖人数</span><div class="num"><input type="number" id="lt_win" min="1" max="1000" value="1"><em>人</em></div></label></div>
        <label class="field" id="lt_codes_w"><span>卡密</span><textarea id="lt_codes" rows="4" placeholder="XXXX-YYYY-ZZZZ&#10;AAAA-BBBB-CCCC"></textarea><small id="lt_ncode">0 条</small></label></div>
      <div id="lt_tiers" hidden></div>
      <label class="chk"><input type="checkbox" id="lt_multi"><span>设置多个奖项（一等奖、二等奖…）</span></label>
      <div class="field" id="lt_file_w" hidden><span>奖品文件</span><div class="row"><label class="filebtn"><input type="file" id="lt_file">${icon("upload", 16)}<span id="lt_fname">选择文件</span></label></div></div>
      ${collapse("参与条件（防小号薅羊毛）", `<div class="fields">
        <label class="field"><span>必须加入的频道 / 群</span><input type="text" id="lt_req" placeholder="@yourchannel"><small>机器人要在那里（设为管理员）才能检查</small></label>
        <label class="field"><span>本群至少发言</span><div class="num"><input type="number" id="lt_msgs" min="0" value="0"><em>条</em></div></label>
        <label class="field"><span>积分至少</span><div class="num"><input type="number" id="lt_minp" min="0" value="0"><em>分</em></div></label>
        <label class="field"><span>参与消耗积分</span><div class="num"><input type="number" id="lt_cost" min="0" value="0"><em>分</em></div></label>
        <label class="field"><span>至少邀请</span><div class="num"><input type="number" id="lt_inv" min="0" value="0"><em>人</em></div><small>需要开启邀请统计</small></label></div>
        <div class="toggles" style="margin-top:12px"><label class="tg"><span class="t"><b>只限老成员</b><small>抽奖发起后才进群的人不能参与</small></span><input type="checkbox" id="lt_old"><span class="sw"></span></label></div>`)}
      ${collapse("更多选项", `<div class="fields">${topicSelect("lt_th", 0, topics) || ""}
        <label class="field"><span>周期抽奖</span><select id="lt_rep">${opt([["", "只抽这一次"], ["daily", "每天自动再发一期"], ["weekly", "每周自动再发一期"]], "")}</select><small>卡密用上一期剩下的，用完自动停止</small></label></div>
        <div class="toggles" style="margin-top:12px"><label class="tg"><span class="t"><b>置顶抽奖消息</b><small>开奖后改为置顶结果</small></span><input type="checkbox" id="lt_pin"><span class="sw"></span></label>
          <label class="tg"><span class="t"><b>群里直接参与</b><small>不跳私聊，点一下就参与；中奖后需要私聊机器人领奖</small></span><input type="checkbox" id="lt_ing"><span class="sw"></span></label></div>`)}
    </div>`,
    onOpen: (w) => {
      mdEditor($("#lt_desc", w));
      const sync = () => {
        const file = $('input[name="lt_type"]:checked', w).value === "file", multi = $("#lt_multi", w).checked;
        $("#lt_codes_w", w).hidden = file; $("#lt_file_w", w).hidden = !file;
        $("#lt_single", w).hidden = multi; $("#lt_tiers", w).hidden = !multi;
        $$(".tc-w", w).forEach((x) => (x.hidden = file));
        if (multi && !$("#lt_tiers [data-tier]", w)) $("#lt_tiers", w).innerHTML = tierRow(0) + tierRow(1) + `<button type="button" class="sm" id="lt_addtier">＋ 再加一个奖项</button>`;
      };
      $$('input[name="lt_type"]', w).forEach((r) => (r.onchange = sync));
      $("#lt_multi", w).onchange = sync;
      $("#lt_tiers", w).onclick = (e) => {
        if (e.target.id === "lt_addtier") { e.target.insertAdjacentHTML("beforebegin", tierRow($$("[data-tier]", w).length)); sync(); }
        if (e.target.closest("[data-rmtier]")) e.target.closest("[data-tier]").remove();
      };
      $("#lt_codes", w).oninput = () => { $("#lt_ncode", w).textContent = `${$("#lt_codes", w).value.split("\n").filter((x) => x.trim()).length} 条`; };
      $("#lt_file", w).onchange = () => { const f = $("#lt_file", w).files[0]; $("#lt_fname", w).textContent = f ? `${f.name}（${fmtSize(f.size)}）` : "选择文件"; };
    },
    onOk: async (w) => {
      const type = $('input[name="lt_type"]:checked', w).value;
      const at = $("#lt_at", w).value, atDate = at ? new Date(at) : null;
      if (atDate && isNaN(atDate)) throw new Error("开奖时间格式不对");
      const body = { title: $("#lt_title", w).value, desc: $("#lt_desc", w).value, prize_type: type, winners: Number($("#lt_win", w).value),
        draw_at: atDate ? atDate.toISOString() : "", draw_count: Number($("#lt_cnt", w).value) || 0,
        require_chat: $("#lt_req", w).value.trim(), min_msgs: Number($("#lt_msgs", w).value) || 0, min_points: Number($("#lt_minp", w).value) || 0,
        cost_points: Number($("#lt_cost", w).value) || 0, min_invites: Number($("#lt_inv", w).value) || 0, old_only: $("#lt_old", w).checked,
        thread_id: Number(($("#lt_th", w) || {}).value || 0), repeat: $("#lt_rep", w).value, pin_msg: $("#lt_pin", w).checked, join_in_group: $("#lt_ing", w).checked };
      if ($("#lt_multi", w).checked) {
        const ts = $$("[data-tier]", w);
        if (!ts.length) throw new Error("至少一个奖项");
        body.tiers = ts.map((t) => ({ name: $(".tn", t).value.trim(), winners: Number($(".tw", t).value) || 1 }));
        body.tier_codes = ts.map((t) => $(".tc", t).value);
        body.winners = body.tiers.reduce((a, t) => a + t.winners, 0);
        if (type === "code") body.codes = "x"; // 单奖项卡密不用，占位
      } else if (type === "code") body.codes = $("#lt_codes", w).value;
      if (type === "file") {
        const f = $("#lt_file", w).files[0];
        if (!f) throw new Error("请选择奖品文件");
        if (f.size > 10 << 20) throw new Error("文件不能超过 10MB");
        body.file_name = f.name; body.file_data = await readFileData(f);
      }
      await oai("POST", `/groups/${groupID}/lotteries`, body);
      toast("抽奖已发到群里");
      route();
    } });
}

async function lotteryDetail(groupID, id) {
  const r = await oai("GET", `/groups/${groupID}/lotteries/${id}`);
  const l = r.lottery, es = r.entries || [], won = es.filter((e) => e.won);
  const un = r.unused || [], ud = r.undelivered || [], all = [...un, ...ud];
  const row = (e) => `<tr><td>${esc(e.name || e.user_id)}${e.username ? ` <span class="muted">@${esc(e.username)}</span>` : ""}<div class="muted small">${e.user_id}</div></td><td>${e.won ? pill("acc", esc(e.tier || "中奖")) : ""}</td><td>${e.won && l.prize_type === "code" ? `<code>${esc(e.code)}</code>` : ""}</td><td>${e.won ? (e.delivered ? pill("ok", "已送达") : pill("bad", esc(e.deliver_err || "未送达"))) : `<span class="muted small">${fmtTime(e.created_at)}</span>`}</td></tr>`;
  await sheet({ title: `#${l.id} ${esc(l.title)}`, sub: `${es.length} 人参与${l.status === "drawn" ? `，${won.length} 人中奖` : ""}`, wide: true, footer: false,
    body: `${l.seed ? `<div class="notice info small">${icon("shield")}<div>公平性：种子 <code>${esc(l.seed)}</code>（发起时公布的哈希 <code>${esc((l.seed_hash || "").slice(0, 16))}…</code>）。每人按 SHA-256(种子:用户ID) 从小到大排序依次中奖。</div></div>` : ""}
      ${l.prize_type === "code" ? `<div class="row" style="margin-bottom:10px"><b>未发出的卡密 ${all.length} 条</b><span class="muted">未分配 ${un.length}，中奖未送达 ${ud.length}</span>${all.length ? `<button class="sm" id="lt_cp">一键复制</button><button class="sm" id="lt_dl">下载 txt</button>` : ""}</div>${all.length ? `<textarea id="lt_left" rows="${Math.min(6, all.length)}" readonly class="mono">${esc(all.join("\n"))}</textarea>` : ""}` : ""}
      <div class="tbl-wrap" style="margin-top:12px"><table class="tbl"><tr><th>成员</th><th>结果</th><th>卡密</th><th>状态</th></tr>${[...won, ...es.filter((e) => !e.won)].map(row).join("") || empty("users", "还没有人参与", 4)}</table></div>`,
    onOpen: (w) => {
      const left = $("#lt_left", w);
      if (!left) return;
      $("#lt_cp", w).onclick = async () => { left.select(); try { await navigator.clipboard.writeText(left.value); } catch { document.execCommand("copy"); } toast("已复制"); };
      $("#lt_dl", w).onclick = () => { const a = document.createElement("a"); a.href = URL.createObjectURL(new Blob([left.value + "\n"], { type: "text/plain" })); a.download = `抽奖${l.id}-未发卡密.txt`; document.body.appendChild(a); a.click(); a.remove(); };
    } });
}

// ---------- 工具 ----------
PAGES.gTools = async (seq) => {
  const groupID = state.gid;
  const [g, tools, topics] = await Promise.all([oai("GET", "/groups/" + groupID), oai("GET", `/groups/${groupID}/tools`).catch(() => ({})), loadTopics(groupID)]);
  if (!alive(seq)) return;
  const others = state.groups.filter((x) => String(x.id) !== String(groupID) && !x.bot_left);
  view.innerHTML = `<div class="tool-grid">
    <div class="card"><div class="card-h"><div><h2>清理已注销账号</h2><p>逐个检查机器人见过的成员，踢出「Deleted Account」</p></div></div>
      <p class="muted small">${tools.zombie_running ? "正在清理…" : esc(tools.zombie_last || "还没运行过")}</p><button id="tz" ${tools.zombie_running ? "disabled" : ""}>开始清理</button></div>
    <div class="card" id="linkcard"><div class="card-h"><div><h2>邀请链接</h2><p>定期换掉主邀请链接，防止被搬运到广告群</p></div></div>
      <div class="fields">${selField("link_rotate", "自动更换", [[0, "不自动换"], [24, "每天"], [72, "每 3 天"], [168, "每周"]], g.link_rotate)}</div>
      <div class="row" style="margin-top:12px"><button id="trl">立即更换</button><span class="muted small" id="trl_res"></span></div></div>
  </div>
  ${card("U 价查询", "群里 @机器人 发「100u」「500r」或「u价」，按币安 C2C 实时价格换算，回复里带最高价、最低价和数据来源", `<div class="toggles one">${toggle(g, ["usdt_on", "开启 U 价查询", "只回复 @机器人 的消息，不影响正常聊天"])}</div>`)}
  <div class="card" id="topiccard"><div class="card-h"><div><h2>话题群</h2><p>${topics.length ? `机器人见过 ${topics.length} 个话题` : "不是话题群，或者机器人还没见过任何话题（话题里有人说话后会出现）"}</p></div></div>
    <div class="fields">${topicSelect("wth", g.welcome_thread, topics, { k: "welcome_thread", label: "欢迎语发到" })}${topicSelect("bth", g.bot_thread, topics, { k: "bot_thread", label: "机器人提示（警告、处罚通知）发到", none: "跟随原消息所在话题" })}</div></div>
  <div class="card"><div class="card-h"><div><h2>复制到其它群</h2><p>把本群设置一键复制过去，对方群的对应设置会被覆盖</p></div></div>
    ${others.length ? `<div class="checks">${others.map((x) => `<label class="chk"><input type="checkbox" class="cpg" value="${x.id}"><span>${esc(x.title || x.id)}</span></label>`).join("")}</div>
    <div class="row" style="margin-top:12px"><label class="chk"><input type="checkbox" id="cpf" checked><span>同时复制屏蔽词</span></label><label class="chk"><input type="checkbox" id="cpt"><span>同时复制欢迎语和群规</span></label><span class="grow"></span><button class="pri" id="cpgo">复制到选中的群</button></div>` : `<p class="muted">机器人只在这一个群里。</p>`}</div>
  <div class="card"><div class="card-h"><div><h2>导出 / 导入</h2><p>导出全部设置和屏蔽词，可以导入到任意群。数据库每天 04:00 自动备份到 data/backup（保留 7 份）</p></div></div>
    <div class="row"><button id="exp">导出设置</button><label class="btn" for="impf">导入设置</label><input type="file" id="impf" accept=".json,application/json" hidden></div></div>
  ${g.bot_left ? card("删除这个群", "机器人已经不在这个群里。删除后设置、屏蔽词、笔记、定时消息和广告都会清掉；处罚记录和日志保留", `<button class="danger" id="gdel">删除「${esc(g.title || g.id)}」</button>`) : ""}`;
  bindForm(view, g, (diff) => oai("PATCH", "/groups/" + groupID, diff));
  on("#tz", async () => { await oai("POST", `/groups/${groupID}/zombies`, {}); toast("已开始，人多时需要几分钟"); setTimeout(() => viewKey() === "home/tools" && route(), 5000); });
  on("#trl", async () => { if (!(await ask("立即更换主邀请链接？", "旧的主邀请链接会失效（成员专属链接不受影响）。", { yes: "更换" }))) return; const r = await oai("POST", `/groups/${groupID}/rotate-link`, {}); $("#trl_res").textContent = "新链接：" + r.link; });
  on("#cpgo", async () => {
    const targets = $$(".cpg").filter((x) => x.checked).map((x) => Number(x.value));
    if (!targets.length) throw new Error("先勾选要复制到的群");
    if (!(await ask(`复制到 ${targets.length} 个群？`, "这些群的对应设置会被覆盖成本群的设置。", { yes: "复制" }))) return;
    const r = await oai("POST", `/groups/${groupID}/copy`, { targets, filters: $("#cpf").checked, text: $("#cpt").checked });
    toast(`已复制到 ${r.groups} 个群${r.filters ? `，新增屏蔽词 ${r.filters} 条` : ""}`);
  });
  on("#exp", async () => {
    const data = await oai("GET", `/groups/${groupID}/export`);
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: "application/json" }));
    a.download = `tg-guard-${String(g.title || groupID).replace(/[\\/:*?"<>|\s]+/g, "_")}.json`;
    document.body.appendChild(a); a.click(); a.remove();
  });
  const impf = $("#impf");
  if (impf) impf.onchange = act(null, async () => {
    const f = impf.files[0]; if (!f) return;
    let data; try { data = JSON.parse(await f.text()); } catch { throw new Error("文件不是有效的 JSON"); }
    impf.value = "";
    if (!(await ask("导入设置？", `本群设置会被 ${f.name} 覆盖，屏蔽词只增不删。`, { yes: "导入" }))) return;
    const r = await oai("POST", `/groups/${groupID}/import`, data);
    toast(`已导入 ${r.settings} 项设置，新增屏蔽词 ${r.filters} 条`); route();
  });
  on("#gdel", async () => { if (!(await ask("删除这个群？", "设置和群内容会被清除，不能恢复。", { danger: true, yes: "删除" }))) return; await oai("DELETE", "/groups/" + groupID); state.gid = ""; toast("已删除"); route(); });
};

// ---------- 白名单 ----------
PAGES.members = async (seq) => {
  const groupID = state.gid;
  const [g, ap, ws] = await Promise.all([oai("GET", "/groups/" + groupID), oai("GET", `/groups/${groupID}/approved`), oai("GET", `/groups/${groupID}/warns`)]);
  if (!alive(seq)) return;
  const wl = (g.bot_admins || "").split(/\s+/).filter(Boolean);
  view.innerHTML = `<div class="cols">
    <div class="card"><div class="card-h"><div><h2>管理员白名单 <span class="pill">${wl.length}</span></h2><p>不是群管理员也能用管理命令、处理举报；不受反垃圾限制</p></div></div>
      <div class="row">${userPicker("wlu")}<button id="wladd">添加</button></div>
      <div class="chips" id="wlchips">${wl.map((id) => `<span class="chip" data-id="${esc(id)}">${esc(id)} <button data-rm>✕</button></span>`).join("")}</div>
      <div class="row" style="margin-top:12px"><button class="pri" id="wls">保存</button><span class="muted small">谁能在 Telegram 私聊里管理，在「私聊管理权限」里设置</span></div></div>
    <div class="card"><div class="card-h"><div><h2>免检白名单 <span class="pill">${ap.length}</span></h2><p>只是不受反垃圾限制，没有管理权限</p></div></div>
      <div class="row">${userPicker("au")}<button class="pri" id="aa">加入</button></div>
      <div class="tbl-wrap" style="margin-top:14px"><table class="tbl"><tr><th>用户</th><th></th></tr>
      ${ap.map((a) => `<tr><td>${userCell(a.user_id, a.name)}</td><td class="act"><button class="sm" data-da="${a.user_id}">移除</button></td></tr>`).join("") || empty("inbox", "暂无", 2)}</table></div></div>
  </div>
  <div class="cols">
    <div class="card"><div class="card-h"><div><h2>解封 / 解除禁言</h2><p>只在当前群生效</p></div></div>
      <div class="row">${userPicker("ub")}<button id="uba">解封</button></div></div>
    <div class="card"><div class="card-h"><div><h2>警告记录</h2></div></div>
      <div class="tbl-wrap"><table class="tbl"><tr><th>用户 ID</th><th class="r">警告</th><th class="r">消息</th><th>入群</th></tr>
      ${ws.map((m) => `<tr><td><code>${m.user_id}</code></td><td class="r">${pill(m.warns >= 2 ? "warn" : "", m.warns)}</td><td class="r">${m.msg_count}</td><td class="muted">${fmtTime(m.joined_at)}</td></tr>`).join("") || empty("inbox", "没有人被警告", 4)}</table></div></div>
  </div>`;
  bindPickers(view);
  $("#wlchips").onclick = (e) => { const b = e.target.closest("[data-rm]"); if (b) b.parentElement.remove(); };
  on("#wladd", async () => { const id = pickedUID("#wlu"); if (!$(`#wlchips [data-id="${id}"]`)) $("#wlchips").insertAdjacentHTML("beforeend", `<span class="chip" data-id="${id}">${id} <button data-rm>✕</button></span>`); $("#wlu").value = ""; });
  on("#wls", async () => { await oai("PATCH", "/groups/" + groupID, { bot_admins: $$("#wlchips .chip").map((c) => c.dataset.id).join(" ") }); toast("已保存"); route(); });
  on("#aa", async () => { await oai("POST", `/groups/${groupID}/approved`, { user_id: pickedUID("#au") }); toast("已加入白名单"); route(); });
  on("#uba", async () => { await oai("POST", `/groups/${groupID}/unban`, { user_id: pickedUID("#ub") }); toast("已解封"); });
  $$("[data-da]").forEach((b) => (b.onclick = act(b, async () => { await oai("DELETE", `/groups/${groupID}/approved/${b.dataset.da}`); route(); })));
};

// ---------- 启动 ----------
(async () => {
  try {
    const b = await bridge();
    const ctx = (b.getContext && b.getContext()) || {};
    if (ctx.isDark !== undefined) state.ctxDark = !!ctx.isDark;
    applyTheme(store.get("tgguard.theme"));
    if (b.onContext) b.onContext((c) => { if (c && c.isDark !== undefined) { state.ctxDark = !!c.isDark; applyTheme(store.get("tgguard.theme")); } });
    state.targets = unwrap(await b.apiGet("page/targets", {})) || state.targets;
  } catch (e) {
    view.innerHTML = `<div class="card"><div class="empty"><span class="e">${icon("plug", 34)}</span><b>无法连接插件</b><br><span class="muted">${esc(e.message)}</span></div></div>`;
    return;
  }
  const conn = [state.targets.oai && "群管已连接", state.targets.jack && "中继已连接"].filter(Boolean).join(" · ");
  $("#sidefoot").textContent = conn || "未配置";
  $("#sidefoot").classList.toggle("off", !conn);
  if (state.targets.oai) oai("GET", "/status").then((o) => { state.info = o; $("#botname").textContent = "@" + (o.bot_username || ""); renderNav(); }).catch(() => {});
  // ?gid= 只用来打开时定位到某个群（Mini App 按钮带的），记下后从地址里去掉，之后切群以面板为准
  const qg = new URLSearchParams(location.search).get("gid");
  if (qg) { store.set("tgguard.gid", qg); try { history.replaceState(null, "", location.pathname + location.hash); } catch { /* sandbox */ } }
  route();
})();
})();
