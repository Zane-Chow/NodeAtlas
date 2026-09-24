# 部署指南

本文说明如何把 Server Control Panel 部署到一台 Linux 服务器。对于单用户、单实例场景，推荐使用 **Docker Compose + SQLite + Caddy/Nginx HTTPS 反向代理**：组件少、迁移方便，也符合当前产品的使用规模。如果已经有 MySQL 运维体系，也可以改用随项目提供的 MySQL 8.4 Compose 配置。

> 当前版本应只运行一个应用实例。不要对应用做水平扩容；SQLite 也不能由多个应用实例同时挂载。SQLite 与 MySQL 是二选一，不会双写。

## 1. 部署结构

推荐拓扑如下：

```text
互联网
  │  HTTPS :443
  ▼
Caddy / Nginx
  │  HTTP 127.0.0.1:8080
  ▼
Server Control Panel
  ├─ SQLite 数据卷，或 MySQL 容器
  ├─ 加密备份数据卷
  └─ 出站访问 AWS、GCP 和各第三方面板 API
```

公网只开放 `80/tcp` 和 `443/tcp`。应用的 `8080/tcp` 应绑定到回环地址，MySQL 不应暴露到公网。

## 2. 部署前准备

准备以下内容：

- 一台安装了 Docker Engine 和 Docker Compose v2 的 Linux 服务器。
- 一个指向该服务器的域名，例如 `panel.example.com`。
- 防火墙已允许公网访问 TCP 80、443。
- 服务器能够出站访问所配置服务商的 HTTPS API 和控制台端点。
- 已安装 `openssl`，用于生成随机密钥。

拉取代码并进入项目目录：

```bash
git clone <项目仓库地址> server-control-panel
cd server-control-panel
cp .env.example .env
chmod 600 .env
```

生成凭据加密主密钥：

```bash
openssl rand -base64 32
```

保存输出值，并在 `.env` 中写成：

```dotenv
CREDENTIAL_KEYS=1:<刚才生成的值>
CREDENTIAL_ACTIVE_KEY_VERSION=1
```

这把密钥用于加密数据库中的服务商凭据。**必须与数据库备份分开保存一份离线副本**；丢失后，已有服务商凭据无法解密。

## 3. 推荐方式：Docker Compose + SQLite

编辑 `.env`，至少确认以下配置：

```dotenv
APP_ENV=production
PANEL_PORT=127.0.0.1:8080
PUBLIC_ORIGIN=https://panel.example.com

ADMIN_USERNAME=
ADMIN_PASSWORD=

CREDENTIAL_KEYS=1:<Base64 编码的 32 字节密钥>
CREDENTIAL_ACTIVE_KEY_VERSION=1
BACKUP_DIRECTORY=/backups

CONSOLE_ALLOWED_PRIVATE_CIDRS=
PROVIDER_ALLOWED_PRIVATE_CIDRS=
```

把 `panel.example.com` 替换为真实域名。`PUBLIC_ORIGIN` 必须与浏览器最终访问的协议和域名完全一致；生产模式必须使用 `https://`，且不能带业务路径。

建议先将 `ADMIN_USERNAME` 和 `ADMIN_PASSWORD` 留空，首次打开页面时通过初始化向导创建唯一管理员。若希望启动时自动创建管理员，必须同时填写两项，密码至少 12 字节；只填写其中一项会导致应用拒绝启动。

启动 SQLite 版本：

```bash
docker compose -f compose.yaml -f compose.sqlite.yaml up -d --build
```

检查容器与应用状态：

```bash
docker compose -f compose.yaml -f compose.sqlite.yaml ps
docker compose -f compose.yaml -f compose.sqlite.yaml logs --tail=100 app
curl -fsS http://127.0.0.1:8080/health/live
curl -fsS http://127.0.0.1:8080/health/ready
```

两个健康检查均成功后，继续配置第 5 节的 HTTPS 反向代理。

> SQLite 部署后续执行 `logs`、`pull`、`up`、`down` 等 Compose 命令时，都要同时带上 `-f compose.yaml -f compose.sqlite.yaml`。

## 4. 可选方式：Docker Compose + MySQL

如果选择 MySQL，先生成两个仅包含十六进制字符的随机密码，避免连接 URL 中出现需要转义的特殊字符：

```bash
openssl rand -hex 24
openssl rand -hex 24
```

编辑 `.env`：

```dotenv
APP_ENV=production
PANEL_PORT=127.0.0.1:8080
PUBLIC_ORIGIN=https://panel.example.com

ADMIN_USERNAME=
ADMIN_PASSWORD=

CREDENTIAL_KEYS=1:<Base64 编码的 32 字节密钥>
CREDENTIAL_ACTIVE_KEY_VERSION=1
BACKUP_DIRECTORY=/backups

MYSQL_DATABASE=controlpanel
MYSQL_USER=controlpanel
MYSQL_PASSWORD=<第一个随机密码>
MYSQL_ROOT_PASSWORD=<第二个随机密码>

CONSOLE_ALLOWED_PRIVATE_CIDRS=
PROVIDER_ALLOWED_PRIVATE_CIDRS=
```

