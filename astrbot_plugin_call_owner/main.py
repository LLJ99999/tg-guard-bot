"""群友呼叫站长：N 人在窗口期内点按钮 / 发 /call，就通过喵提醒给站长打电话。

不走 AstrBot 消息管线（别的插件可能 stop_event），直接往 Telegram 适配器的
python-telegram-bot Application 上挂 CommandHandler / CallbackQueryHandler。
"""
from __future__ import annotations

import asyncio
import json
import os
import time
import uuid
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo

import aiohttp
from astrbot.api import logger
from astrbot.api.star import Context, Star

try:
    from astrbot.api.web import request as _web_request
except Exception:  # pragma: no cover
    _web_request = None

try:
    from telegram import InlineKeyboardButton, InlineKeyboardMarkup
    from telegram.ext import CallbackQueryHandler, CommandHandler
except Exception:  # pragma: no cover
    InlineKeyboardButton = InlineKeyboardMarkup = CallbackQueryHandler = CommandHandler = None

PLUGIN = "astrbot_plugin_call_owner"
PREFIX = f"/{PLUGIN}/page"
CB_DATA = "callowner:vote"
TZ = ZoneInfo("Asia/Shanghai")
MIAO_URL = "https://miaotixing.com/trigger"
MAX_CALLS = 500

RULE_KEYS = {
    "miao_id": str, "threshold": int, "window_min": int, "cooldown_min": int, "fail_cooldown_min": int,
    "group_ids": str, "owner_ids": str, "blacklist_ids": str, "active_hours": str,
    "voice_template": str, "schedule_enabled": bool, "schedule_times": str, "schedule_text": str,
    "button_text": str, "platform_id": str,
}
DEFAULTS = {
    "miao_id": "", "threshold": 5, "window_min": 10, "cooldown_min": 30, "fail_cooldown_min": 2,
    "group_ids": "", "owner_ids": "", "blacklist_ids": "", "active_hours": "",
    "voice_template": "{group}有{count}位群友呼叫站长，请尽快上线处理。",
    "schedule_enabled": False, "schedule_times": "23:30",
    "schedule_text": "🌙 站长可能已经睡了。\n网站出问题需要叫醒站长，就点下面的按钮：{window}分钟内满{threshold}人点，会直接给站长打电话。\n请勿恶作剧，每次通话都有记录。",
    "button_text": "📞 叫醒站长", "platform_id": "",
}


def _ok(data):
    return {"status": "ok", "data": data}


def _err(msg):
    return {"status": "error", "message": msg, "data": {"error": msg}}


def _ids(s) -> set[str]:
    return {x.strip() for x in str(s or "").replace("，", ",").replace("\n", ",").split(",") if x.strip()}


def _fmt(ts) -> str:
    return datetime.fromtimestamp(ts, TZ).strftime("%Y-%m-%d %H:%M:%S") if ts else ""


