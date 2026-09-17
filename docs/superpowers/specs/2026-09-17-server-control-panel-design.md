# 多服务商服务器控制面板设计说明

状态：已完成产品设计确认，等待书面审阅  
日期：2026-09-17

## 1. 产品定位

本项目是一个面向单一管理员、自托管运行的多服务商服务器控制面板。管理员可以配置多个同类型或不同类型的服务商连接，例如两个 AWS 账户、三个 VirtFusion 控制端，并在统一界面中查看服务器和执行基础管理操作。

项目分四个阶段交付：

1. 完成统一核心、完整界面和 Mock Provider。
2. 接入 AWS 与 VirtFusion。
3. 接入 GCP 与 Virtualizor。
4. 接入 SolusVM 2，并保留第三方 PVE 面板适配器扩展点。

PVE 原生接口和第三方 PVE 自研面板不在当前四阶段的实现范围内。

## 2. 目标

- 支持一个本地管理员账号，无公开注册或多租户功能。
- 支持同一种服务商配置任意多个独立连接。
- 将不同服务商的服务器映射为统一资源模型。
- 支持开机、关机和重启。
- 根据服务商能力提供内嵌控制台、新窗口控制台或服务商后台跳转。
- 支持首次启动设置向导及环境变量无人值守初始化。
- 同一程序兼容 SQLite 和 MySQL，部署时选择其中一种数据库。
- 提供应用级加密备份、下载、保留和恢复能力。
- 同时提供单可执行文件和 Docker Compose 两种发行方式。
- 为后续第三方 PVE 面板适配器提供稳定的 Provider 接口、契约测试和接入文档。

## 3. 非目标

- 不创建、删除、重装、迁移或调整服务器规格。
- 不提供账单、成本分析、流量计费或资源采购功能。
- 不提供批量电源操作。
- 不支持多用户、RBAC、组织或租户隔离。
- 首版云认证不支持 AWS AssumeRole、实例角色、GCP 服务账号模拟或宿主环境身份。
- 不持续记录 VNC 或串行控制台的输入输出。
- 不对 SQLite 和 MySQL 同时写入，也不实现跨数据库自动故障切换。

## 4. 技术方案

采用模块化单体架构：

- 后端：Go。
- 前端：React、TypeScript。
- HTTP：REST API；状态更新使用 Server-Sent Events；控制台数据通道使用 WebSocket。
- 数据库：SQLite 或 MySQL，由 `DATABASE_URL` 在启动时选择。
- 前端发行：生产构建产物通过 `go:embed` 嵌入 Go 可执行文件。
- 后台任务：使用数据库持久化任务队列和进程内 Worker，不强制依赖 Redis。
- 部署：单文件直接运行，或使用 Docker Compose 运行应用与可选 MySQL。

模块边界：

- `auth`：初始化、登录、退出、密码修改和 Session。
- `connections`：服务商连接、凭据加密、连接测试和健康状态。
- `providers`：统一接口、注册表、能力和契约测试。
- `inventory`：服务器同步、本地索引和状态刷新。
- `operations`：电源操作、幂等、状态机和审计。
- `console`：短期票据、WebSocket 代理和控制台回退。
- `jobs`：持久任务、租约、重试和定时调度。
- `backup`：导出、加密、保留、验证和恢复。
- `audit`：安全事件和管理员操作日志。
- `web`：嵌入前端、REST、SSE 和 WebSocket 路由。

## 5. 供应商抽象

统一 Provider 接口表达业务能力，不向上层暴露供应商 SDK 类型：

```go
type Provider interface {
	ValidateConnection(ctx context.Context) (ConnectionInfo, error)
	ListServers(ctx context.Context, cursor *Cursor) (ServerPage, error)
	GetServer(ctx context.Context, ref ServerRef) (RemoteServer, error)
	StartServer(ctx context.Context, ref ServerRef) (ActionReceipt, error)
	StopServer(ctx context.Context, ref ServerRef) (ActionReceipt, error)
	RebootServer(ctx context.Context, ref ServerRef) (ActionReceipt, error)
	OpenConsole(ctx context.Context, ref ServerRef, mode ConsoleMode) (ConsoleTarget, error)
	ProviderPortalURL(ctx context.Context, ref ServerRef) (*url.URL, error)
}
```

每个服务器返回以下能力：

- `can_start`
- `can_stop`
- `can_reboot`
- `can_embed_console`
- `can_open_console_window`
- `has_provider_portal`

能力可带不可用原因。前端只根据能力呈现操作，不使用供应商名称硬编码功能。

规范化服务器状态为：

