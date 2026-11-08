#!/bin/sh
# 把 AstrBot 面板的前端复制到机器人里，作为 Telegram Mini App 管理面板（改了 dashboard 后运行一次）
set -e
cd "$(dirname "$0")/.."
SRC=astrbot_plugin_tg_guard/pages/dashboard
DST=tg-guard/oai_bot/internal/webapp/static
cp "$SRC/app.js" "$SRC/styles.css" "$DST/"
# index.html：多加载 Telegram 的 SDK 和 miniapp.js（把 AstrBot 桥接换成直接请求机器人）
sed -e 's#<title>TG 群管面板</title>#<title>群管理面板</title>\n    <script src="https://telegram.org/js/telegram-web-app.js"></script>#' \
    -e 's#<script src="./app.js" defer></script>#<script src="./miniapp.js"></script>\n    <script src="./app.js" defer></script>#' \
    "$SRC/index.html" > "$DST/index.html"
echo "已同步到 $DST/"
