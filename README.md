# TG Guard · Telegram 群管机器人

一个 Go 写的 Telegram 群管理机器人，类似 Rose / NMBot：入群人机验证、反广告、AI 审核、定时广告、按钮式设置面板，外加一个可选的 AstrBot 后台面板插件。单二进制 + SQLite，一个 Docker 容器就能跑。

## 功能

**入群验证**
- 申请入群（chat_join_request）自动批准/拒绝，或直接入群后先禁言再验证
- 验证方式：Mini App 网页验证（Cloudflare Turnstile 或自建 [Cap](https://capjs.js.org)）、内置动态 GIF 验证码、算术/按钮题
- 超时、失败次数持久化，CAS 共享黑名单检测，昵称风险检测

**反垃圾**
- 规则：屏蔽词/正则、链接/转发/媒体锁、刷屏（flood）、重复消息波检测、防突袭（antiraid）
- AI 审核：任意 OpenAI 兼容接口；新人前 N 条或全部消息送审，置信度阈值，处罚可选删除/禁言/封禁
- 自学习：管理员 /spam、/notspam 或撤销处罚都会记成样本，相似消息直接命中，样本还会作为 few-shot 喂给 AI
- 主页查重：定时扫描成员个人简介/频道里的相同链接，只上报到日志群，不自动处罚

**管理**
- 警告、禁言、封禁、全局封禁（/gban），联邦封禁
- 管理员白名单、入群白名单、/report 举报
- 笔记、自动回复、欢迎语（带按钮）、定时消息、夜间模式
- 定时广告：Markdown、图片、按钮，可置顶、可删除上一条，发送前私聊预览
- 处罚记录（可一键撤销）、成员列表、统计、操作日志频道
- 私聊按钮式设置面板

**AstrBot 面板（可选）**
`astrbot_plugin_tg_guard` 把上面所有设置搬进 AstrBot 后台网页里管理，通过内网管理接口（Bearer token）代理到机器人。

## 快速开始

1. 找 [@BotFather](https://t.me/BotFather) 新建机器人，拿到 token；在 Group Privacy 里关掉隐私模式。
2. 复制配置并修改：
   ```bash
   cp oai_bot/config.example.yaml config.yaml
   # 至少填 bot.token 和 bot.owner_ids（你的 TG 数字 ID）
   ```
3. 启动：
   ```bash
   docker compose -f docker-compose.example.yaml up -d --build
   ```
4. 把机器人拉进群并设为管理员（需要删除消息、封禁用户、邀请用户权限），私聊机器人 `/start` 打开设置面板。

不想用 Docker 也可以直接编译（Go 1.26+）：
```bash
cd oai_bot && go build -o oai_bot ./cmd/oai_bot && ./oai_bot -c ../config.yaml
```

### 网页验证（可选）
用 Mini App 验证需要一个 HTTPS 地址：把容器的 8080 反代出去，填到 `web.public_url`，再在 `captcha` 里配置 Turnstile 或 Cap 的 site key / secret。不配的话可以用内置的动态 GIF 验证码。

### AI 审核（可选）
在 `ai` 里填 OpenAI 兼容接口的 `base_url`、`api_key`、`model`（便宜的小模型就够），也可以之后在面板里改。

### AstrBot 面板（可选）
1. `config.yaml` 里设置 `admin.listen: ":8090"` 和 16 位以上的 `admin.token`。
2. 让 AstrBot 和机器人在同一个 Docker 网络里。
3. 把 `astrbot_plugin_tg_guard` 放进 AstrBot 的 `data/plugins/`，在插件配置里填 `oai_url`（如 `http://oai-bot:8090`）和同一个 token。

## 常用命令

| 命令 | 说明 |
| --- | --- |
| `/start`（私聊） | 打开设置面板 |
| `/warn` `/mute` `/ban` `/unban` | 回复消息使用 |
| `/spam` `/notspam` | 回复消息：删封并学习 / 标记为正常 |
| `/gban` | 全局封禁（仅主人） |
| `/ad` `/ads` `/adsend` `/adpreview` | 定时广告 |
| `/bioscan` | 立刻做一次主页查重 |
| `/report` | 举报（普通成员可用） |
| `/stats` | 群统计 |

完整命令在私聊面板和 `/help` 里。

## 说明
- Bot API 拿不到完整成员列表，成员列表/主页查重只覆盖机器人见过的人。
- 建议先开 `observe_only: true` 观察几天，确认误判率再正式处罚。
- 管理接口没有做公网鉴权以外的防护，请只在内网监听。

## License
MIT
