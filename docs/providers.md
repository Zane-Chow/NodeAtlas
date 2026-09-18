# AWS 与 VirtFusion 服务商接入

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
