# Server Control Panel

单用户、多服务商服务器控制面板。当前里程碑已经提供安全初始化、登录、SQLite/MySQL 双数据库基础、多个服务商连接、统一服务器清单，以及安全的开机、关机和重启流程。现阶段使用 Mock 服务商验证完整流程，下一阶段将实现控制台与备份，再接入 AWS 与 VirtFusion。

## 当前功能

- 创建多个独立的服务商连接，并单独测试连接与触发同步。
- 将不同连接下的服务器汇总到统一清单，支持按名称、区域、状态和连接筛选。
- 服务商凭据使用 AES-256-GCM 加密后入库，密钥支持版本轮换。
- 后台任务持久化、租约执行和失败重试；同步失败不会覆盖上一次成功清单。
- 根据服务商能力和服务器状态启用开机、关机、重启按钮，所有操作均需二次确认。
- 电源操作具有幂等保护、单服务器互斥、远端状态复核、操作历史和实时界面更新。
- VNC/串行控制台、服务商后台回退与加密备份仍在下一里程碑中，当前按钮保持禁用。

电源操作的状态、重试和反向代理要求详见 [docs/operations.md](docs/operations.md)。

## 单文件运行

需要 Go 1.27、Node.js 24 和 npm 11：

```bash
make build
export CREDENTIAL_KEYS="1:$(openssl rand -base64 32)"
export CREDENTIAL_ACTIVE_KEY_VERSION=1
DATABASE_URL=sqlite://data/controlpanel.db PUBLIC_ORIGIN=http://127.0.0.1:8080 ./dist/controlpanel
```

首次打开 `http://127.0.0.1:8080` 后，通过设置向导创建唯一管理员。也可以在第一次启动前同时设置 `ADMIN_USERNAME` 与 `ADMIN_PASSWORD`；只设置其中一个会拒绝启动，已有管理员不会被覆盖。

## Docker Compose

复制 `.env.example` 为 `.env`，替换所有示例密码并设置真实的 HTTPS `PUBLIC_ORIGIN`。使用 `openssl rand -base64 32` 生成 32 字节凭据加密密钥，填入 `CREDENTIAL_KEYS` 的版本前缀后面，例如 `1:<生成值>`。请把这把密钥与数据库一同备份；丢失后已保存的服务商凭据无法恢复。

MySQL：

```bash
docker compose up -d --build
```

SQLite：

```bash
docker compose -f compose.yaml -f compose.sqlite.yaml up -d --build
```

生产环境必须通过 Caddy、Traefik、Nginx 或既有入口代理提供 HTTPS。应用只监听 HTTP，并根据 `PUBLIC_ORIGIN` 执行同源检查。

## 健康检查

- `GET /health/live`：进程存活。
- `GET /health/ready`：数据库已连接且迁移完成。

## 数据库选择

`DATABASE_URL` 支持：

- `sqlite://data/controlpanel.db`
- `mysql://username:password@hostname:3306/database`

SQLite 与 MySQL 是部署时二选一，不会双写。未来跨数据库迁移必须使用应用级加密备份功能，不能复制 SQLite 文件或直接导入 MySQL。

## Mock 服务商

登录后进入“服务商”页面，创建 Mock 连接并选择服务器数量和控制台能力。连接创建后可执行“测试连接”和“立即同步”；同步任务由后台工作器处理，完成后服务器会出现在“服务器”页面。打开服务器详情即可对符合状态与能力要求的服务器执行电源操作，并在“操作记录”页面查看结果。Mock 凭据仅用于验证加密存储和交互流程，不会访问外部服务。

`CREDENTIAL_KEYS` 可以用逗号配置多个版本，例如 `1:<旧密钥>,2:<新密钥>`；`CREDENTIAL_ACTIVE_KEY_VERSION` 指定新写入数据使用的版本。轮换期间保留仍被数据库记录引用的旧密钥。

## 测试

```bash
make test
```

设置 `TEST_MYSQL_URL` 后运行 `make integration-mysql` 可验证 MySQL 迁移与仓储契约。
