# TG Guard · Telegram Group Guard Bot

![version](https://img.shields.io/badge/version-v1.17.0-blue) ![go](https://img.shields.io/badge/Go-1.26+-00ADD8) ![license](https://img.shields.io/badge/license-MIT-green)

[简体中文](README.md) | English

A Telegram group management bot written in Go, in the spirit of Rose / GroupHelp / nmbot: join captcha, anti-spam, AI moderation, self-learning, giveaways, points, notes, scheduled ads, plus a built-in **Telegram Mini App admin panel**. A single binary with SQLite that runs in one Docker container.

The repo also ships two optional AstrBot plugins: a "TG Guard Panel" that brings every setting into the AstrBot dashboard, and "Call the Owner", which lets group members vote to phone the owner.

> The repo contains no tokens, secrets, databases, or production config.

> Note: the bot's UI and messages are currently in Chinese. English UI is on the roadmap.

---

## Repository layout

```
.
├── tg-guard/oai_bot/            # The bot itself (Go)
│   ├── cmd/oai_bot/             # Entry point
│   ├── internal/
│   │   ├── guard/               # Captcha, anti-spam, commands, panels (core logic)
│   │   ├── admin/               # Internal admin API (for the panel / plugins)
│   │   ├── webapp/              # Telegram Mini App admin panel
│   │   ├── web/                 # Web verification page
│   │   ├── store/               # SQLite storage
│   │   └── captcha/ config/ queue/ telegram/ ...
│   ├── config.example.yaml      # Config template
│   └── Dockerfile
├── astrbot_plugin_tg_guard/     # AstrBot "TG Guard Panel" plugin
├── astrbot_plugin_call_owner/   # AstrBot "Call the Owner" plugin
├── scripts/sync-webapp.sh       # Sync the panel front end into the Mini App
├── docs/ROADMAP.md              # Feature inventory and roadmap
├── docker-compose.example.yaml
└── CHANGELOG.md
```

## Features

**Join verification**
- Auto-approve / decline join requests (chat_join_request), or mute new members until they pass verification
- Methods: Mini App web captcha (Cloudflare Turnstile or self-hosted [Cap](https://capjs.js.org)), built-in animated GIF captcha, math / button challenges
- Persistent timeouts and failure counts; CAS shared blacklist; risky-name detection; text-only for the first N hours

**Anti-spam**
- Blocked words / regex, link / forward / media locks, flood control, duplicate-message wave detection, anti-raid
- AI moderation: any OpenAI-compatible API, for a newcomer's first N messages or all messages, including image-ad detection; NFKC normalization against fancy-font evasion
- Self-learning: admin `/spam`, `/notspam` and undone punishments become samples; similar messages match directly and samples are fed to the AI as few-shot examples
- Profile duplicate scan: periodically scans member bios / channels for shared links and reports to the log group only, no automatic punishment

**Moderation**
- Warn, mute, kick, ban, global ban (`/gban`), federation bans, `/votekick`, auto-mute when reports reach a threshold
- Admin and join whitelists; choose who can manage which group from inside Telegram
- One-tap undo for punishments, log channel, `/zombies` to remove deleted accounts, daily automatic database backups

**Content and engagement**
- Notes: tapping the index or sending `#name` in a group delivers the note privately to that user, with an access log (v1.17.0)
- Auto-replies (exact / prefix / regex, random pick, auto-delete), welcome messages (photo / GIF / video / buttons, auto-delete)
- Scheduled messages, scheduled ads (Markdown, images, buttons, pinning, private preview before sending), night-mode group mute
- Giveaways: multiple prizes, recurring draws, points / invite thresholds, publicly verifiable seed
- Check-in points, message points, activity leaderboards, invite tracking (personal links, counted only after verification, invite leaderboard)
- Forum topics supported

**Admin panels**
- Telegram Mini App: admins send `/settings` in private chat to open it; four sections (Activity / Rules / Members / Content), works on phone and desktop, dark mode follows the system
- Inline-button settings panel in private chat
- Optional AstrBot dashboard panel with the same features as the Mini App

## Quick start

### 1. Create the bot
Create a bot with [@BotFather](https://t.me/BotFather), copy the token, and turn off Group Privacy.

### 2. Configure
```bash
cp tg-guard/oai_bot/config.example.yaml config.yaml
```
At minimum, set:

| Key | Description |
| --- | --- |
| `bot.token` | Token from BotFather (do not share it with another bot) |
| `bot.owner_ids` | Your numeric Telegram ID; can use admin commands in any group and `/gban` |
| `bot.allowed_groups` | Only these groups may use the bot; empty means no limit |
| `bot.log_chat_id` | Group / channel for action logs; 0 disables logging |

Secrets can also be overridden with environment variables such as `OAI_BOT_TOKEN` and `OAI_AI_API_KEY`.

### 3. Run
```bash
docker compose -f docker-compose.example.yaml up -d --build
```
Or build the image yourself:
```bash
docker build --build-arg VERSION=v1.17.0 -t oai-guard:v1.17.0 tg-guard/oai_bot
```
Without Docker (Go 1.26+):
```bash
cd tg-guard/oai_bot
go build -o oai_bot ./cmd/oai_bot
./oai_bot -c ../../config.yaml
```

### 4. Add it to your group
Add the bot to your group as an admin (delete messages, ban users, invite users, pin), then send `/start` or `/settings` to it in private chat.

## Optional features

### Mini App admin panel / web verification
1. Reverse-proxy the container's port `8080` over HTTPS with Nginx / Caddy, set it as `web.public_url`, and restart the bot.
2. Admins send `/settings` to the bot in private chat and tap "📱 打开完整管理面板" (open full admin panel); afterwards the menu button in the private chat opens it directly.
3. Choose who can use it per group under "成员与权限 › 私聊管理权限" (Members & permissions › Private-chat admin access). Global settings are visible only to `bot.owner_ids`.
4. For web verification, also set the Turnstile or Cap site key / secret under `captcha`; without them the built-in GIF captcha is used.

No extra domain setup in BotFather is needed. After editing the front end in `astrbot_plugin_tg_guard/pages/dashboard`, run `scripts/sync-webapp.sh` once to sync it into the Mini App.

### AI moderation
Fill in `base_url`, `api_key`, and `model` of any OpenAI-compatible API under `ai` (a cheap small model is enough). `threshold` is the confidence needed to ban. You can also change these later in the panel.

### AstrBot panel plugin `astrbot_plugin_tg_guard`
1. In `config.yaml`, set `admin.listen: ":8090"` and an `admin.token` of 16+ characters.
2. Put AstrBot and the bot on the same Docker network (never expose `8090` publicly).
3. Copy the plugin into AstrBot's `data/plugins/` and set `oai_url` (e.g. `http://oai-bot:8090`) and the same token in the plugin config.

Requires AstrBot ≥ 4.26.0.

### Call-the-owner plugin `astrbot_plugin_call_owner`
When enough members tap the button or send `/call` within a time window (default: 5 people in 10 minutes), it phones the owner through [Miao Tixing](https://miaotixing.com). Supports cooldowns, allowed groups, a blacklist, scheduled button messages, call history, and a settings panel. The owner can send `/awake` to say they're up.

In the plugin config, set the Miao Tixing "voice call" reminder code (starts with t) and the owner's Telegram ID.

## Common commands

| Command | Description |
| --- | --- |
| `/start` `/settings` (private) | Open the settings panel / Mini App |
| `/warn` `/unwarn` `/mute` `/unmute` `/kick` `/ban` `/unban` | Use as a reply |
| `/spam` `/notspam` | Delete-ban and learn / mark as normal |
| `/gban` `/ungban` | Global ban (owner only) |
| `/filter` `/filters` `/lock` `/flood` | Blocked words, locks, flood settings |
| `/purge` `/del` `/pin` | Bulk delete, delete, pin |
| `/votekick` | Vote to kick |
| `/checkin` `/top` `/me` | Check in, leaderboard, my points |
| `/invites` `/myinvites` | Invite leaderboard, my invite link |
| `/notes` `/get name` | Note index, get a note (sent privately) |
| `/rules` `/report` `/id` `/help` | Available to everyone |

See the private-chat panel and `/help` for the full list.

## Notes
- The Bot API cannot list all members, so the member list and profile scan only cover users the bot has seen.
- Turn on `bot.observe_only: true` for a few days first to check the false-positive rate before enforcing.
- The admin API is protected only by its token; listen on an internal network only.

## Changelog and roadmap
- Per-version changes: [CHANGELOG.md](CHANGELOG.md) (Chinese)
- Feature inventory and plans: [docs/ROADMAP.md](docs/ROADMAP.md) (Chinese)

## Contributing
Fork the repo and open a Pull Request. When filing an issue, include the version and steps to reproduce, and **never post your token or config file**.

## Contributors
- [LLJ99999](https://github.com/LLJ99999) · Author
- [Claude](https://claude.ai) (Anthropic) · Development and documentation

## License
[MIT](LICENSE) © 2026 LLJ99999
