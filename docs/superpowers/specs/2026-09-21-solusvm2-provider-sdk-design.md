# SolusVM 2 与第三方 PVE Provider SDK 设计

**状态：Approved**

## 1. 目标

第四阶段为现有单用户服务器控制面板增加 SolusVM 2，并完成第三方 PVE 自研面板的稳定扩展入口：

- 同一种 `solusvm2` 服务商可创建任意多个独立连接。
- 通过 SolusVM 2 官方 REST API 同步服务器、查询状态并执行开机、正常关机和正常重启。
- 可用时通过本面板的一次性 WebSocket 票据提供内嵌或新窗口 noVNC；不可用时回退到不含凭据的服务商面板。
- 提供 Provider 契约测试工具、可编译的第三方 PVE 适配器骨架和接入文档。
- 保持现有 AWS、GCP、VirtFusion、Virtualizor、Mock 行为不变。

不实现 SolusVM 1，不直接接入 Proxmox VE 原生 API，也不假设不同第三方 PVE 面板共享 API。第三方适配器仍需编译进应用，不提供运行时加载任意二进制插件的能力。

## 2. SolusVM 2 连接模型

Provider ID 固定为 `solusvm2`，显示名为 `SolusVM 2`。

每个连接包含：

- `endpoint`：SolusVM 2 management node 的 HTTPS 根地址；允许用户输入根地址或规范的 `/api/v1` 后缀，内部统一为 API 根地址。
- `credentials.api_token`：SolusVM 2 UI 生成的 API Token，只写、加密保存，不回显。
- `settings`：当前必须是空对象；未知字段一律拒绝，为以后显式版本演进保留空间。

认证只使用 `Authorization: Bearer <token>`。Token 不得出现在 URL、服务器清单、错误文本、日志、审计、浏览器存储或生成资源中。

## 3. API 与服务器映射

适配器位于 `internal/providers/solusvm2`，实现现有 `providers.Factory` 与 `providers.Provider`。

使用以下官方 API v1 端点：

- `GET /servers?page=N`：连接验证与分页服务器清单。
- `GET /servers/{id}`：单台服务器及操作后的状态确认。
- `POST /servers/{id}/start`：开机。
- `POST /servers/{id}/stop`，请求体 `{"force":false}`：正常关机。
- `POST /servers/{id}/restart`，请求体 `{"force":false}`：正常重启。
- `POST /servers/{id}/vnc_up`：获取短期 VNC 主机、端口与服务器返回的 VNC 密码。

服务器 ID 必须是规范的正整数。分页 cursor 只保存下一页正整数，依据 `meta.current_page` 与 `meta.last_page` 生成；畸形、倒退或不一致的分页元数据必须失败，不能静默循环。

状态映射规则：

- `is_suspended=true` 优先映射为 `suspended`。
- `is_processing=true`、`status=processing` 或 `real_status=processing` 映射为 `pending`。
- 优先使用 `real_status`，缺失时使用 `status`：`started` → `running`，`stopped` → `stopped`，`not exist` → `error`，`unavailable` 或未知值 → `unknown`。

清单只保存明确挑选的安全字段：ID、名称、项目范围、虚拟化类型、OS 类型、vCPU、RAM、磁盘以及规范化 IP 地址。不得把完整 `settings`、`vnc_password`、`vnc_url`、用户信息或原始响应写入 `Spec`、`Addresses` 或 `RemoteState`。

电源操作返回 SolusVM task ID 作为 `ActionReceipt.RequestID`。能力依据规范化状态、挂起状态和 `settings.vnc_enabled` 计算；挂起服务器不提供电源或 VNC 操作。

## 4. HTTP 与出站安全

SolusVM 2 复用 `internal/providers/network`：

- API 端点必须使用 HTTPS。
- 禁止 userinfo、query、fragment、非规范路径和非法端口。
- 保存连接、DNS 解析、每次拨号和每次重定向都执行地址策略。
- 环回、链路本地及未显式放行的 RFC1918/ULA 地址被拒绝；私网管理节点通过 `PROVIDER_ALLOWED_PRIVATE_CIDRS` 放行最小网段。
- 只允许同源 HTTPS 重定向，设置严格请求超时与响应大小上限。
- 不支持跳过 TLS 校验。

错误必须归一化且脱敏：401 → authentication，403 → permission，404 → not found，409/422 → provider state/config conflict，429 → retryable rate limit，网络/超时 → retryable network，5xx → retryable provider error。不得返回上游响应体、Token、VNC 密码或目标地址。

## 5. VNC 信任边界

SolusVM 2 的浏览器控制台不是 raw RFB 直连。官方故障说明展示的传输为 management node 上的 `wss://<panel>/vnc?url=<compute-host>:<proxy-port>/<vm-uuid>`。因此：