- `pending`
- `running`
- `stopping`
- `stopped`
- `rebooting`
- `suspended`
- `error`
- `unknown`

保留供应商原始状态用于排错，但不直接驱动通用界面。

## 6. 阶段一 Mock Provider

Mock Provider 是正式适配器而非临时页面假数据，必须通过与真实服务商相同的接口和契约测试。它支持：

- 每个 Mock 连接生成一组独立服务器。
- 运行、停止、过渡和错误状态。
- 可配置的操作延迟、失败率和网络错误。
- 内嵌控制台、新窗口控制台、仅后台跳转和完全不可用四种能力组合。
- 连接健康、同步失败、限流和凭据失效模拟。

阶段一完成后，管理员可完整体验初始化、连接管理、同步、筛选、电源操作、操作记录、控制台回退、备份和恢复。

## 7. 后续真实适配器

### 7.1 AWS

- 静态凭据：Access Key ID、Secret Access Key、可选 Session Token。
- 支持配置一个或多个区域。
- 使用 EC2 API 发现实例并执行启动、停止和重启。
- 优先使用 EC2 Serial Console 作为串行控制台能力。
- 生成 AWS Console 资源直达链接作为服务商后台回退。

### 7.2 VirtFusion

- 使用 Base URL 和官方 API 支持的静态 Token/Key。
- 通过 API 同步服务器并执行电源操作。
- 在 API 可提供 VNC 信息时，通过后端代理内嵌 noVNC。
- 可获得临时 Web 控制台 URL 时支持新窗口打开。
- 其他情况回退到 VirtFusion 服务商后台。

### 7.3 GCP

- 使用 Service Account JSON 静态凭据。
- 支持配置一个或多个项目与区域/可用区范围。
- 使用 Compute Engine API 发现实例并执行启动、停止和重置。
- 支持串行端口能力时提供 Web 串行控制台；否则使用 Google Cloud Console 资源链接。

### 7.4 Virtualizor

- 使用 API Endpoint、API Key 和 API Password/Secret。
- 通过官方 API 同步 VPS、执行电源操作并请求 VNC 信息。
- 根据服务端版本及 API 返回能力选择内嵌代理、新窗口或后台跳转。

### 7.5 SolusVM 2

- 仅支持 SolusVM 2，不兼容 SolusVM 1。
- 使用官方 API 支持的静态认证。
- 同步服务器、执行电源操作并探测控制台能力。
- 适配器实现相同的 Provider 契约，不向上层泄露 SolusVM 专属模型。

### 7.6 第三方 PVE 扩展点

第四阶段提供 Provider SDK 文档、示例骨架和契约测试入口。未来每家第三方 PVE 面板作为独立 Provider 类型接入；不会假设不同自研面板共享 API，也不会在核心代码中添加按厂商判断的分支。

## 8. 数据模型

所有内部主键使用 UUID，时间以 UTC 存储并通过 RFC 3339 传输。

### 8.1 `users`

- `id`
- `username`，唯一
- `password_hash`
- `initialized_at`
- `created_at`、`updated_at`、`last_login_at`

数据库只允许存在一个有效用户。首次初始化在一个事务内完成，并写入不可逆的初始化标记。

### 8.2 `sessions`

- `id_hash`
- `user_id`
- `csrf_secret_hash`
- `expires_at`、`last_seen_at`
- `created_at`

浏览器只保存随机 Session Cookie；数据库保存其哈希。Session Cookie 使用 `HttpOnly`、`Secure` 和 `SameSite=Lax`。

### 8.3 `provider_connections`

- `id`
- `name`
- `provider_type`
- `endpoint`
- `settings_json`
- `credentials_ciphertext`
- `credentials_nonce`
- `credentials_key_version`
- `enabled`
- `health_status`
- `last_tested_at`、`last_synced_at`
- `last_error_code`、`last_error_message`
- `created_at`、`updated_at`、`deleted_at`

`provider_type` 不唯一，因此同一种服务商可创建多个连接。凭据字段只写不读，任何 API 响应均不返回凭据原文或密文。

### 8.4 `servers`

- `id`
- `connection_id`
- `external_id`
- `scope`
- `name`
- `normalized_state`
- `remote_state`
- `spec_json`
- `addresses_json`
- `capabilities_json`
- `portal_url`
- `last_seen_at`、`last_state_checked_at`
- `hidden_at`
- `created_at`、`updated_at`

唯一约束为 `(connection_id, scope, external_id)`。同步未发现的服务器先隐藏而不物理删除，以保留操作和审计历史。

