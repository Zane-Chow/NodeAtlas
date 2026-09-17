# Server Control Panel

单用户、多服务商服务器控制面板。当前里程碑提供安全初始化、登录、SQLite/MySQL 双数据库基础和内嵌式 React 运维界面。

## 单文件运行

需要 Go 1.27、Node.js 24 和 npm 11：

```bash
make build
DATABASE_URL=sqlite://data/controlpanel.db PUBLIC_ORIGIN=http://127.0.0.1:8080 ./dist/controlpanel
```

首次打开 `http://127.0.0.1:8080` 后，通过设置向导创建唯一管理员。也可以在第一次启动前同时设置 `ADMIN_USERNAME` 与 `ADMIN_PASSWORD`；只设置其中一个会拒绝启动，已有管理员不会被覆盖。

## Docker Compose

复制 `.env.example` 为 `.env`，替换所有示例密码并设置真实的 HTTPS `PUBLIC_ORIGIN`。

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

## 测试

```bash
make test
```

设置 `TEST_MYSQL_URL` 后运行 `make integration-mysql` 可验证 MySQL 迁移与仓储契约。