启动：

```bash
docker compose up -d --build
docker compose ps
docker compose logs --tail=100 app mysql
curl -fsS http://127.0.0.1:8080/health/ready
```

项目自带的 `compose.yaml` 会根据 `MYSQL_*` 变量构造容器内的 `DATABASE_URL`；此部署方式不使用 `.env` 中单独的 `DATABASE_URL` 行。

## 5. 配置 HTTPS 反向代理

### Caddy 示例

Caddy 会自动申请和续期 TLS 证书，并自动处理 WebSocket。将以下内容加入 Caddyfile：

```caddyfile
panel.example.com {
    encode zstd gzip
    reverse_proxy 127.0.0.1:8080
}
```

验证并重新加载 Caddy：

```bash
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

### Nginx 示例

在 `http` 配置块中加入 WebSocket 连接映射：

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
```

站点配置示例：

```nginx
server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name panel.example.com;

    ssl_certificate     /etc/letsencrypt/live/panel.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;

        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        proxy_buffering off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

检查并重新加载 Nginx：

```bash
sudo nginx -t
sudo systemctl reload nginx
```

反向代理必须满足以下条件：

- 保留请求的 `Host`，并正确传递外部 HTTPS 协议。
- 允许 `/ws/console/` 的 WebSocket Upgrade。
- 不缓冲 `/api/v1/events` 的 SSE 长连接。
- 长连接超时时间应高于一次控制台会话可能持续的时间。

完成后访问 `https://panel.example.com`，按向导创建管理员并登录。

## 6. 关键环境变量

| 变量 | 用途 | 生产建议 |
| --- | --- | --- |
| `APP_ENV` | 运行环境 | 使用 `production` |
| `PANEL_PORT` | Compose 发布到宿主机的地址和端口 | `127.0.0.1:8080` |
| `HTTP_ADDRESS` | 应用进程监听地址 | Compose 已固定为 `0.0.0.0:8080` |
| `PUBLIC_ORIGIN` | 浏览器看到的外部来源及同源校验基准 | 精确填写 `https://域名` |
| `DATABASE_URL` | 单文件部署的数据库连接 | Compose 会按所选配置覆盖它 |
| `CREDENTIAL_KEYS` | 服务商凭据加密主密钥 | `版本:Base64密钥`，可配置多个版本 |
| `CREDENTIAL_ACTIVE_KEY_VERSION` | 新写入凭据使用的密钥版本 | 必须存在于 `CREDENTIAL_KEYS` |
| `BACKUP_DIRECTORY` | 应用级加密备份目录 | Compose 使用 `/backups` |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | 首次启动管理员引导 | 同时填写或同时留空 |
| `PROVIDER_ALLOWED_PRIVATE_CIDRS` | 允许访问的私网服务商 API 网段 | 仅放行必要的最小私网段 |
| `CONSOLE_ALLOWED_PRIVATE_CIDRS` | 允许代理的私网控制台目标网段 | 仅放行必要的最小私网段 |

私网白名单使用逗号分隔，例如：

```dotenv
PROVIDER_ALLOWED_PRIVATE_CIDRS=10.20.30.0/24,192.168.50.10/32
CONSOLE_ALLOWED_PRIVATE_CIDRS=10.20.40.0/24
```

只接受 RFC1918 IPv4 或 ULA IPv6 私网。控制台校验会同时考虑这两项配置，因此不要放行过大的网段。公网 HTTPS 服务商无需加入白名单。

## 7. 使用单文件和 systemd 部署

不使用 Docker 时，需要 Go 1.27、Node.js 24、npm 11 和 `make`。构建过程会先运行前后端测试：

```bash
make build
./dist/controlpanel -version
```

在目标服务器创建专用用户和目录：

```bash
sudo useradd --system --home /var/lib/server-control-panel --shell /usr/sbin/nologin controlpanel
sudo install -d -o controlpanel -g controlpanel -m 0750 /var/lib/server-control-panel
sudo install -d -o controlpanel -g controlpanel -m 0700 /var/lib/server-control-panel/backups
sudo install -d -o root -g root -m 0755 /opt/server-control-panel
sudo install -m 0755 dist/controlpanel /opt/server-control-panel/controlpanel
```

创建 `/etc/server-control-panel.env`：

```dotenv
APP_ENV=production
HTTP_ADDRESS=127.0.0.1:8080
PUBLIC_ORIGIN=https://panel.example.com
DATABASE_URL=sqlite:///var/lib/server-control-panel/controlpanel.db
ADMIN_USERNAME=
ADMIN_PASSWORD=
CREDENTIAL_KEYS=1:<Base64 编码的 32 字节密钥>
CREDENTIAL_ACTIVE_KEY_VERSION=1
BACKUP_DIRECTORY=/var/lib/server-control-panel/backups
CONSOLE_ALLOWED_PRIVATE_CIDRS=
PROVIDER_ALLOWED_PRIVATE_CIDRS=
```

限制环境文件权限：

```bash
sudo chown root:root /etc/server-control-panel.env
sudo chmod 600 /etc/server-control-panel.env
```