### 8.5 `operations`

- `id`
- `server_id`、`connection_id`
- `action`：`start`、`stop`、`reboot`
- `status`：`queued`、`running`、`verifying`、`succeeded`、`failed`、`timed_out`、`cancelled`
- `idempotency_key`
- `provider_request_id`
- `error_code`、`error_message`
- `queued_at`、`started_at`、`finished_at`

应用层与数据库约束共同保证每台服务器最多存在一个非终态操作。

### 8.6 `jobs`

- `id`
- `kind`
- `payload_json`
- `status`
- `attempts`、`max_attempts`
- `available_at`
- `lease_owner`、`lease_expires_at`
- `last_error`
- `created_at`、`updated_at`

Worker 通过短租约领取任务。进程异常退出后，租约到期的任务可重新领取。任务处理器必须幂等。

### 8.7 `console_sessions`

- `id`
- `server_id`
- `mode`
- `ticket_hash`
- `expires_at`
- `opened_at`、`closed_at`
- `result`

该表只保存会话生命周期，不保存控制台地址中的秘密、临时密码、连接字节流或终端内容。

### 8.8 `audit_logs`

- `id`
- `event_type`
- `target_type`、`target_id`
- `request_id`
- `source_ip`
- `metadata_json`
- `created_at`

元数据经过字段白名单过滤。普通界面不可编辑或删除审计记录。

### 8.9 `settings`

- `key`
- `value_json`
- `updated_at`

只保存非敏感运行设置。秘密必须进入加密凭据或部署环境。

## 9. 数据库兼容策略

- 启动时根据 `DATABASE_URL` 选择 SQLite 或 MySQL 驱动。
- 领域仓储接口与数据库驱动分离。
- 迁移保留统一版本号；确有语法差异时提供 SQLite/MySQL 两个等价迁移实现。
- 不依赖仅 MySQL 支持的生成列、存储过程或锁语义。
- SQLite 开启 WAL、外键和 busy timeout，并限制单实例写入。
- MySQL 使用事务和行锁实现同等业务约束。
- CI 对两种数据库运行相同的仓储和核心集成测试。

## 10. 初始化与认证

支持两种初始化方式：

1. 默认首次启动设置向导创建管理员。
2. 若提供完整的管理员环境变量，启动时执行无人值守初始化。

两种方式共用同一原子初始化逻辑。任一方式成功后，设置向导和环境变量初始化均不再覆盖现有管理员。缺失一部分环境变量时拒绝无人值守初始化，并保留向导可用。

密码使用 Argon2id 哈希。登录按用户名和来源地址组合限速，错误信息不区分用户名不存在或密码错误。所有改变状态的请求校验 CSRF Token 和同源信息。修改密码后撤销其他 Session。

## 11. 凭据安全

- 使用 AES-256-GCM 加密服务商凭据。
- 主密钥来自环境变量或权限受限的密钥文件，不写入数据库、备份、日志或镜像。
- 每条凭据使用独立随机 nonce。
- 连接 ID、服务商类型和密钥版本作为附加认证数据。
- 日志层统一过滤密码、Cookie、Authorization、Token、Secret、Service Account JSON、VNC 和串行临时凭据。
- Base URL 保存及使用时校验协议、解析结果和重定向目标，阻止云元数据、loopback、链路本地及未批准私网地址。
- 私网 VirtFusion、Virtualizor 或 SolusVM 控制端必须通过明确 CIDR 允许列表授权。

## 12. 同步流程

1. 调度器为每个启用连接创建同步任务。
2. Worker 获取连接级租约，防止手动与定时同步并发。
3. Provider 分页读取服务器并逐批 upsert。
4. 只有完整同步成功后，才隐藏本轮未出现的服务器。
5. 认证、权限、限流、网络和响应格式错误转为统一错误码。
6. 连接健康状态及最后成功同步时间写入数据库。
7. 前端通过 SSE 收到连接和服务器变更事件。

页面默认查询本地索引，不在页面加载时串行调用所有服务商。管理员可手动刷新单台服务器或整个连接。

## 13. 电源操作流程

1. API 验证登录、CSRF、服务器能力和当前状态。
2. 使用客户端提供或服务端生成的幂等键创建 `queued` 操作及对应任务。
3. Worker 获取服务器级租约并再次读取远端状态。
4. 若目标状态已经满足，则不重复发出远端写请求。
5. 调用 Provider 动作，记录非敏感回执并进入 `verifying`。
6. 使用有上限的退避轮询确认最终状态。
7. 成功、明确失败或超时后写入终态和审计日志。
8. 通过 SSE 将状态推送给界面。

