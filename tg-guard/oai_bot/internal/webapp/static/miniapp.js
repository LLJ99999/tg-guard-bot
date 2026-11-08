// Telegram Mini App 外壳：把面板的 AstrBot 桥接换成直接请求机器人接口（用 Telegram initData 鉴权）
(function () {
  const tg = window.Telegram && window.Telegram.WebApp;
  if (tg) {
    tg.ready();
    tg.expand();
    try { tg.setHeaderColor("secondary_bg_color"); } catch (e) { /* 旧客户端不支持 */ }
  }
  const initData = (tg && tg.initData) || "";
  async function req(method, path, body, query) {
    let url = "api" + String(path).replace(/^\/api/, "");
    if (query) {
      const q = new URLSearchParams();
      Object.entries(query).forEach(([k, v]) => { if (v !== undefined && v !== null && v !== "") q.set(k, v); });
      const s = q.toString();
      if (s) url += "?" + s;
    }
    const r = await fetch(url, {
      method,
      headers: { "Content-Type": "application/json", "X-Telegram-Init-Data": initData },
      body: body !== undefined && body !== null && method !== "GET" ? JSON.stringify(body) : undefined,
    });
    let data = null;
    try { data = await r.json(); } catch (e) { /* 空响应 */ }
    if (!r.ok) throw new Error((data && data.error) || "请求失败（" + r.status + "）");
    return data;
  }
  const dark = () => (tg && tg.colorScheme ? tg.colorScheme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches);
  window.AstrBotPluginView = {
    ready: async () => { if (!initData) throw new Error("请从 Telegram 里机器人私聊的「管理面板」按钮打开"); },
    getContext: () => ({ isDark: dark() }),
    onContext: (fn) => { if (tg) tg.onEvent("themeChanged", () => fn({ isDark: dark() })); },
    apiGet: async (p) => {
      if (p !== "page/targets") return {};
      const s = await req("GET", "/api/status").catch(() => ({}));
      return { oai: true, jack: false, owner: !!s.owner };
    },
    apiPost: async (p, b) => req(b.method, b.path, b.body, b.query),
  };
  // t.me 链接（「从 Telegram 获取」）在 Telegram 里打开，不跳外部浏览器
  document.addEventListener("click", (e) => {
    const a = e.target.closest && e.target.closest('a[href^="https://t.me/"]');
    if (a && tg && tg.openTelegramLink) { e.preventDefault(); tg.openTelegramLink(a.href); }
  });
  document.documentElement.classList.add("tma");
})();