创建 `/etc/systemd/system/server-control-panel.service`：

```ini
[Unit]
Description=Server Control Panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=controlpanel
Group=controlpanel
EnvironmentFile=/etc/server-control-panel.env
WorkingDirectory=/var/lib/server-control-panel
ExecStart=/opt/server-control-panel/controlpanel
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=/var/lib/server-control-panel

[Install]
WantedBy=multi-user.target
```

启动并检查：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now server-control-panel
sudo systemctl status server-control-panel
curl -fsS http://127.0.0.1:8080/health/ready
```

之后仍需按第 5 节配置 HTTPS 反向代理。

## 8. 备份与恢复

登录后，在“备份与设置”中创建应用级加密备份。备份口令至少 12 字符，建议将下载的归档保存到另一台机器或对象存储。

完整灾难恢复至少需要同时保留：

1. 应用创建的加密备份归档及其备份口令。
2. 当前和历史 `CREDENTIAL_KEYS`。
3. `.env` 或等价的部署配置。

应用级备份可用于 SQLite 与 MySQL 之间迁移。不要通过复制 SQLite 文件或直接导入表的方式跨数据库迁移。详细恢复行为见 [backups.md](backups.md)。

轮换凭据主密钥时，先追加新版本并切换活动版本：

```dotenv
CREDENTIAL_KEYS=1:<旧密钥>,2:<新密钥>
CREDENTIAL_ACTIVE_KEY_VERSION=2
```

不要立即删除旧密钥；数据库中仍可能存在由旧版本加密的凭据。

## 9. 升级

升级前先创建并下载应用级加密备份，同时确认 `CREDENTIAL_KEYS` 已离线保存。

MySQL Compose 部署：

```bash
git pull --ff-only
docker compose build --pull
docker compose up -d
docker compose logs --tail=100 app
curl -fsS http://127.0.0.1:8080/health/ready
```

SQLite Compose 部署：

```bash
git pull --ff-only
docker compose -f compose.yaml -f compose.sqlite.yaml build --pull
docker compose -f compose.yaml -f compose.sqlite.yaml up -d
docker compose -f compose.yaml -f compose.sqlite.yaml logs --tail=100 app
curl -fsS http://127.0.0.1:8080/health/ready
```

数据库迁移会在应用监听端口前自动运行。不要执行 `docker compose down -v`，该命令会删除数据库和备份数据卷。

单文件部署升级时，先构建新二进制，再停止服务、替换 `/opt/server-control-panel/controlpanel`，最后启动服务并检查 `/health/ready`。

## 10. 常见问题

### 应用无法启动，提示 `CREDENTIAL_KEYS is required`

检查 `.env` 是否存在，以及 `CREDENTIAL_KEYS` 是否采用 `版本:Base64值` 格式。解码后的密钥必须正好为 32 字节，最简单的生成方式是 `openssl rand -base64 32`。

### 应用无法启动，提示生产环境必须使用 HTTPS

当 `APP_ENV=production` 时，`PUBLIC_ORIGIN` 必须以 `https://` 开头。应用自身仍监听 HTTP，由同机反向代理终止 TLS。

### 浏览器登录后又回到登录页，或请求被拒绝

确认浏览器访问地址与 `PUBLIC_ORIGIN` 完全一致，并确认反向代理传递了 `Host` 和 `X-Forwarded-Proto`。不要通过服务器 IP 或额外域名混用登录。

### 页面能打开，但实时状态或控制台不工作

检查反向代理是否支持 WebSocket Upgrade、是否关闭 SSE 缓冲，以及长连接超时是否足够。然后查看：

```bash
docker compose logs -f --tail=200 app
```

SQLite 部署需在命令中同时指定两个 Compose 文件。

### 自托管 VirtFusion、Virtualizor 或 SolusVM 2 连接测试失败

如果面板域名解析到私网地址，把实际需要访问的最小网段加入 `PROVIDER_ALLOWED_PRIVATE_CIDRS`。如果 VNC/WSS 目标也位于私网，再配置 `CONSOLE_ALLOWED_PRIVATE_CIDRS`。不要为了省事放行整个企业网络。

### `/health/live` 正常，但 `/health/ready` 返回 503

进程还活着，但数据库连接或初始化尚未就绪。查看应用日志；MySQL 部署还应查看 `mysql` 容器健康状态、账号密码和数据卷权限。

## 11. 上线检查清单

- `PUBLIC_ORIGIN` 使用唯一、正确的 HTTPS 域名。
- 公网只能访问 80/443，8080 仅绑定 `127.0.0.1`。
- MySQL 没有发布到宿主机公网端口。
- `.env` 权限为 600，未提交到 Git。
- `CREDENTIAL_KEYS` 已离线备份，旧版本密钥未被提前删除。
- 管理员密码唯一且足够长。
- `/health/live` 和 `/health/ready` 均成功。
- WebSocket 控制台和 SSE 实时状态已通过反向代理验证。
- 已创建一次应用级加密备份，并在另一位置验证可下载保存。

