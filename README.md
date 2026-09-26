# Server Control Panel

单用户、多服务商服务器控制面板。当前版本已经提供安全初始化、登录、SQLite/MySQL 双数据库基础、多个服务商连接、统一服务器清单、安全电源操作、三级控制台回退和应用级加密备份，并已接入 Mock、AWS EC2、Google Cloud Compute Engine、VirtFusion User API 与 Virtualizor Enduser API。

界面与 Mock、AWS/VirtFusion、GCP/Virtualizor 以及第三方 PVE 扩展入口均已实现。SolusVM 2 的 management API 适配器源码仍保留，但因其官方 Token 属于管理员凭据，已按“仅客户账户凭据”策略停止运行时注册。第三方 PVE 部分只交付编译期 Provider SDK、契约测试和未注册的示例骨架，没有内置 PVE Provider，也不直接连接 Proxmox VE 原生 API。真实账号的权限、网络和控制台兼容性仍需在部署环境验收。

## 当前功能

- 创建多个独立的服务商连接，并单独测试连接与触发同步。
- 将不同连接下的服务器汇总到统一清单，支持按名称、区域、状态和连接筛选。
- 服务商凭据使用 AES-256-GCM 加密后入库，密钥支持版本轮换。
- 后台任务持久化、租约执行和失败重试；同步失败不会覆盖上一次成功清单。
- 根据服务商能力和服务器状态启用开机、关机、重启按钮，所有操作均需二次确认。
- 电源操作具有幂等保护、单服务器互斥、远端状态复核、操作历史和实时界面更新。
- 根据能力支持面板内嵌控制台、新窗口临时控制台和服务商后台三级回退。
- 支持创建、下载、校验和恢复应用级加密备份；恢复前自动创建安全快照。

完整部署步骤见 [docs/deployment.md](docs/deployment.md)。电源操作、控制台、服务商接入和备份运维要求分别见 [docs/operations.md](docs/operations.md)、[docs/console.md](docs/console.md)、[docs/providers.md](docs/providers.md) 和 [docs/backups.md](docs/backups.md)。

## 单文件运行

需要 Go 1.27、Node.js 24 和 npm 11：

```bash
make build
export CREDENTIAL_KEYS="1:$(openssl rand -base64 32)"
export CREDENTIAL_ACTIVE_KEY_VERSION=1
export BACKUP_DIRECTORY=data/backups
DATABASE_URL=sqlite://data/controlpanel.db PUBLIC_ORIGIN=http://127.0.0.1:8080 ./dist/controlpanel
```

首次打开 `http://127.0.0.1:8080` 后，通过设置向导创建唯一管理员。也可以在第一次启动前同时设置 `ADMIN_USERNAME` 与 `ADMIN_PASSWORD`；只设置其中一个会拒绝启动，已有管理员不会被覆盖。

## Docker Compose

复制 `.env.example` 为 `.env`，替换所有示例密码并设置真实的 HTTPS `PUBLIC_ORIGIN`。使用 `openssl rand -base64 32` 生成 32 字节凭据加密密钥，填入 `CREDENTIAL_KEYS` 的版本前缀后面，例如 `1:<生成值>`。请把这把密钥与数据库一同备份；丢失后已保存的服务商凭据无法恢复。

推荐在 `.env` 中设置 `PANEL_PORT=127.0.0.1:8080`，只通过 HTTPS 反向代理对外提供服务。包含 Caddy、Nginx、systemd、升级、备份和排障步骤的生产部署说明见 [docs/deployment.md](docs/deployment.md)。

MySQL：

```bash
docker compose up -d --build
```

SQLite：

```bash
docker compose -f compose.yaml -f compose.sqlite.yaml up -d --build
```

生产环境必须通过 Caddy、Traefik、Nginx 或既有入口代理提供 HTTPS，并允许 `/ws/console/` 的 WebSocket 升级以及 `/api/v1/events` 的 SSE 长连接。应用只监听 HTTP，并根据 `PUBLIC_ORIGIN` 执行同源检查。

## 健康检查

- `GET /health/live`：进程存活。
- `GET /health/ready`：数据库已连接且迁移完成。

## 数据库选择

`DATABASE_URL` 支持：

- `sqlite://data/controlpanel.db`
- `mysql://username:password@hostname:3306/database`

SQLite 与 MySQL 是部署时二选一，不会双写。跨数据库迁移必须使用应用级加密备份功能，不能复制 SQLite 文件或直接导入 MySQL。

## Mock 服务商

登录后进入“服务商”页面，创建 Mock 连接并选择服务器数量和控制台能力。连接创建后可执行“测试连接”和“立即同步”；同步任务由后台工作器处理，完成后服务器会出现在“服务器”页面。打开服务器详情即可执行符合能力要求的电源操作，或演示内嵌、新窗口、仅后台和完全不可用四种控制台组合。Mock 凭据仅用于验证加密存储和交互流程，不会访问外部服务。

`CREDENTIAL_KEYS` 可以用逗号配置多个版本，例如 `1:<旧密钥>,2:<新密钥>`；`CREDENTIAL_ACTIVE_KEY_VERSION` 指定新写入数据使用的版本。轮换期间保留仍被数据库记录引用的旧密钥。

## 服务商接入

AWS 连接使用客户 AWS 账户中的静态 IAM Access Key、Secret Access Key、可选 Session Token 和显式区域列表；不会读取宿主机环境变量、共享凭据文件或实例角色。VirtFusion 只使用客户区 `Account → API` 创建的 User API Token，地址填写面板根地址或 `/api`，不接受管理员 `/api/v1`。自托管面板若解析到私网地址，必须用 `PROVIDER_ALLOWED_PRIVATE_CIDRS` 显式放行所需的最小网段。

第三阶段新增 Google Cloud Compute Engine 与 Virtualizor。GCP 每个连接使用一份完整的服务账号 JSON 和显式项目 ID，支持跨区域清单、开机、关机、硬重置及 Google Cloud 官方资源页回退。Virtualizor 使用客户侧 Enduser API 的 API Key/API Password，支持清单、电源操作，以及通过本面板一次性 WebSocket 票据代理的内嵌或新窗口 VNC。详细权限、凭据保护、HTTPS 与私网放行要求见 [docs/providers.md](docs/providers.md)。

SolusVM 2 官方公开 API 目前要求管理员侧 management Token，不符合本项目的客户凭据策略，因此不在运行时服务商列表中；适配器源码暂时保留，等待有文档的客户 API。第三方 PVE 自研面板需要按各自协议实现独立适配器，接入方式见 [docs/provider-sdk.md](docs/provider-sdk.md)。

## 测试

```bash
make test
```

设置 `TEST_MYSQL_URL` 后运行 `make integration-mysql` 可验证 MySQL 迁移与仓储契约。
