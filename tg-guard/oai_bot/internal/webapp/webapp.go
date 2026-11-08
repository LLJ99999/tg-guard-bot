// Package webapp Mini App 管理面板的静态文件。
// static/app.js、styles.css 从 astrbot_plugin_tg_guard/pages/dashboard 复制（scripts/sync-webapp.sh），两边保持一致。
package webapp

import "embed"

//go:embed static
var Files embed.FS