class CallOwner(Star):
    def __init__(self, context: Context, config: dict | None = None):
        super().__init__(context)
        self.config = config if config is not None else {}
        self.lock = asyncio.Lock()
        self.data_dir = self._data_dir()
        self.db_path = self.data_dir / "records.json"
        self.db = self._load()
        self._attached_app = None
        self._tasks: list[asyncio.Task] = []
        self._http: aiohttp.ClientSession | None = None
        if hasattr(context, "register_web_api"):
            for path, fn, m, d in [
                ("state", self.api_state, ["GET"], "状态"),
                ("save", self.api_save, ["POST"], "保存规则"),
                ("test", self.api_test, ["POST"], "测试拨号"),
                ("reset", self.api_reset, ["POST"], "清零"),
                ("send", self.api_send, ["POST"], "立即发送按钮消息"),
                ("delete_call", self.api_delete_call, ["POST"], "删除记录"),
            ]:
                context.register_web_api(f"{PREFIX}/{path}", fn, m, f"呼叫站长：{d}")
        loop = asyncio.get_event_loop()
        self._tasks.append(loop.create_task(self._attach_loop()))
        self._tasks.append(loop.create_task(self._schedule_loop()))

    # ---------------- 配置 / 存储 ----------------
    def c(self, k):
        v = self.config.get(k, DEFAULTS[k]) if hasattr(self.config, "get") else DEFAULTS[k]
        return DEFAULTS[k] if v is None else v

    def ci(self, k) -> int:
        try:
            return max(0, int(self.c(k)))
        except Exception:
            return int(DEFAULTS[k])

    def _data_dir(self) -> Path:
        try:
            from astrbot.api.star import StarTools
            p = Path(StarTools.get_data_dir(PLUGIN))
        except Exception:
            p = Path("data/plugin_data") / PLUGIN
        p.mkdir(parents=True, exist_ok=True)
        return p

    def _load(self) -> dict:
        try:
            d = json.loads(self.db_path.read_text("utf-8"))
        except Exception:
            d = {}
        d.setdefault("calls", [])
        d.setdefault("rounds", {})       # chat_id -> {"voters": {uid: {...}}, "msg_ids": []}
        d.setdefault("cooldown", {})     # chat_id -> until_ts
        d.setdefault("sched_done", [])   # "YYYY-mm-dd HH:MM"
        d.setdefault("groups", {})       # chat_id -> title
        return d

    def _save(self):
        tmp = self.db_path.with_suffix(".tmp")
        tmp.write_text(json.dumps(self.db, ensure_ascii=False), "utf-8")
        os.replace(tmp, self.db_path)

    # ---------------- Telegram 接入 ----------------
    def _adapter(self):
        pid = str(self.c("platform_id") or "").strip()
        try:
            insts = self.context.platform_manager.platform_insts
        except Exception:
            insts = []
        for p in insts:
            try:
                meta = p.meta()
            except Exception:
                continue
            if meta.name == "telegram" and (not pid or meta.id == pid):
                return p
        return None

    def _bot(self):
        a = self._adapter()
        return getattr(a, "client", None) if a else None

    async def _attach_loop(self):
        await asyncio.sleep(5)
        while True:
            try:
                a = self._adapter()
                app = getattr(a, "application", None) if a else None
                if app is not None and app is not self._attached_app and CommandHandler:
                    app.add_handler(CommandHandler("call", self.tg_call), group=-77)
                    app.add_handler(CommandHandler("callstatus", self.tg_status), group=-77)
                    app.add_handler(CommandHandler("awake", self.tg_awake), group=-77)
                    app.add_handler(CallbackQueryHandler(self.tg_button, pattern=f"^{CB_DATA}$"), group=-77)
                    self._attached_app = app
                    logger.info("[call_owner] 已挂载到 Telegram 适配器")
            except Exception as e:
                logger.warning(f"[call_owner] 挂载失败: {e!r}")
            await asyncio.sleep(20)

    def _group_ok(self, chat) -> bool:
        if chat is None or chat.type not in ("group", "supergroup"):
            return False
        allowed = _ids(self.c("group_ids"))
        return not allowed or str(chat.id) in allowed

    def _markup(self, count=None):
        if not InlineKeyboardMarkup:
            return None
        t = str(self.c("button_text") or DEFAULTS["button_text"])
        if count is not None:
            t = f"{t}（{count}/{self.ci('threshold') or 1}）"
        return InlineKeyboardMarkup([[InlineKeyboardButton(t, callback_data=CB_DATA)]])

    @staticmethod
    def _uinfo(u) -> dict:
        name = " ".join(x for x in [u.first_name, u.last_name] if x) or (u.username or str(u.id))
        return {"uid": str(u.id), "name": name, "username": u.username or ""}

    async def tg_call(self, update, context):
        chat, u, msg = update.effective_chat, update.effective_user, update.effective_message
        if not self._group_ok(chat) or not u or u.is_bot:
            return
        res = await self.vote(str(chat.id), chat.title or "", self._uinfo(u), "命令")
        try:
            await msg.reply_text(res["text"], reply_markup=self._markup(res.get("count")) if res.get("button") else None)
        except Exception as e:
            logger.warning(f"[call_owner] 回复失败: {e!r}")

    async def tg_button(self, update, context):
        q = update.callback_query
        chat, u = update.effective_chat, q.from_user
        if not self._group_ok(chat) or not u or u.is_bot:
            try:
                await q.answer("这里不能呼叫站长", show_alert=False)
            except Exception:
                pass
            return
        res = await self.vote(str(chat.id), chat.title or "", self._uinfo(u), "按钮")
        try:
            await q.answer(res["text"][:190], show_alert=res.get("alert", False))
        except Exception:
            pass
        if res.get("count") is not None:
            try:
                await q.edit_message_reply_markup(reply_markup=self._markup(res["count"]))
            except Exception:
                pass

    async def tg_status(self, update, context):
        chat, msg = update.effective_chat, update.effective_message
        if not self._group_ok(chat):
            return
        await msg.reply_text(self.status_text(str(chat.id)))

    async def tg_awake(self, update, context):
        chat, u, msg = update.effective_chat, update.effective_user, update.effective_message
        if not self._group_ok(chat) or not u:
            return
        if str(u.id) not in _ids(self.c("owner_ids")):
            await msg.reply_text("只有站长能用 /awake")
            return
        cid = str(chat.id)
        async with self.lock:
            self.db["rounds"].pop(cid, None)
            self.db["cooldown"][cid] = time.time() + self.ci("cooldown_min") * 60
            for call in reversed(self.db["calls"]):
                if call["chat_id"] == cid and call["kind"] == "auto":
                    if not call.get("awake_ts"):
                        call["awake_ts"] = time.time()
                    break
            self._save()
        await msg.reply_text(f"站长已上线 ✅ 呼叫清零，{self.ci('cooldown_min')} 分钟内不再拨打。")

    # ---------------- 核心逻辑 ----------------
    def _in_active_hours(self) -> bool:
        spec = str(self.c("active_hours") or "").strip()
        if not spec:
            return True
        now = datetime.now(TZ).strftime("%H:%M")
        for part in spec.replace("，", ",").split(","):
            if "-" not in part:
                continue
            a, b = [x.strip().zfill(5) for x in part.split("-", 1)]
            if (a <= b and a <= now < b) or (a > b and (now >= a or now < b)):
                return True
        return False

    def _prune(self, cid: str, now: float) -> dict:
        r = self.db["rounds"].setdefault(cid, {"voters": {}})
        win = self.ci("window_min") * 60
        r["voters"] = {k: v for k, v in r["voters"].items() if now - v["ts"] <= win}
        return r

    async def vote(self, cid: str, title: str, user: dict, via: str) -> dict:
        now = time.time()
        th = self.ci("threshold") or 1
        if title:
            self.db["groups"][cid] = title
        if user["uid"] in _ids(self.c("blacklist_ids")):
            return {"text": "你已被禁止呼叫站长", "alert": True}
        if not str(self.c("miao_id") or "").strip():
            return {"text": "站长还没配置电话提醒，暂时不能呼叫", "alert": True}
        if not self._in_active_hours():
            return {"text": f"现在不在可呼叫时段（{self.c('active_hours')}）", "alert": True}
        async with self.lock:
            until = float(self.db["cooldown"].get(cid, 0))
            if until > now:
                return {"text": f"刚给站长打过电话，冷却中，还剩 {int((until - now) // 60) + 1} 分钟", "alert": True}
            r = self._prune(cid, now)
            if user["uid"] in r["voters"]:
                n = len(r["voters"])
                self._save()
                return {"text": f"你已经呼叫过了，当前 {n}/{th}，还差 {th - n} 人", "count": n, "button": True}
            r["voters"][user["uid"]] = {**user, "ts": now, "via": via}
            n = len(r["voters"])
            if n < th:
                self._save()
                return {"text": f"已记录 ✅ 当前 {n}/{th}，{self.ci('window_min')} 分钟内还差 {th - n} 人就会给站长打电话",
                        "count": n, "button": True}
            voters = sorted(r["voters"].values(), key=lambda v: v["ts"])
            self.db["rounds"].pop(cid, None)
            self.db["cooldown"][cid] = now + self.ci("cooldown_min") * 60
            self._save()
        call = await self.dial(cid, title or self.db["groups"].get(cid, ""), voters, "auto")
        if call["ok"]:
            text = f"📞 已给站长打电话（{n} 人呼叫）。{self.ci('cooldown_min')} 分钟内不会重复拨打。"
        else:
            async with self.lock:
                self.db["cooldown"][cid] = time.time() + self.ci("fail_cooldown_min") * 60
                self._save()
            text = f"⚠️ 拨号失败：{call['resp'][:80]}。{self.ci('fail_cooldown_min')} 分钟后可再次呼叫。"
        asyncio.create_task(self._announce(cid, text))
        return {"text": text, "count": 0, "alert": True}

    def render_voice(self, group: str, voters: list) -> str:
        tpl = str(self.c("voice_template") or DEFAULTS["voice_template"])
        names = "、".join(v["name"] for v in voters)[:60]
        try:
            text = tpl.format(group=group or "站点群", count=len(voters), names=names,
                              time=datetime.now(TZ).strftime("%H点%M分"))
        except Exception:
            text = tpl
        return text[:200]

    async def dial(self, cid: str, group: str, voters: list, kind: str) -> dict:
        text = self.render_voice(group, voters)
        ok, resp = False, ""
        try:
            if self._http is None or self._http.closed:
                self._http = aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=15))
            params = {"id": str(self.c("miao_id")).strip(), "text": text, "type": "json"}
            async with self._http.get(MIAO_URL, params=params) as r:
                raw = await r.text()
            try:
                j = json.loads(raw)
                ok = j.get("code") == 0
                resp = str(j.get("msg") or raw)[:300]
            except Exception:
                ok = r.status == 200 and ("成功" in raw or "ok" in raw.lower())
                resp = raw[:300]
        except Exception as e:
            resp = f"请求喵提醒失败：{type(e).__name__}"
        call = {"id": uuid.uuid4().hex[:10], "ts": time.time(), "chat_id": cid, "chat_title": group,
                "kind": kind, "voters": voters, "text": text, "ok": ok, "resp": resp or ("成功" if ok else "失败"),
                "awake_ts": None}
        async with self.lock:
            self.db["calls"].append(call)
            self.db["calls"] = self.db["calls"][-MAX_CALLS:]
            self._save()
        logger.info(f"[call_owner] 拨号 kind={kind} ok={ok} chat={cid}")
        return call

    async def _announce(self, cid: str, text: str):
        bot = self._bot()
        if bot:
            try:
                await bot.send_message(chat_id=int(cid), text=text)
            except Exception as e:
                logger.warning(f"[call_owner] 群通知失败: {e!r}")

    def status_text(self, cid: str) -> str:
        now = time.time()
        until = float(self.db["cooldown"].get(cid, 0))
        if until > now:
            return f"冷却中，还剩 {int((until - now) // 60) + 1} 分钟"
        r = self._prune(cid, now)
        n, th = len(r["voters"]), self.ci("threshold") or 1
        return f"当前 {n}/{th} 人呼叫（{self.ci('window_min')} 分钟窗口），满 {th} 人给站长打电话"

    async def send_button_msg(self, cid: str) -> str:
        bot = self._bot()
        if not bot:
            raise RuntimeError("找不到 Telegram 适配器")
        tpl = str(self.c("schedule_text") or DEFAULTS["schedule_text"])
        try:
            text = tpl.format(threshold=self.ci("threshold") or 1, window=self.ci("window_min"),
                              cooldown=self.ci("cooldown_min"))
        except Exception:
            text = tpl
        n = len(self._prune(cid, time.time())["voters"])
        await bot.send_message(chat_id=int(cid), text=text, reply_markup=self._markup(n))
        return text

    async def _schedule_loop(self):
        await asyncio.sleep(15)
        while True:
            try:
                if self.c("schedule_enabled"):
                    now = datetime.now(TZ)
                    hm, key = now.strftime("%H:%M"), now.strftime("%Y-%m-%d %H:%M")
                    times = {t.strip().zfill(5) for t in str(self.c("schedule_times")).replace("，", ",").split(",") if t.strip()}
                    if hm in times and key not in self.db["sched_done"]:
                        self.db["sched_done"] = (self.db["sched_done"] + [key])[-50:]
                        self._save()
                        for cid in _ids(self.c("group_ids")):
                            try:
                                await self.send_button_msg(cid)
                            except Exception as e:
                                logger.warning(f"[call_owner] 定时消息发送失败 {cid}: {e!r}")
            except Exception as e:
                logger.warning(f"[call_owner] 定时循环异常: {e!r}")
            await asyncio.sleep(20)

    # ---------------- 面板 API ----------------
    async def _json(self) -> dict:
        try:
            if _web_request is not None:
                d = await _web_request.json(default=None)
            else:
                from quart import request as qr
                d = await qr.get_json(silent=True)
            return d if isinstance(d, dict) else {}
        except Exception:
            return {}

    def _target_groups(self) -> list[str]:
        g = list(_ids(self.c("group_ids")))
        return g or list(self.db["groups"].keys())

    async def api_state(self):
        now = time.time()
        groups = []
        bot = self._bot()
        for cid in self._target_groups():
            if bot and not self.db["groups"].get(cid):
                try:
                    chat = await asyncio.wait_for(bot.get_chat(int(cid)), 8)
                    self.db["groups"][cid] = chat.title or ""
                    self._save()
                except Exception:
                    pass
            r = self._prune(cid, now)
            groups.append({
                "chat_id": cid, "title": self.db["groups"].get(cid, ""),
                "voters": sorted(({**v, "time": _fmt(v["ts"])} for v in r["voters"].values()), key=lambda v: v["ts"]),
                "cooldown_left": max(0, int(float(self.db["cooldown"].get(cid, 0)) - now)),
            })
        calls = []
        for c in reversed(self.db["calls"]):
            calls.append({**c, "time": _fmt(c["ts"]), "awake_time": _fmt(c.get("awake_ts")),
                          "awake_after_min": round((c["awake_ts"] - c["ts"]) / 60, 1) if c.get("awake_ts") else None,
                          "voters": [{**v, "time": _fmt(v.get("ts"))} for v in c.get("voters", [])]})
        rules = {k: self.c(k) for k in RULE_KEYS}
        a = self._adapter()
        return _ok({
            "rules": rules, "groups": groups, "calls": calls,
            "known_groups": self.db["groups"], "telegram": bool(a),
            "telegram_id": a.meta().id if a else "", "hooked": self._attached_app is not None,
            "in_active_hours": self._in_active_hours(), "now": _fmt(now),
            "last_sched": self.db["sched_done"][-1] if self.db["sched_done"] else "",
        })

    async def api_save(self):
        d = await self._json()
        rules = d.get("rules") or {}
        for k, typ in RULE_KEYS.items():
            if k not in rules:
                continue
            v = rules[k]
            try:
                if typ is int:
                    v = max(0, int(v))
                elif typ is bool:
                    v = bool(v)
                else:
                    v = str(v or "")
            except Exception:
                return _err(f"{k} 格式不对")
            self.config[k] = v
        if (self.ci("threshold") or 0) < 1:
            self.config["threshold"] = 1
        try:
            self.config.save_config()
        except Exception as e:
            return _err(f"保存失败：{e}")
        return _ok({"saved": True})

    async def api_test(self):
        if not str(self.c("miao_id") or "").strip():
            return _err("先在规则里填写喵码")
        d = await self._json()
        cid = str(d.get("chat_id") or (self._target_groups() or [""])[0])
        call = await self.dial(cid, self.db["groups"].get(cid, "测试"),
                               [{"uid": "panel", "name": "面板测试", "username": "", "ts": time.time(), "via": "面板"}], "test")
        return _ok(call) if call["ok"] else _err(f"拨号失败：{call['resp']}")

    async def api_reset(self):
        d = await self._json()
        cid = str(d.get("chat_id") or "")
        async with self.lock:
            for k in ([cid] if cid else list(self.db["rounds"].keys()) + list(self.db["cooldown"].keys())):
                self.db["rounds"].pop(k, None)
                self.db["cooldown"].pop(k, None)
            self._save()
        return _ok({"reset": True})

    async def api_send(self):
        d = await self._json()
        targets = [str(d["chat_id"])] if d.get("chat_id") else list(_ids(self.c("group_ids")))
        if not targets:
            return _err("先在规则里填写群 ID")
        sent = []
        for cid in targets:
            try:
                await self.send_button_msg(cid)
                sent.append(cid)
            except Exception as e:
                return _err(f"发送到 {cid} 失败：{e}")
        return _ok({"sent": sent})

    async def api_delete_call(self):
        d = await self._json()
        cid = d.get("id")
        async with self.lock:
            self.db["calls"] = [c for c in self.db["calls"] if c["id"] != cid]
            self._save()
        return _ok({"deleted": cid})

    async def terminate(self):
        for t in self._tasks:
            t.cancel()
        app = self._attached_app
        if app is not None:
            try:
                for h in list(app.handlers.get(-77, [])):
                    app.remove_handler(h, group=-77)
            except Exception:
                pass
        if self._http and not self._http.closed:
            await self._http.close()