对供应商限流、临时网络错误和 5xx 执行有限重试。若写请求因网络中断而结果不确定，必须先查询远端状态再决定是否重试。认证与权限错误不自动重试。

## 14. 控制台流程

控制台能力按以下顺序呈现：

1. 面板内嵌 VNC 或串行控制台。
2. 在新标签页或新窗口打开服务商提供的临时控制台 URL。
3. 跳转服务商后台中的服务器页面；无法生成资源直达链接时打开服务商首页。

内嵌流程：

1. 前端请求创建控制台会话。
2. API 验证能力和服务器状态，创建一次性短期票据。
3. 浏览器使用票据建立 WebSocket，握手后立即使票据失效。
4. 后端按 Provider 返回的经过验证目标代理 VNC 或串行字节流。
5. 会话达到空闲或最长时限后关闭并释放所有临时凭据。
6. 审计只记录打开、关闭、时长和结果。

Provider 返回的 TCP 或 WebSocket 目标必须经过地址策略校验，不能将控制台代理变为任意 SSRF 或 TCP 代理。

## 15. 备份与恢复

备份是应用级、与数据库类型无关的加密归档，而非 SQLite/MySQL 双写：

- 导出用户、连接、服务器索引、操作、任务、审计和设置数据。
- 包含格式版本、应用版本、创建时间、校验和及记录数量清单。
- 使用独立备份口令或备份密钥进行认证加密。
- 不包含运行时主密钥；恢复后仍需同一主密钥才能解密服务商凭据。
- 支持手动创建、下载、校验、恢复和定时保留。
- 默认目标为管理员配置的本地目录。
- 恢复前自动创建当前状态安全快照。
- 恢复过程先校验格式、版本、完整性和解密能力，再使用事务替换数据。
- 不允许将 SQLite 文件直接导入 MySQL；跨数据库恢复必须走应用级归档。

## 16. HTTP 与实时接口

所有 REST 路径使用 `/api/v1`。

### 16.1 初始化与认证

- `GET /setup/status`
- `POST /setup/initialize`
- `POST /auth/login`
- `POST /auth/logout`
- `GET /auth/me`
- `PUT /auth/password`

### 16.2 服务商连接

- `GET /connections`
- `POST /connections`
- `GET /connections/{id}`
- `PUT /connections/{id}`
- `DELETE /connections/{id}`
- `POST /connections/{id}/test`
- `POST /connections/{id}/sync`

### 16.3 服务器与操作

- `GET /servers`
- `GET /servers/{id}`
- `POST /servers/{id}/refresh`
- `POST /servers/{id}/actions/start`
- `POST /servers/{id}/actions/stop`
- `POST /servers/{id}/actions/reboot`
- `GET /operations`
- `GET /operations/{id}`
- `GET /events`

### 16.4 控制台与跳转

- `GET /servers/{id}/console-options`
- `POST /servers/{id}/console-sessions`
- `WS /ws/console/{ticket}`
- `GET /servers/{id}/provider-portal`

外部跳转只返回后端根据 Provider 规则生成并验证的 URL，不接受前端传入任意目标。

### 16.5 备份与设置

- `GET /backups`
- `POST /backups`
- `GET /backups/{id}/download`
- `POST /backups/validate`
- `POST /backups/{id}/restore`
- `GET /settings`
- `PUT /settings`

错误响应包含稳定 `code`、面向管理员的 `message` 和 `request_id`。敏感字段不进入 `details`。

## 17. 用户界面

使用左侧导航的运维控制台布局：

- **登录与初始化**：设置向导、登录和通用错误提示。
- **总览**：服务器总数、运行/停止/异常数量、连接健康、最近失败操作。
- **服务器**：跨服务商搜索；按服务商类型、连接、作用域和状态筛选。
- **服务器详情**：规格、地址、状态、数据新鲜度、能力、操作和历史。
- **服务商**：创建多个同类型连接、测试凭据、启停、同步和后台跳转。
- **操作记录**：查看排队、执行、验证、成功、失败和超时状态。
- **备份与设置**：备份、恢复、保留策略、同步周期和安全设置。

电源操作必须二次确认。界面显示数据更新时间和过期状态。桌面和平板为主要目标；手机支持查看和电源操作，但不以小屏内嵌 VNC 为核心体验。

## 18. 错误模型

稳定错误类别至少包括：

- 初始化冲突
- 未认证或 CSRF 失败
- 凭据无效
- 权限不足
- 功能不支持
- 服务器状态冲突
- 活动操作冲突
- 供应商限流
- 网络不可达
- 响应格式无效
- 操作超时
- 控制台目标被安全策略拒绝
- 备份校验或恢复失败
- 数据库不可用

