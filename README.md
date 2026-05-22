# cpa-pool-manager

一个面向 **CLIProxyAPI / CPA-Manager** 的轻量号池控制平面。

这个项目首先要解决的核心痛点不是“批量删号”或“日志巡检”，而是：

> **让号池优先使用“最近要刷新额度”的账号，从而最大化整体额度使用率。**

在一个有很多账号的池子里，如果使用顺序不合理，就会出现：

- 该先用、马上要刷新的账号没有被优先消耗
- 长时间后才恢复的账号反而被排在前面
- 某些账号已经额度耗尽却还在前排
- 某些账号明明恢复可用了，却没有及时回到池子里

`cpa-pool-manager` 的职责，就是围绕这个核心目标，把账号池的**排序、禁用、恢复、异常识别**做成一个可审计、可预演、可自动执行的控制平面。

它不代理推理流量，而是专门负责：

- 按真实恢复时间重排账号使用顺序
- 识别 **额度用尽** 账号并自动禁用
- 识别 **额度恢复** 账号并自动启用
- 识别 **明确 401 / token_invalidated** 账号并先自动禁用、再进入人工确认
- 提供一个内置的中文管理页面，用于扫描、复核、追踪提案历史

> 默认建议运行在 `APP_MODE=dry-run`，先看提案，再切 `apply`。

---

## 项目目标

在基于 CLIProxyAPI / CPA 的账号池里，账号状态会不断变化，但更重要的是：

**账号的使用顺序本身决定了额度利用率。**

理想情况下，池子应该尽量优先使用：

- **最近就会刷新额度**的账号
- **当前仍可用**的账号
- **真正处于可工作状态**的账号

而不应该继续把：

- 已额度耗尽的账号
- 已失效的账号
- 恢复时间更晚的账号

排在前面。

因此，这个项目的核心目标是两层：

1. **动态维护正确的账号使用顺序**
2. **围绕排序结果自动治理异常账号状态**

---

## 为什么“最近要刷新额度”的账号应该优先用

这个项目背后的策略不是“谁最闲先用谁”，而是：

> **谁恢复得更快，谁更应该优先消耗。**

原因很简单：

- 如果一个账号很快就会恢复额度，那么现在先把它用掉，过不了多久它又能重新加入池子
- 如果一个账号很久以后才恢复，现在就把它排在前面，往往会更早把它打空，然后长时间失去这部分容量

换句话说，正确的策略不是“把账号平均打散”，而是：

- 尽量让**短恢复周期账号更高频周转**
- 让**长恢复周期账号不要过早被耗尽**
- 让整个池子的可用容量在时间轴上分布得更平滑

这样做的结果通常是：

- 更少出现“前排账号全空、后排账号又不该顶上来”的情况
- 更快恢复整体吞吐
- 更高的额度利用率
- 更稳定的池子可用性

---

## 排序策略示例

假设当前有 4 个启用中的账号：

| 账号 | 当前状态 | 下一次可恢复/可重试时间 |
| --- | --- | --- |
| A | 可用 | 10 分钟后 |
| B | 可用 | 2 小时后 |
| C | 可用 | 30 分钟后 |
| D | 可用 | 12 小时后 |

如果你的目标是**最大化额度利用率**，更合理的使用顺序通常应该更偏向：

1. A（最近恢复）
2. C
3. B
4. D（最晚恢复）

因为这样意味着：

- A 现在即使被优先消耗，也很快又会恢复
- C 其次
- B、D 这种恢复更慢的账号，不应该被过早打空

### 一个更直观的理解

如果反过来优先使用 D：

- D 被打空以后，要等很久才能回来
- 你提前损失了一块长期不可用容量

而如果优先使用 A：

- A 用完后很快又能恢复
- 这部分容量会更快重新回到池子

这就是为什么 **“最近要刷新额度的账号优先使用”** 往往比“随便轮询”或“固定顺序”更有效。

---

## 核心能力

