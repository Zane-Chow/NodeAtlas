# Provider 适配器 SDK

本项目的 Provider SDK 是一组编译期 Go 接口、契约测试和接入约定，不是运行时插件系统。每个适配器都会随应用一起编译和发布；应用不会加载用户上传的二进制文件。

`examples/providers/pvepanel` 只是一个**假想第三方 PVE 面板协议**的可编译示例。它不调用 Proxmox VE 原生 API，也不兼容任何真实服务商面板。不同服务商的自研面板通常没有共同 API；接入时必须复制并重命名该示例，为目标服务商分配唯一 Provider ID，并根据其正式 API 文档实现独立包和 fixture。

## 适配器边界

适配器实现 `internal/providers` 中的两个接口：

- `Factory.Create` 严格解析一个连接的 endpoint、settings 和 credentials，并创建相互隔离的 Provider 实例。
- `Provider` 实现连接验证、分页清单、单机查询、开机、关机、重启、控制台和服务商后台入口。

厂商请求/响应 DTO、认证方法、分页规则和状态映射只能放在适配器包或其 client 中。不要在 inventory、operations、console 等核心域中解析厂商响应。

示例中的 `Client` 是可替换边界：测试注入内存 fixture，正式实现注入有界 HTTP client。以下代码只说明测试接法，类型和值必须替换为真实服务商自己的协议：

```go
factory := pvepanel.NewFactory(func(endpoint *url.URL, token string) (pvepanel.Client, error) {
    return newVendorClient(endpoint, token, networkPolicy), nil
})

provider, err := factory.Create(providers.ConnectionConfig{
    ID:          "vendor-a-account-1",
    Type:        "vendor-a-pve-panel",
    Endpoint:    "https://panel.vendor-a.example",
    Settings:    json.RawMessage(`{"tenant":"customer-a"}`),
    Credentials: credentialsFromEncryptedStore,
})
```

不要直接注册示例的 `example-pvepanel` ID。复制后应使用只属于一个协议的稳定小写 ID，例如 `vendor-a-pve-panel`；未来不兼容协议应使用新 ID 或显式版本迁移。

## 配置与凭据

连接记录的职责必须分开：

- `endpoint` 只保存面板地址，不得包含 userinfo、Token、查询参数或片段。
- `settings` 只保存可回显的非秘密选项，例如租户名或显式分页大小。
- `credentials` 只保存写入后加密、读取时不回显的 Token/API Key。

所有 JSON 都应使用 `json.Decoder.DisallowUnknownFields`，只接受一个完整 JSON 值，并显式校验必填项、长度和取值范围。错误只描述哪个配置无效，不能拼接输入 JSON、Token 或上游响应。Factory 不得从环境变量、宿主文件、实例身份或其他连接回退读取凭据。

前端为每个 Provider 增加独立表单和 payload 映射。秘密字段必须使用 password 控件和 `autocomplete="new-password"`；保存成功后卸载表单，连接读取接口和浏览器持久层都不得返回秘密。

## HTTP 和网络安全

示例只做最小的 HTTPS 语法检查，不能代替正式 client 的网络防护。正式适配器必须复用 `internal/providers/network`，并在以下阶段应用同一个地址策略：

1. 保存/验证连接 endpoint 时解析并检查地址。
2. 每次 DNS 解析和实际 TCP 拨号时重新检查，防止 DNS rebinding。
3. 每次重定向时检查协议和同源约束。
4. 从厂商响应得到控制台目标时再次解析和检查。

生产连接应只允许 HTTPS，禁止 userinfo、query、fragment、非法端口、跨源重定向和 TLS 跳过。环回、链路本地、未显式放行的 RFC1918/ULA 地址必须拒绝；私网服务只通过 `PROVIDER_ALLOWED_PRIVATE_CIDRS` 放行最小网段。所有请求都要设置超时和响应体上限。

厂商错误应转换为 `*providers.Error` 或实现同等安全接口的类型：

- `SafeMessage() string` 只返回可展示的固定描述。
- `RetryableError() bool` 准确区分临时网络、限流和服务端故障。
- 错误、日志、审计和测试失败信息不得包含 Authorization、Token、密码、完整响应体或带凭据 URL。

## 清单和操作

Provider 只把经过挑选的安全字段映射为 `RemoteServer`。ID 与 scope 的组合必须稳定且唯一；`State` 必须归一化为核心枚举，原始状态只能放进不含秘密的 `RemoteState`。`Spec` 和 `Addresses` 必须是有效 JSON，不能保存整个厂商对象。

