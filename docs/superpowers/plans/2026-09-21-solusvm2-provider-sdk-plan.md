# SolusVM 2 与 Provider SDK 实施计划

**Goal:** 完成第四阶段 SolusVM 2 真实适配器，并交付第三方 PVE 自研面板的文档化 Provider SDK 扩展入口。

**Architecture:** SolusVM 2 使用现有共享 provider 网络策略和统一 Provider 契约；Bearer Token 与所有厂商响应仅留在适配器内。SolusVM 2 VNC 使用 management node 的受验证 WSS `/vnc` 代理并继续由中央一次性 ticket 承载；现有 raw TCP allowlist 不扩大。PVE 扩展以编译期 Go 接口、契约测试工具、示例骨架和接入文档提供，不引入运行时插件或统一 PVE API 假设。

**Spec:** `docs/superpowers/specs/2026-09-21-solusvm2-provider-sdk-design.md`

## Task 1: Provider 契约测试工具与 PVE 示例骨架

**Files:**

- Create: `internal/providers/contracttest/contract.go`
- Create: `internal/providers/contracttest/contract_test.go`
- Create: `examples/providers/pvepanel/provider.go`
- Create: `examples/providers/pvepanel/provider_test.go`
- Create: `docs/provider-sdk.md`

1. 先写失败测试，定义 fixture 驱动的契约入口：连接信息非空、分页终止、服务器 ID 唯一稳定、状态在规范枚举内、Spec/Addresses 为合法 JSON、GetServer 与清单身份一致、能力结构完整、安全错误实现 `SafeMessage`/retryable 语义。
2. 实现最小 `contracttest.Run`，设置页数/服务器数上限，失败信息不得打印凭据或完整上游 payload。
3. 创建不注册的 `pvepanel` 示例：严格解析假想 endpoint/token、通过注入 client 展示边界，并用内存 fixture 通过契约测试。示例不得声称兼容真实 PVE。
4. 编写 SDK 文档，说明复制/重命名示例、实现接口、注册 provider、增加表单、网络/console 安全审核及测试清单。
5. 运行 `go test ./internal/providers/contracttest ./examples/providers/pvepanel -count=1`。
6. Commit: `feat: add provider adapter SDK contract`

## Task 2: SolusVM 2 Provider 适配器

**Files:**

- Create: `internal/providers/solusvm2/provider.go`
- Create: `internal/providers/solusvm2/provider_test.go`
- Create: `internal/providers/solusvm2/power_flow_test.go`

1. 先以 TLS fixture 写失败测试，覆盖 endpoint/token/settings 校验、Bearer header、不泄露 token、响应上限、同源重定向和私网/DNS 策略。
2. 写官方 OpenAPI 形状的分页与详情 fixture，覆盖 `status`/`real_status`/processing/suspended、规格、安全 IP 映射和分页 cursor 防循环。
3. 实现 Factory 与 bounded JSON client，复用 `providers/network`，不允许 TLS skip 或跨源重定向。
4. 实现 Validate/List/Get；只选择安全字段，明确证明 `settings.vnc_password`、`vnc_url` 和用户对象不会进入 inventory JSON。
5. 先写失败测试再实现 start、`force=false` stop/restart 与 task receipt；用操作执行器 fixture 验证请求实际发出且轮询最终状态。
6. 覆盖安全错误归一化与 malformed JSON/content-type/HTTP 状态。
7. 用 Task 1 契约入口跑 SolusVM 2 fixture。
8. 运行 `go test ./internal/providers/solusvm2 ./internal/providers/contracttest -count=1`。
9. Commit: `feat: add SolusVM 2 provider`

## Task 3: SolusVM 2 VNC 与中央 raw-target 授权

**Files:**

- Modify: `internal/providers/solusvm2/provider.go`
- Modify: `internal/providers/solusvm2/provider_test.go`
- Modify: `internal/console/service.go`
- Modify: `internal/console/service_test.go`
- Modify: `internal/console/websocket_test.go`