### 1. 独立优先级重排：`POST /scan/full?mode=reorder`

这是项目最核心的一条能力。

它只做优先级分析与重排，不混入 quota / 401 处理：

- 根据真实恢复时间 / retry 时间进行排序
- 优先让**更快恢复额度**的账号排到更合适的位置
- 只针对**当前启用账号**生成 `REORDER_PRIORITY`
- **禁用账号不参与重排**

目标是让池子的实际使用顺序更贴近“最大化额度利用率”的策略。

### 2. 全量额度检查：`POST /scan/full?mode=quota`

全量额度检查用于维护账号可用性，保证排序集合本身是干净的：

- 通过 `wham/usage` 探测账号额度
- 对当前启用、但 `limit_reached=true` 的账号生成 **自动禁用** 提案
- 对当前禁用、但 `limit_reached=false` 的账号生成 **自动启用** 提案
- 对 probe 明确返回 401 / `token_invalidated` 的账号：
  - 先生成 **自动禁用**
  - 再生成 **加入 401 人工确认**

### 3. 轻量问题扫描：`POST /scan/issues`

低成本扫描，适合高频巡检：

- 读取 auth-files 当前状态
- 可选读取管理日志
- 找出当前明确待处理的问题
- 生成提案

主要识别：

- 明确 401
- quota / `usage_limit_reached`

### 4. 401 人工确认列表：`GET /review/401`

单独列出所有需要人工确认的明确 401 账号。

当前列表会合并两类来源：

1. auth-file 当前状态里明确带 401 信号的账号
2. 全量额度检查时 probe 出来的明确 401 账号

### 5. Proposal 历史与执行

所有扫描结果都会生成 Proposal 并持久化到 SQLite。

Proposal 是这个项目的审计核心：

- 可以回看每一轮扫描识别了什么
- 可以区分 dry-run 与 apply 行为
- 可以判断系统到底“识别到了什么、准备怎么做”

### 6. 内置中文管理页：`GET /`

内置一个面向日常运维的中文控制台，支持：

- 查看运行模式
- 手动触发问题扫描 / 额度检查 / 优先级重排
- 查看当前问题
- 查看 401 人工确认列表
- 查看 Proposal 历史
- 查看全量任务进度

---

## 鉴权模型

当前管理台与脚本调用共用 **CPA 管理密钥**，但浏览器与脚本走的是两种不同路径。

### 浏览器访问

浏览器访问管理页时：

1. 打开 `GET /`
2. 若未登录，看到登录页
3. 在登录页输入与 CPA 共用的管理密钥
4. 前端将密钥提交到：
   - `POST /auth/login`
5. 服务端校验通过后，签发一个 **HttpOnly session cookie**
6. 浏览器后续访问管理台 API 时，自动携带该 cookie

这意味着：

- 管理密钥不会拼接到 URL 上
- 前端页面不会靠 query string 传递密钥
- 浏览器侧持有的是登录后的 session cookie，而不是直接把原始密钥放在地址栏里

### 脚本 / API 调用

脚本和自动化调用仍然可以直接使用管理密钥请求 API：

- `X-CPA-Management-Key: <key>`
- 或 `Authorization: Bearer <key>`

因此：

- **浏览器用户**走登录页 + cookie 会话
- **脚本用户**继续走 header 方式

### 相关接口

- `POST /auth/login`
- `POST /auth/logout`

---

## 关键业务规则

### 优先级规则

这个项目默认认为：

- **账号顺序本身就是资源利用策略的一部分**
- 应优先让**最近将要恢复 / 最近可再次使用**的账号处在更优先的位置

优先级重排只针对：

- **当前启用账号**

不会再对禁用账号生成重排提案。

### quota 规则

quota 与 401 是两条独立链路，不能混用。

- `limit_reached=true` 且账号当前启用 → 自动禁用候选
- `limit_reached=false` 且账号当前禁用 → 自动启用候选

