# TG Guard · Telegram 群管机器人

![version](https://img.shields.io/badge/version-v1.17.0-blue) ![go](https://img.shields.io/badge/Go-1.26+-00ADD8) ![license](https://img.shields.io/badge/license-MIT-green)

简体中文 | [English](README.en.md)

一个用 Go 写的 Telegram 群管理机器人，对标 Rose / GroupHelp / nmbot：入群人机验证、反广告、AI 审核、自学习、抽奖、积分、笔记、定时广告，外加 Telegram 内置的 **Mini App 管理面板**。单二进制 + SQLite，一个 Docker 容器就能跑。

仓库里还带两个可选的 AstrBot 插件：把全部设置搬进 AstrBot 后台的「TG 群管面板」，以及群友投票给站长打电话的「呼叫站长」。

> 仓库不含任何 Token、密钥、数据库和线上配置。

---

## 目录结构

```
.
├── tg-guard/oai_bot/            # 机器人本体（Go）
│   ├── cmd/oai_bot/             # 入口
│   ├── internal/
│   │   ├── guard/               # 验证、反垃圾、命令、面板等核心逻辑
│   │   ├── admin/               # 内网管理接口（给面板 / 插件用）
│   │   ├── webapp/              # Telegram Mini App 管理面板
│   │   ├── web/                 # 网页验证页
│   │   ├── store/               # SQLite 存储
│   │   └── captcha/ config/ queue/ telegram/ ...
│   ├── config.example.yaml      # 配置模板
│   └── Dockerfile
├── astrbot_plugin_tg_guard/     # AstrBot「TG 群管面板」插件
├── astrbot_plugin_call_owner/   # AstrBot「呼叫站长 · 电话叫醒」插件
├── scripts/sync-webapp.sh       # 把面板前端同步成 Mini App
├── docs/ROADMAP.md              # 功能盘点与路线图
├── docker-compose.example.yaml
└── CHANGELOG.md
```

## 功能

**入群验证**
- 申请入群（chat_join_request）自动批准 / 拒绝，或进群后先禁言再验证
- 验证方式：Mini App 网页验证（Cloudflare Turnstile 或自建 [Cap](https://capjs.js.org)）、内置动态 GIF 验证码、算术 / 按钮题
- 超时、失败次数持久化；CAS 共享黑名单；昵称风险检测；新人 N 小时内只能发文字

**反垃圾**
- 屏蔽词 / 正则、链接 / 转发 / 媒体锁、刷屏、重复消息波检测、防突袭
- AI 审核：任意 OpenAI 兼容接口，可审新人前 N 条或全部消息，支持看图识别图片广告；NFKC 归一化防花体字绕过
- 自学习：管理员 `/spam`、`/notspam` 或撤销处罚都会记成样本，相似消息直接命中，并作为 few-shot 喂给 AI
- 主页查重：定时扫描成员简介 / 频道里的相同链接，只上报到日志群，不自动处罚

**管理**
- 警告、禁言、踢出、封禁、全局封禁（`/gban`）、联邦封禁、`/votekick` 投票踢人、举报达阈值自动禁言
- 管理员 / 入群白名单，可指定谁能在 Telegram 里管理哪个群
- 处罚记录一键撤销、操作日志频道、`/zombies` 清理已注销账号、每日自动备份数据库

**内容与活跃**
- 笔记：群里点目录或发 `#名字` 只会私聊发给本人，并记录取用日志（v1.17.0）
- 自动回复（完全一致 / 开头 / 正则、多条随机、自动删除）、欢迎语（图片 / 动图 / 视频 / 按钮，可自动删除）
- 定时消息、定时广告（Markdown、图片、按钮、置顶、发送前预览）、夜间全员禁言
- 抽奖：多奖项、周期抽奖、积分 / 邀请门槛、公开种子可验证
- 签到积分、发言积分、活跃榜、邀请统计（专属链接、通过验证才算、邀请榜）
- 支持话题群（Topics）

**管理面板**
- Telegram 内 Mini App：管理员私聊 `/settings` 一键打开，四个入口（动态 / 规则 / 成员 / 内容），手机和电脑都适配，深色模式跟随系统
- 私聊按钮式设置面板
- 可选的 AstrBot 后台面板，功能与 Mini App 一致

## 快速开始

### 1. 创建机器人
找 [@BotFather](https://t.me/BotFather) 新建机器人拿到 Token，并在 Group Privacy 里关掉隐私模式。

### 2. 配置
```bash
cp tg-guard/oai_bot/config.example.yaml config.yaml
```
至少填写：

| 配置项 | 说明 |
| --- | --- |
| `bot.token` | BotFather 给的 Token（不要和其他机器人共用） |
| `bot.owner_ids` | 你的 TG 数字 ID，可在任意群用管理命令和 `/gban` |
| `bot.allowed_groups` | 只允许这些群使用，留空不限制 |
| `bot.log_chat_id` | 操作日志发到哪个群 / 频道，0 不记录 |

敏感项也可以用环境变量覆盖，如 `OAI_BOT_TOKEN`、`OAI_AI_API_KEY`。

### 3. 启动
```bash
docker compose -f docker-compose.example.yaml up -d --build
```
或手动构建镜像：
```bash
docker build --build-arg VERSION=v1.17.0 -t oai-guard:v1.17.0 tg-guard/oai_bot
```
不用 Docker 也可以直接编译（Go 1.26+）：
```bash
cd tg-guard/oai_bot
go build -o oai_bot ./cmd/oai_bot
./oai_bot -c ../../config.yaml
```

### 4. 拉进群
把机器人拉进群设为管理员（删除消息、封禁用户、邀请用户、置顶），私聊机器人 `/start` 或 `/settings` 开始设置。

## 可选功能

### Mini App 管理面板 / 网页验证
1. 把容器的 `8080` 用 Nginx / Caddy 反代成 HTTPS，填到 `web.public_url`，重启机器人。
2. 管理员私聊机器人发 `/settings`，点「📱 打开完整管理面板」；之后私聊左下角菜单按钮也能直接打开。
3. 在每个群「成员与权限 › 私聊管理权限」里设置谁能用；全局设置只有 `bot.owner_ids` 里的人能看到。
4. 网页验证还需在 `captcha` 里配置 Turnstile 或 Cap 的 site key / secret；不配就用内置动态 GIF 验证码。

不需要去 BotFather 额外配置域名。改了 `astrbot_plugin_tg_guard/pages/dashboard` 的前端后，运行一次 `scripts/sync-webapp.sh` 同步到 Mini App。

### AI 审核
在 `ai` 里填 OpenAI 兼容接口的 `base_url`、`api_key`、`model`（便宜的小模型就够），`threshold` 是封禁的置信度门槛。也可以之后在面板里改。

### AstrBot 面板插件 `astrbot_plugin_tg_guard`
1. `config.yaml` 里设置 `admin.listen: ":8090"` 和 16 位以上的 `admin.token`。
2. 让 AstrBot 和机器人在同一个 Docker 网络里（`8090` 不要映射到公网）。
3. 把插件放进 AstrBot 的 `data/plugins/`，插件配置里填 `oai_url`（如 `http://oai-bot:8090`）和同一个 token。

需要 AstrBot ≥ 4.26.0。

### 呼叫站长插件 `astrbot_plugin_call_owner`
群里多人点按钮或发 `/call`，在时间窗口内达到人数（默认 10 分钟 5 人）就通过 [喵提醒](https://miaotixing.com) 给站长打电话；支持冷却时间、允许的群、黑名单、定时发按钮消息、通话记录和面板设置。站长可用 `/awake` 表示已醒。

插件配置里填喵提醒「语音电话」提醒的喵码（t 开头）和站长 TG ID 即可。

## 常用命令

| 命令 | 说明 |
| --- | --- |
| `/start` `/settings`（私聊） | 打开设置面板 / Mini App |
| `/warn` `/unwarn` `/mute` `/unmute` `/kick` `/ban` `/unban` | 回复消息使用 |
| `/spam` `/notspam` | 删封并学习 / 标记为正常 |
| `/gban` `/ungban` | 全局封禁（仅主人） |
| `/filter` `/filters` `/lock` `/flood` | 屏蔽词、锁定、刷屏设置 |
| `/purge` `/del` `/pin` | 批量删除、删除、置顶 |
| `/votekick` | 投票踢人 |
| `/checkin` `/top` `/me` | 签到、排行榜、我的积分 |
| `/invites` `/myinvites` | 邀请榜、我的邀请链接 |
| `/notes` `/get 名字` | 笔记目录、取笔记（私聊发送） |
| `/rules` `/report` `/id` `/help` | 所有人可用 |

完整命令见私聊面板和 `/help`。

## 注意事项
- Bot API 拿不到完整成员列表，成员列表 / 主页查重只覆盖机器人见过的人。
- 建议先在配置里开 `bot.observe_only: true` 观察几天，确认误判率再正式处罚。
- 管理接口只做了 Token 鉴权，请只在内网监听。

## 更新记录与路线图
- 每个版本的改动见 [CHANGELOG.md](CHANGELOG.md)
- 功能盘点和计划见 [docs/ROADMAP.md](docs/ROADMAP.md)

## 参与贡献
欢迎 Fork 本仓库后提交 Pull Request。提 Issue 时请附上版本号和复现步骤，**不要贴出 Token 或配置文件**。

## 贡献者
- [LLJ99999](https://github.com/LLJ99999) · 作者
- [Claude](https://claude.ai)（Anthropic）· 参与开发与文档

## License
[MIT](LICENSE) © 2026 LLJ99999
