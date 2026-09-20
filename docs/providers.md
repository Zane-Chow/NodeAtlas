# 服务商接入

## AWS EC2

每个 AWS 连接使用独立的静态凭据和一个或多个显式区域。面板不会回退到环境变量、`~/.aws`、ECS/EKS 或 EC2 实例角色。建议为专用 IAM 身份仅授予目标资源所需的以下操作：

- `ec2:DescribeInstances`
- `ec2:StartInstances`
- `ec2:StopInstances`
- `ec2:RebootInstances`

资源范围和条件应按账户策略尽量收紧。AWS 浏览器串行控制台还依赖账户级启用、兼容实例、额外的 `ec2-instance-connect:SendSerialConsoleSSHPublicKey` 权限以及有效的 AWS Console 浏览器会话，因此当前不会伪造临时串行链接；界面会回退到对应区域和实例的 AWS EC2 官方页面。

## VirtFusion

连接地址填写控制面板根地址，例如 `https://panel.example.com`；也接受以 `/api/v1` 结尾的地址。面板调用 API v1 的 `/connect`、`/servers`、服务器详情、电源操作和 VNC 端点。Token 应只具有读取服务器、执行电源操作和读取 VNC 临时连接信息所需的权限。

生产环境只接受 HTTPS。若面板 DNS 或地址位于 RFC1918/ULA 私网，使用逗号分隔的 `PROVIDER_ALLOWED_PRIVATE_CIDRS` 放行最小必要网段，例如：

```env
PROVIDER_ALLOWED_PRIVATE_CIDRS=10.30.4.0/24,fd10:20::/64
```

地址在保存连接、每次建立 TCP 连接及跟随重定向时都会重新校验；跨源重定向、协议降级、环回、链路本地和未放行私网地址会被拒绝。

VirtFusion 6.1 及以上版本可返回短期 VNC WebSocket 信息。上游 WSS URL 只保存在后端一次性内存目标中，浏览器通过本面板的一次性票据连接 noVNC。临时 VNC 密码只出现在当前已认证会话的内存响应中，不写入数据库、审计日志、URL 或浏览器存储。内嵌与新标签页模式均使用同一安全代理；不支持时仍可打开服务商面板。

## Google Cloud Compute Engine

每个 GCP 连接填写一个显式项目 ID，并提交一份完整的服务账号 JSON。面板只使用该连接保存的 JSON 创建客户端，不会回退到 Application Default Credentials、宿主机环境变量、元数据服务、共享文件、用户 OAuth 或其他连接的身份。服务账号可以访问与其 JSON 中 `project_id` 不同的项目，但必须在界面中填写实际要管理的目标项目 ID。

建议为面板创建专用、最小权限的 IAM 角色，仅授予目标项目或资源所需的以下权限：

- `compute.instances.list`
- `compute.instances.get`
- `compute.instances.start`
- `compute.instances.stop`
- `compute.instances.reset`

服务账号密钥属于长期凭据。应使用专用账号、按最小权限授权、妥善保护和定期轮换；不要把 JSON 保存到源码、镜像、日志或共享目录。删除或禁用旧密钥前，应先在面板中更新连接并验证新密钥可用。

服务器清单通过 Compute Engine 聚合实例列表读取，无需手工维护区域。面板中的“重启”会调用 Compute Engine `reset`，这是硬重置，不是操作系统内的正常重启；执行前应确认应用和文件系统能够承受突然复位。当前不代理 GCP 串行控制台，因为它还依赖实例配置、额外 IAM 权限和有效的 Google Cloud Console 浏览器会话。控制台入口会安全回退到该项目、区域和实例的 Google Cloud 官方资源页，且链接不含服务账号凭据。

## Virtualizor

Virtualizor 连接使用客户侧 Enduser API，不使用管理员 API。先在服务商的 Virtualizor 客户面板中创建或获取 Enduser API Key 与 API Password，再在本面板中填写 HTTPS 面板根地址和这两个凭据。凭据字段为只写：保存后不会通过连接 API 或页面回显。

生产连接必须使用 HTTPS。若自托管面板或 VNC 地址解析到 RFC1918/ULA 私网，应使用 `PROVIDER_ALLOWED_PRIVATE_CIDRS` 仅放行实际需要的最小网段，例如：

```env
PROVIDER_ALLOWED_PRIVATE_CIDRS=10.30.4.0/24,fd10:20::/64
```

保存前请确认当前 Enduser API 账号具备以下能力，并确认服务商已为目标 VPS 启用 VNC：

- `listvs` 可列出账号下 VPS；空账号也可以通过连接测试。
- `vpsmanage` 可打开不含 API 凭据的服务商 VPS 管理页。
- `start`、`stop` 和 `restart` 可执行对应电源操作。
- VNC Info 可返回目标 VPS 当前有效的 VNC 地址、端口和临时密码。

Virtualizor 协议要求 `apikey` 与 `apipass` 出现在发往上游的查询字符串中。本应用只在受保护的后端请求中按协议发送它们，并在其他所有位置进行隐藏：不会把包含凭据的上游 URL 写入日志、错误、审计、服务器清单或浏览器响应。服务商后台回退链接只包含面板地址、`vpsmanage` 和 VPS ID，不含凭据。

可用的原始 VNC 连接由后端通过一次性、本地同源 WebSocket 票据代理。上游 IP、端口和临时密码只存在于短期内存会话中；浏览器仅连接本面板的 WebSocket。内嵌与新窗口 noVNC 使用相同机制。VNC 不可用时，两种 VNC 入口都会禁用，但不含凭据的 Virtualizor 管理页仍可打开。私网 VNC 目标同样必须落在 `PROVIDER_ALLOWED_PRIVATE_CIDRS` 的允许范围内。