1. 先写失败测试：`vnc_enabled=true` 且未挂起时调用 `vnc_up`；严格校验 host/port、`vm.uuid` 和密码，从已验证 panel origin 构造 `wss://panel/vnc?url=host:port/uuid`；disabled/suspended/畸形/未放行私网全部安全失败。
2. 实现 SolusVM 2 OpenConsole，两种模式均返回 `rfb` WSS target；明确忽略不能作为 transport target 的 `vnc_proxy_url`；ProviderPortalURL 只返回面板 HTTPS origin。
3. 保持中央 raw VNC allowlist 只接受数据库连接类型精确 `virtualizor`，增加测试证明 `solusvm2` 返回 raw target 会在 ticket 持久化前被拒绝。
4. 测试大小写、前后缀和未来 provider ID 均拒绝；验证 WSS target 仍经过中央策略，浏览器/session payload 无上游 host/port/token，密码不写数据库与审计。
5. 运行 `go test ./internal/providers/solusvm2 ./internal/console -count=1`。
6. Commit: `feat: proxy SolusVM 2 VNC consoles`

## Task 4: 注册、元数据与组合测试

**Files:**

- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/connections/http.go`
- Modify: `internal/connections/http_test.go`

1. 先写失败测试，要求 `/provider-types` 返回 `{id:"solusvm2",name:"SolusVM 2"}`，排序稳定且认证边界不变。
2. 在 app composition 中使用 `AllowedPrivateCIDRs` 注册 `solusvm2.NewFactory`。
3. 组合测试证明多个 SolusVM 2 连接可独立创建/解密/构造，且 raw VNC 判断使用数据库 provider type。
4. 运行 `go test ./internal/app ./internal/connections ./internal/providers/... -count=1`。
5. Commit: `feat: register SolusVM 2 provider`

## Task 5: SolusVM 2 前端连接与控制台

**Files:**

- Modify: `web/src/connections/ConnectionsPage.tsx`
- Modify: `web/src/connections/ConnectionsPage.test.tsx`
- Modify: `web/src/servers/ServersPage.tsx`
- Modify: `web/src/servers/ServersPage.test.tsx`
- Modify: `web/src/console/api.test.ts`

1. 先写失败测试，要求表单提交 `{provider_type:"solusvm2",endpoint,settings:{},credentials:{api_token}}`，Token 控件为 password/new-password，成功后不再渲染秘密值。
2. 增加 endpoint、API Token 和 HTTPS/私网提示，不把 Token 放入连接类型或页面状态的持久层。
3. 将 `solusvm2` 加入本地 noVNC 新窗口路径；验证不可用时两个 VNC 按钮禁用而 provider portal 可用。
4. 验证 popout 消息与 `ConsoleSession` 无上游 host、port、API Token 字段。
5. 运行聚焦 Vitest，再运行 `npm --prefix web test -- --run`、lint、build。
6. Commit: `feat: add SolusVM 2 controls`

## Task 6: 文档、生成资源与最终验证

**Files:**

- Modify: `README.md`
- Modify: `docs/providers.md`
- Modify: `docs/console.md`
- Modify: `docs/provider-sdk.md`
- Modify: `internal/webassets/dist/**`

1. 文档说明仅支持 SolusVM 2、Bearer Token 创建/最小权限、HTTPS/CIDR、正常 stop/restart、VNC 密码与目标处理、portal fallback 和 API 版本兼容性。
2. README 标记四阶段完成，并明确 PVE 只交付扩展入口而非内置 Provider。
3. 运行秘密扫描，确保 token/password fixture、Authorization 值、VNC host/port 不进入生成资源或文档示例。
4. 运行完整 Go 测试、前端 test/lint/build、`scripts/build.sh`（宿主无 Go 时记录并用 Go 1.27 容器等价验证），同步 `internal/webassets/dist`。
5. 构建临时生产 Docker 镜像，确认无 8080 监听/运行容器，删除且只删除该临时镜像。
6. Commit: `docs: complete SolusVM 2 provider stage`

## Final review

对本计划全部提交做独立最终审查，重点核对 API 形状、Bearer 脱敏、SSRF/DNS rebinding/TLS、分页终止、状态与能力映射、SolusVM WSS 构造、raw VNC provider allowlist 不扩大、密码生命周期、PVE 示例不冒充通用实现，以及前后端契约。所有 finding 在一次集中修复后做 scoped re-review；最终需工作树干净、全部验证通过且没有运行服务。