> 当前口径：**只认 `rate_limit.limit_reached`**。

### 401 规则

只有**明确 401** 才进入 401 流程。

当前 401 判断来源包括：

- auth-file 状态 / message 中的明确 401 信号
- quota probe 返回：
  - `status_code = 401`
  - `X-Openai-Authorization-Error: 401`
  - `X-Openai-Ide-Error-Code: token_invalidated`
  - body 中明确的 token invalidated 语义

处理方式：

1. 如果账号当前启用：先自动禁用
2. 加入 401 人工确认列表
3. 删除动作保持人工确认，不自动删除

### quota / reorder 分离规则

`/scan/full` 已拆成两个真正独立的执行模式：

- `mode=quota`：只处理 quota / 401
- `mode=reorder`：只处理优先级重排

---

## 运行模式

通过环境变量 `APP_MODE` 控制：

### `APP_MODE=dry-run`

- 扫描会生成 Proposal
- 不会执行真实变更
- 适合作为默认模式

### `APP_MODE=apply`

- 扫描生成 Proposal 后会自动执行可执行动作
- 适合在规则确认稳定后启用

> 推荐流程：始终先 `dry-run` 验证，再切 `apply`。

---

## 项目结构

```text
cpa-pool-manager/
├── cmd/server/main.go                # 入口
├── deploy/
│   ├── Dockerfile                    # 镜像构建
│   ├── docker-compose.yml            # 源码构建版 compose
│   └── docker-compose.image.yml      # 直接拉镜像运行版 compose
├── internal/
│   ├── api/http.go                   # HTTP API + 内置管理页面
│   ├── config/config.go              # 配置加载
│   ├── cpa/
│   │   ├── client.go                 # CLIProxyAPI / CPA client
│   │   └── quota_probe.go            # wham usage probe + 401 识别
│   ├── engine/
│   │   ├── engine.go                 # 核心扫描 / 提案 / 应用逻辑
│   │   └── loop.go                   # 后台循环
│   ├── store/store.go                # SQLite 持久化
│   └── types/
│       ├── quota.go                  # quota 类型
│       └── types.go                  # proposal / issue / review 类型
├── .env.example
├── IMPLEMENTATION_NOTES.md           # 实现说明
└── README.md
```

---

## HTTP 接口

### 基础接口

- `GET /`
- `GET /healthz`
- `GET /status`

### 认证接口

- `POST /auth/login`
- `POST /auth/logout`

### 只读接口

- `GET /issues`
- `GET /review/401`
- `GET /proposals`
- `GET /progress/full`

### 扫描接口

- `POST /scan/issues`
- `POST /scan/full?mode=quota`
- `POST /scan/full?mode=reorder`

### 人工操作接口

- `POST /accounts/:name/disable`
- `POST /accounts/:name/delete`
- `POST /proposals/:id/apply`

> 注意：当 `APP_MODE=dry-run` 时，`/proposals/:id/apply` 会被阻止。

---

## 数据来源与动作输出

### 输入

1. CLIProxyAPI Management API `auth-files`
2. CLIProxyAPI Management API `logs`
3. CLIProxyAPI Management API `api-call`
   - 用于调用 `https://chatgpt.com/backend-api/wham/usage`

### 输出动作

- 删除 auth file
- 禁用 auth file
- 启用 auth file
- 更新 priority
- 标记进入 401 人工确认

---

## 配置

`.env.example` 当前包含：

```env
APP_PORT=18417
APP_MODE=dry-run
STATE_DB_PATH=/data/state.db
CPA_BASE_URL=http://host.docker.internal:8317
CPA_MANAGEMENT_KEY=change-me
CPAM_BASE_URL=http://host.docker.internal:18317
POLL_INTERVAL_SECONDS=60
LOG_SCAN_INTERVAL_SECONDS=5
MAX_POLL_CONCURRENCY=2
PRIORITY_ORDER=desc
ENABLE_LOG_SIGNAL=true
ENABLE_QUOTA_POLL=true
REQUEST_TIMEOUT_SECONDS=15
```

