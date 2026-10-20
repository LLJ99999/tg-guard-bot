"""把面板请求转发到两个 Go 机器人的内网管理接口。纯函数部分单独放这里方便测试。"""

from __future__ import annotations

import re
from typing import Any
from urllib.parse import quote

TARGETS = ("oai", "jack")
METHODS = ("GET", "POST", "PATCH", "DELETE")
# 只允许 /api/ 下的路径，段内只能是字母数字和 _-，防止面板被拿来访问任意地址
_PATH_RE = re.compile(r"^/api(/[A-Za-z0-9_\-]+)+$")


class ProxyError(ValueError):
    pass


def build_request(conf: dict, payload: dict) -> tuple[str, str, dict, Any, dict]:
    """校验面板提交的调用，返回 (method, url, headers, json_body, query)。"""
    target = payload.get("target")
    if target not in TARGETS:
        raise ProxyError("未知目标")
    base = str(conf.get(f"{target}_url") or "").rstrip("/")
    token = str(conf.get(f"{target}_token") or "")
    if not base:
        raise ProxyError(f"未配置 {target}_url")
    if len(token) < 16:
        raise ProxyError(f"未配置 {target}_token（至少 16 位）")
    method = str(payload.get("method") or "GET").upper()
    if method not in METHODS:
        raise ProxyError("不支持的方法")
    path = str(payload.get("path") or "")
    if not _PATH_RE.match(path):
        raise ProxyError("路径不合法")
    query = payload.get("query") or {}
    if not isinstance(query, dict):
        raise ProxyError("query 必须是对象")
    query = {str(k): str(v) for k, v in query.items() if v is not None and v != ""}
    body = payload.get("body")
    if method in ("GET", "DELETE"):
        body = None
    url = base + "/".join(quote(seg, safe="-_") for seg in path.split("/"))
    headers = {"Authorization": f"Bearer {token}"}
    return method, url, headers, body, query