单个连接故障不会阻塞其他连接。供应商故障时保留本地数据，标记错误与最后成功同步时间，不误删服务器。

## 19. 测试策略

### 19.1 单元测试

- 状态与能力映射。
- Argon2id 密码和 Session 生命周期。
- 凭据加解密及敏感字段过滤。
- 操作状态机、幂等和重试判断。
- 任务租约、崩溃恢复和定时调度。
- 控制台票据及地址安全策略。
- 备份格式、加密、校验和恢复。

### 19.2 Provider 契约测试

同一测试套件验证每个 Provider：

- 分页服务器映射。
- 状态和能力稳定性。
- 电源动作统一回执和错误。
- 控制台三层能力回退。
- 后台链接生成与验证。
- 认证失败、权限不足、限流、超时、畸形响应和分页中断。

### 19.3 数据库集成测试

SQLite 和 MySQL 均运行：

- 迁移与回滚安全检查。
- 仓储契约。
- 单服务器活动操作约束。
- 任务租约与重新领取。
- 同步完整成功和部分失败语义。
- 备份跨数据库恢复。

### 19.4 前端与端到端测试

- 筛选、确认框、错误提示和能力禁用状态。
- 初始化、登录、添加多个 Mock 连接和手动同步。
- 执行开机、关机、重启并观察完整状态流转。
- 验证内嵌、新窗口和后台跳转三种控制台路径。
- 创建、下载、校验和恢复备份。

## 20. 发行与运维

### 20.1 单文件发行

- 针对主要操作系统和架构构建单个可执行文件。
- 内嵌前端静态资源和数据库迁移。
- 默认可使用本地 SQLite 文件，也可通过 `DATABASE_URL` 连接 MySQL。
- 运行目录、数据目录、备份目录和主密钥文件均可显式配置。

### 20.2 Docker Compose

- 提供应用和 MySQL 服务。
- 可通过配置切换为 SQLite 并移除 MySQL 依赖。
- 数据、备份和可选密钥文件使用独立持久卷。
- 应用以非 root 用户运行，并提供存活和就绪检查。
- TLS 默认由外部反向代理终止，生产模式要求安全来源配置正确。

### 20.3 可观测性

- 结构化日志并携带 `request_id`、`job_id` 或 `operation_id`。
- `/health/live` 检查进程存活。
- `/health/ready` 检查数据库、迁移和必要运行状态，不调用外部服务商。
- 指标覆盖请求、同步、任务、供应商错误和活动控制台会话。

## 21. 四阶段验收

### 阶段一：核心、界面和 Mock

- 两种初始化方式及单用户认证可用。
- SQLite 与 MySQL 均可启动并通过核心测试。
- 可创建多个 Mock 连接并统一同步服务器。
- 三种电源操作、操作状态机和 SSE 更新可用。
- 控制台三层回退均可演示。
- 加密备份可在 SQLite 与 MySQL 间恢复。
- 单文件和 Docker Compose 发行物均可运行。

### 阶段二：AWS 与 VirtFusion

- 可创建多个 AWS 和 VirtFusion 连接。
- 真实清单、电源操作和连接健康检查可用。
- 支持范围内的串行/VNC 控制台和后台链接可用。
- 两个适配器通过 Provider 契约测试。

### 阶段三：GCP 与 Virtualizor

- 可创建多个 GCP 和 Virtualizor 连接。
- 真实清单、电源操作、控制台能力和后台链接可用。
- 两个适配器通过 Provider 契约测试。

### 阶段四：SolusVM 2 与扩展接口

- 可创建多个 SolusVM 2 连接。
- 真实清单、电源操作、控制台能力和后台链接可用。
- SolusVM 2 适配器通过 Provider 契约测试。
- 提供第三方 PVE 面板适配器文档、示例骨架和契约测试入口。

## 22. 关键设计决策

1. 使用模块化单体，满足单文件与 Docker Compose 两种发行要求。
2. 使用数据库持久任务队列，避免单文件模式强制依赖 Redis。
3. SQLite/MySQL 是部署时二选一，不进行双写。
4. 备份采用可跨数据库恢复的应用级加密归档。
5. 控制台采用内嵌、新窗口、服务商后台三级能力回退。
6. 首阶段完成真实交互的 Mock Provider，再按既定顺序接入五种真实服务商。
7. 第三方 PVE 面板只保留标准适配器扩展点，不假设存在统一 API。
