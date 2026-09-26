# 控制台与服务商后台

服务器详情按能力显示三层入口：面板内嵌控制台、新窗口临时控制台、服务商后台。每层独立显示，管理员可以直接选择；上层不可用时仍可使用服务商提供的下一层入口。

## 内嵌会话安全

创建内嵌会话时，后端向 Provider 请求临时目标并执行地址策略校验。上游目标 URL 和令牌只保存在后端进程内存，不写入数据库或返回给浏览器。noVNC 所需的临时密码只返回当前已认证会话并停留在浏览器内存中，不进入 URL、本地存储或会话存储。数据库只保存随机票据的 SHA-256 哈希、服务器、生命周期和结果。

浏览器获得的票据有效期为 60 秒，并在 WebSocket 握手时原子消费。握手失败或进程重启后不能重新使用。会话默认空闲 5 分钟关闭、最长运行 30 分钟；面板不记录 VNC、串行输入输出或终端内容。

外部目标规则：

- 内嵌代理接受经过校验的 `wss`；后端还可为 Virtualizor 原始 VNC 创建内部专用的 `vnc+tcp` 目标。`vnc+tcp` 只允许用于已识别为 Virtualizor 的连接，且同样执行主机、端口、DNS 和私网 CIDR 策略校验；它不会作为 URL 返回浏览器。Mock Provider 使用不访问网络的进程内测试传输。
- 新窗口与服务商后台仅接受 `https`。
- 拒绝 URL userinfo、fragment、无主机目标、loopback、链路本地、多播、未指定地址、云元数据地址和未授权私网地址。
- 中央 console 策略使用 `CONSOLE_ALLOWED_PRIVATE_CIDRS` 与 `PROVIDER_ALLOWED_PRIVATE_CIDRS` 的并集。为自托管 provider 放行的网段也会允许同网段的 WSS/HTTPS 控制台目标，无需在两处重复配置；`CONSOLE_ALLOWED_PRIVATE_CIDRS` 用于额外仅供控制台使用的网段，不会扩大 provider API 的权限。Virtualizor 的私有原始 VNC 目标仍需通过 provider 自身的 `PROVIDER_ALLOWED_PRIVATE_CIDRS` 校验。两项配置接受逗号分隔的 RFC1918/ULA CIDR；provider 放行也会扩大中央 console 的允许范围，务必限定为最小必要网段。

WSS 在创建票据和实际连接时都会执行目标校验，拨号时重新解析并检查全部 DNS 结果，只连接允许的 IP，同时保留原主机名用于 TLS 证书校验与 SNI。WSS 不使用环境代理，不跳过 TLS 校验，也不跟随重定向；服务商必须返回实际可连接的 WSS 端点。

无论上游使用 `wss` 还是内部 `vnc+tcp`，浏览器都只连接同源 `/ws/console/` 一次性票据，不会看到上游 IP、端口或供应商凭据。原始 VNC 的 TCP 字节与 WebSocket 二进制帧在网关中直接转发，不写入 React 状态、数据库、审计或应用日志。

## SolusVM 2 VNC（保留实现，运行时未启用）

以下内容记录保留适配器的安全边界。由于官方接口使用 management API Token，不符合当前“仅客户账户凭据”策略，该适配器不会在生产运行时注册，也不会出现在新增服务商界面。

SolusVM 2 使用配置的 management node origin 上的 WSS `/vnc` 代理，内嵌和新窗口均复用本地 noVNC 与一次性票据。后端从 `vnc_up` 响应中严格校验 compute host、端口、VM 身份和密码；compute host 的所有 DNS 结果均须通过 provider 网络策略，再选定允许的字面 IP 构造上游目标，避免管理节点二次解析主机名。多地址选择排序后的第一个 IP，不自动切换地址。上游 URL 只在服务端内存中存在，API Token 和 VNC 密码都不放入其查询参数。

私网 API management node 和 compute host 使用 `PROVIDER_ALLOWED_PRIVATE_CIDRS`；该列表已并入中央 console 策略，同网段的 management node WSS 无需再配置 `CONSOLE_ALLOWED_PRIVATE_CIDRS`。仅在 console 列表放行不能替代 provider 对 API 和 compute host 的校验。SolusVM 2 不使用也不允许 `vnc+tcp`，不依赖 `vnc_proxy_url`，不能用上游响应覆盖已验证的面板 origin。

VNC 密码只通过已认证的会话创建响应交给当前浏览器控制台，在内存中供 noVNC 使用，不落盘、不进入日志或浏览器存储。60 秒票据期限与 5 分钟空闲/30 分钟最长会话限制由本应用执行；上游密码由服务商管理，本应用不承诺在相同期限内撤销或轮换它。VNC 不可用时仍可打开连接的 HTTPS origin 根页面；后台链接没有凭据，也不假设特定版本的服务器深链路。

## 反向代理

`/ws/console/` 必须允许 WebSocket `Upgrade`，关闭响应缓冲，并将读取/空闲超时设为大于计划的会话时限。必须原样传递同源 Session Cookie；该路径不能匿名暴露，也不能配置公共缓存。

新窗口和后台 URL 只能由后端根据 Provider 返回值生成和验证。前端不接受管理员输入任意跳转或代理目标，并使用 `noopener,noreferrer` 打开外部页面。
