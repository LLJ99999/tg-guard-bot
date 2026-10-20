from __future__ import annotations

import asyncio

import aiohttp
from astrbot.api import logger
from astrbot.api.star import Context, Star

from .proxy import ProxyError, build_request

try:  # AstrBot >= 4.26 推荐写法
    from astrbot.api.web import request as _web_request
except Exception:  # 兼容旧版本：回退到 Quart
    _web_request = None

PLUGIN_NAME = "astrbot_plugin_tg_guard"
PREFIX = f"/{PLUGIN_NAME}/page"


def _ok(data):
    # bridge 收到 {"status": "ok", "data": x} 时会直接 resolve 为 x
    return {"status": "ok", "data": data}


def _err(msg: str, code: int = 0):
    # bridge 收到 status=error 时会 reject，前端 catch 到 message
    return {"status": "error", "message": msg, "data": {"error": msg, "code": code}}


async def _read_json():
    try:
        if _web_request is not None:
            return await _web_request.json(default=None)
        from quart import request as quart_request

        return await quart_request.get_json(silent=True)
    except Exception:
        return None


class TgGuardPanel(Star):
    def __init__(self, context: Context, config: dict | None = None):
        super().__init__(context)
        self.config = config or {}
        self._session: aiohttp.ClientSession | None = None
        if hasattr(context, "register_web_api"):
            context.register_web_api(f"{PREFIX}/targets", self.page_targets, ["GET"], "TG 群管面板：已配置的机器人")
            context.register_web_api(f"{PREFIX}/call", self.page_call, ["POST"], "TG 群管面板：调用机器人管理接口")
        else:
            logger.warning("[tg_guard] 当前 AstrBot 不支持插件页面 API，面板不可用（需要 AstrBot >= 4.26）")

    def _conf(self) -> dict:
        return dict(self.config or {})

    async def _http(self) -> aiohttp.ClientSession:
        if self._session is None or self._session.closed:
            t = int(self._conf().get("timeout_sec") or 15)
            self._session = aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=max(3, min(t, 60))))
        return self._session

    async def page_targets(self):
        c = self._conf()
        return _ok({
            "oai": bool(c.get("oai_url")) and len(str(c.get("oai_token") or "")) >= 16,
            "jack": bool(c.get("jack_url")) and len(str(c.get("jack_token") or "")) >= 16,
        })

    async def page_call(self):
        payload = await _read_json()
        if not isinstance(payload, dict):
            return _err("请求无效")
        try:
            method, url, headers, body, query = build_request(self._conf(), payload)
        except ProxyError as e:
            return _err(str(e))
        target = payload.get("target")
        try:
            s = await self._http()
            async with s.request(method, url, headers=headers, json=body, params=query) as r:
                try:
                    data = await r.json(content_type=None)
                except Exception:
                    data = {"error": (await r.text())[:300]}
                if r.status == 401:
                    return _err(f"{target} 的管理 token 不对，请检查插件配置", 401)
                if r.status >= 400:
                    msg = data.get("error") if isinstance(data, dict) else None
                    return _err(msg or f"HTTP {r.status}", r.status)
                return _ok(data)
        except aiohttp.ClientError as e:
            logger.warning(f"[tg_guard] {target} 请求失败: {e!r}")
            return _err(f"连不上 {target}（{url.split('/api')[0]}），请确认容器在运行、和 AstrBot 在同一 Docker 网络")
        except (TimeoutError, asyncio.TimeoutError):
            return _err(f"{target} 响应超时")

    async def terminate(self):
        if self._session and not self._session.closed:
            await self._session.close()
