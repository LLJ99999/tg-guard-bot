package webapp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// 仓库里同时有 AstrBot 面板时，确认 Mini App 用的是同一份前端（忘了跑 scripts/sync-webapp.sh 会失败）
func TestSyncedWithDashboard(t *testing.T) {
	dash := filepath.Join("..", "..", "..", "..", "astrbot_plugin_tg_guard", "pages", "dashboard")
	if _, err := os.Stat(dash); err != nil {
		t.Skip("没有 AstrBot 面板源码")
	}
	for _, f := range []string{"app.js", "styles.css"} {
		a, _ := os.ReadFile(filepath.Join(dash, f))
		b, err := Files.ReadFile("static/" + f)
		if err != nil || !bytes.Equal(a, b) {
			t.Fatalf("%s 和 AstrBot 面板不一致，请运行 scripts/sync-webapp.sh", f)
		}
	}
}