### 关键配置说明

- `APP_MODE`
  - `dry-run` / `apply`
- `CPA_BASE_URL`
  - CLIProxyAPI Management API 地址
- `CPA_MANAGEMENT_KEY`
  - 管理 API 密钥，同时也用于浏览器登录校验
- `STATE_DB_PATH`
  - SQLite 状态库路径
- `ENABLE_LOG_SIGNAL`
  - 是否启用日志信号辅助识别
- `ENABLE_QUOTA_POLL`
  - 是否启用 `wham/usage` quota probe
- `PRIORITY_ORDER`
  - 重排顺序，通常为 `desc`

---

## 快速开始（直接拉取镜像）

如果你只是想尽快跑起来，推荐直接使用预构建镜像：

- `ghcr.io/timefunnel/cpa-pool-manager:latest`

### 1. 准备配置

```bash
cp .env.example .env
```

至少需要确认这些配置：

```env
APP_MODE=dry-run
CPA_BASE_URL=http://host.docker.internal:8317
CPA_MANAGEMENT_KEY=change-me
```

如果你的 CPA / 管理 API 不在 Docker host 上，请按实际环境修改 `CPA_BASE_URL`。

### 2. 使用镜像版 Compose 启动

```bash
cd deploy
docker compose -f docker-compose.image.yml up -d
```

服务默认监听：

- `http://127.0.0.1:18417/`

### 3. 登录管理台

1. 打开浏览器访问 `http://127.0.0.1:18417/`
2. 输入与 CPA 共用的管理密钥
3. 登录后使用控制台

### 4. 脚本验证

例如：

```bash
curl -H 'X-CPA-Management-Key: your-key' http://127.0.0.1:18417/status
```

---

## 本地源码构建运行

如果你希望从源码构建而不是直接拉镜像：

### 1. 准备配置

```bash
cp .env.example .env
```

建议先设置：

```env
APP_MODE=dry-run
```

### 2. 使用源码版 Docker Compose 启动

```bash
cd deploy
docker compose up --build
```

服务默认监听：

- `http://127.0.0.1:18417/`

---

## 推荐使用方式

### 浏览器管理台

1. 打开 `GET /`
2. 输入管理密钥登录
3. 通过会话 cookie 使用控制台

### 脚本调用

直接为请求加上管理密钥 header，例如：

```bash
curl -H 'X-CPA-Management-Key: your-key' http://127.0.0.1:18417/status
```

### 优先级整理

用：

```bash
POST /scan/full?mode=reorder
```

适合单独整理当前启用账号的 priority，让账号顺序尽量贴近“最大化额度利用率”的目标。

### 额度治理

用：

```bash
POST /scan/full?mode=quota
```

适合：

- 发现应禁用的额度用尽账号
- 发现应启用的额度恢复账号
- 发现明确 401 账号

### 日常轻量检查

用：

```bash
POST /scan/issues
```

适合高频、低成本巡检。

---

## 当前限制

1. 当前鉴权模型仍然是“共享管理密钥 + 浏览器会话”，还不是完整的多用户权限体系
2. 管理页面仍然嵌入在 Go 源码中的单文件 HTML/JS 中
3. 当前 Docker 构建里仍依赖 build-time `go mod tidy`
4. `go.sum` 尚未稳定纳入项目
5. Proposal 已可审计，但更细的“手工禁用标记”没有持久化建模

---

## 相关文档

- [IMPLEMENTATION_NOTES.md](./IMPLEMENTATION_NOTES.md)

---

## 快速结论

如果你只记住这个项目的 4 句话：

1. **核心目标是优化号池使用顺序，最大化整体额度利用率**
2. **`limit_reached=false` 就表示账号当前可用**
3. **priority 重排只针对当前启用账号**
4. **先 dry-run，确认 proposal，再 apply**