分页实现必须验证 cursor，拒绝空值、倒退和循环。适配器自身应限制每页响应大小；调用方和契约测试还会限制总页数与服务器数量。

电源方法必须使用服务商定义的最安全语义，并返回可审计但不含秘密的 `ActionReceipt.RequestID`。能力必须与规范化状态一致：可用能力不附失败原因，不可用能力给出稳定、安全的原因。挂起、处理中或未知状态不要乐观开放危险操作。

## 控制台三层回退

控制台依次提供：

1. 本应用内嵌的本地同源 noVNC/终端代理。
2. 使用相同一次性本地票据打开新窗口或标签页。
3. 不含凭据的服务商后台 HTTPS 页面。

浏览器只能得到本地一次性 ticket、协议和当前会话必要的数据，不得得到上游 host、port、API Token 或可长期复用 URL。密码只能存在于短期内存目标中，不能写入数据库、审计、日志或浏览器存储。

WSS 目标必须从已信任 endpoint 与严格校验的响应字段构造，并继续经过中央 console target policy；不能盲信上游提供的任意 WebSocket origin。`vnc+tcp` 是高信任能力，新 Provider 默认不能使用。只有完成专门安全审查、证明每个目标经过网络策略后，才能在中央 allowlist 中加入**精确 Provider ID**；相似前后缀和大小写变体不能自动继承权限。

若控制台不可用，内嵌和新窗口按钮都应禁用，但安全的服务商后台入口可继续启用。不要通过前端字段或厂商深链路绕过后端票据和回退规则。

## 注册与前端接入

完成适配器后，在应用 composition root 中显式构造 Factory 并注册唯一 ID。注册时传入由应用配置生成的私网 CIDR 策略，不要在适配器内读取全局环境。为 `/provider-types` 增加稳定显示名，并添加组合测试，证明多个同类型连接使用各自的 endpoint、settings 和解密凭据。

前端至少补充以下测试：

- 表单提交的 provider type、endpoint、settings 和 credentials 形状准确。
- 秘密控件和保存后不回显行为。
- 电源能力与确认提示。
- 内嵌、新窗口和 provider portal 的回退。
- console session 与 popout 消息没有上游地址和凭据字段。

## 契约测试

`internal/providers/contracttest.Run` 会执行两轮有界清单遍历，并检查：

- 连接显示名和版本非空。
- 分页能终止，cursor 不循环，总页数和总服务器数不超限。
- `(scope, external_id)` 唯一且跨遍历稳定。
- 状态属于核心枚举，Spec/Addresses 是合法 JSON。
- `GetServer` 与清单身份一致。
- 六项能力均符合 available/reason 结构。
- 指定错误实现 `SafeMessage`、`RetryableError`，且安全消息不包含 fixture 标记的秘密。

在适配器 fixture 测试中调用：

```go
contracttest.Run(t, contracttest.Fixture{
    Provider: providerBackedByInMemoryFixture,
    Limits: contracttest.Limits{MaxPages: 20, MaxServers: 2_000},
    ErrorCases: []contracttest.ErrorCase{{
        Name: "authentication rejected",
        Call: func(ctx context.Context) error {
            return validateWithRejectedCredential(ctx)
        },
        WantRetryable: false,
        Forbidden: []string{fixtureToken},
    }},
})
```

契约失败只报告固定的不变量名称，不输出完整 server、payload、URL 或原始 error。契约测试是公共底线，不替代适配器对官方响应形状、认证 header、分页边界、状态映射、请求体、响应上限和秘密生命周期的专门测试。

## 发布检查清单

- Provider ID 唯一，示例包未被直接注册，编译期接口断言存在。
- 使用真实服务商文档和录制后脱敏的 fixture，不声称兼容其他 PVE 面板。
- endpoint/settings/credentials 严格解析，凭据只写、独立加密且无隐式回退。
- 共享网络策略覆盖保存、DNS、拨号、重定向和控制台目标。
- 响应体、分页、服务器总数和操作轮询均有上限。
- inventory 只含白名单字段，错误/日志/审计/生成资源通过秘密扫描。
- 电源请求的方法、路径、请求体和回执 ID 有测试。
- 控制台三层回退与中央 target policy 有测试；raw TCP 默认拒绝。
- 适配器 fixture 通过 `contracttest.Run`，全量 Go 和前端测试通过。
- 内嵌 Web 资源同步，生产镜像可构建，验证后没有遗留监听、容器或临时镜像。