- `vnc_up` 返回的 host/port 必须先按 provider 网络策略解析和校验。
- `vm.uuid` 必须是规范 UUID；`vm.settings.vnc_password` 只作为当前短期 `ConsoleTarget.Password` 使用。
- 后端以已经验证的 management node origin 构造 `/vnc` WSS URL，并用 URL 编码器生成唯一的 `url=host:port/uuid` 参数；不得接受上游提供的任意 WebSocket origin。
- 官方 release notes 提到的 `vnc_proxy_url` 缺少足够公开 schema 来证明其传输语义；当前实现不使用该字段作为 transport target，也不得让它绕过上述构造与校验。

构造后的 WSS 目标再经过中央 console target policy，并由现有一次性 ticket 代理。SolusVM 2 不加入 raw `vnc+tcp` allowlist；该 allowlist 仍只接受数据库保存的 provider type 精确等于 `virtualizor`。大小写变体、前后缀相似值及未来未登记 Provider 即使返回 `vnc+tcp` 也必须被拒绝。

浏览器只得到同源 WebSocket ticket、`rfb` 协议和当前会话所需的密码；永远不得到上游 host/port 或 API Token。内嵌与新窗口均复用本地 noVNC 代理。VNC 不可用时两个 VNC 按钮禁用，但服务商面板入口仍保持可用。

服务商面板回退使用连接 HTTPS origin 的根页面，不携带 Token、服务器 ID 查询参数或其他秘密，避免假设管理员区与客户区具有相同深链路。

## 6. 前端

连接表单增加 SolusVM 2：

- HTTPS management node 地址。
- API Token 密码框，`autocomplete=new-password`。
- 与其他自托管面板一致的 HTTPS/私网 CIDR 提示。
- 提交 payload 为 `{ endpoint, settings: {}, credentials: { api_token } }`。

保存成功后表单卸载，Token 不回显。服务器页将 `solusvm2` 纳入本地 noVNC 新窗口路径；控制台不可用时遵循现有能力原因和 provider portal 回退，不添加 SolusVM 专属浏览器秘密字段。

## 7. 第三方 PVE Provider SDK

新增：

- `internal/providers/contracttest`：供适配器测试调用的契约断言入口，检查分页、唯一稳定 ID、规范状态、合法 JSON、能力一致性、单机查询和安全错误接口。
- `examples/providers/pvepanel`：可编译但不注册的示例骨架，展示 Factory、严格配置解析、Provider 全方法、编译期接口断言和 fixture 测试接入方式。
- `docs/provider-sdk.md`：说明适配器边界、注册、配置/凭据分离、网络策略、错误归一化、控制台三层回退、前端表单、契约测试和发布检查清单。

示例不连接真实 PVE，也不包含通用 PVE 凭据模型。每家第三方自研面板必须使用唯一 provider ID、独立包和自己的 API fixture；不能在 inventory、operations 或 console 核心域中加入厂商响应解析。

Raw TCP/WSS 控制台属于高信任能力。新适配器默认不能使用 `vnc+tcp`；需要代码审查后在中央 allowlist 中显式加入准确 provider ID，并证明上游目标经过网络策略。WSS 目标必须由适配器从受信 endpoint 与严格校验的数据构造，并继续经过中央策略。

## 8. 测试与验收

必须覆盖：

- Endpoint、空/未知 settings、Bearer Token 及凭据脱敏。
- 官方形状的分页、状态、规格、IPv4/IPv6 与 task ID。
- start/stop/restart 的方法、路径及 `force=false` 请求体。
- 401/403/404/409/422/429/5xx、超时和畸形 JSON 的安全映射。
- `vnc_enabled=false`、挂起服务器、畸形 VNC host/port/UUID、私网策略和密码不落盘。
- SolusVM 2 只产生受验证的 management-node WSS 目标；raw VNC 仍仅允许精确 `virtualizor`，所有相似 ID 与 `solusvm2` raw target 均被拒绝。
- SolusVM 2 表单 payload、Token 不回显、本地新窗口 noVNC 与 portal 回退。
- PVE 示例编译，契约测试工具自身及 SolusVM 2 fixture 均通过。
- 全量 Go、前端 test/lint/build、嵌入资源同步和生产 Docker 构建通过；无 8080 监听或验证容器/镜像残留。

## 9. 官方依据

- SolusVM 2 OpenAPI：`https://installer.dev.solusvm.com/admin-docs.json`
- SolusVM 2 API 文档：`https://docs.solusvm.com/en/solusvm2/api-reference/api/`
- API Token 创建说明：`https://docs.solusvm.com/en/solusvm2/billing-integration-guide/prepaid-billing/installation-and-configuration/initial-configuration/`
- VNC WSS 路径说明：`https://support.solusvm.com/hc/en-us/articles/13266442487831-VNC-console-does-not-work-in-SolusVM-2-Connection-failed-Unexpected-response-code-502`
- SolusVM 2 release notes：`https://docs.solusvm.com/en/release-notes/`
