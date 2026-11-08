# TG 群管机器人（v1.17.0）

- `tg-guard/oai_bot/`：豆包群管机器人 Go 源码（群抽奖、欢迎语自动删除、/report 等）
  - 构建：`docker build --build-arg VERSION=v1.17.0 -t hwl/oai-guard:v1.17.0 tg-guard/oai_bot`
  - 配置模板：`tg-guard/oai_bot/config.example.yaml`（真实 token 不在包里）
- `astrbot_plugin_tg_guard/`：AstrBot「TG 群管面板」插件（群设置、群抽奖、未发卡密导出等）
- `astrbot_plugin_call_owner/`：AstrBot 电话叫醒插件（/call 5 人投票 → 喵提醒打电话，面板设置）

不含任何密钥、数据库和线上配置。

## Telegram 里的管理面板（Mini App）
1. `config.yaml` 里填 `web.public_url`（公网 HTTPS，反代到 `web.listen`），重启机器人。
2. 管理员私聊机器人发 `/settings`，点「📱 打开完整管理面板」；之后私聊左下角菜单按钮也能直接打开。
3. 每个群「成员与权限 › 私聊管理权限」里设置谁能用。全局设置只有 `bot.owner_ids` 里的人能看到。
4. 不需要去 BotFather 额外配置域名。
