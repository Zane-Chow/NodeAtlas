# Mock Provider

Mock Provider 用于在不访问外部服务的情况下验证服务商连接、凭据加密、后台同步和统一服务器清单。它经过与后续 AWS、VirtFusion 等适配器完全相同的注册、任务和库存路径。

## 创建连接

在“服务商”页面选择“添加服务商”，填写连接名称、服务器数量、控制台能力和任意非空 Mock Token。Token 会使用当前 `CREDENTIAL_ACTIVE_KEY_VERSION` 对应的 AES-256-GCM 密钥加密，之后不会通过 API 或界面返回。

启用连接后，保存会自动排入一次同步任务。也可以使用“测试连接”和“立即同步”分别验证健康状态和重新获取清单。同一种类型可以创建任意多个独立连接。

## 设置字段

| 字段 | 默认值 | 约束与用途 |
| --- | --- | --- |
| `server_count` | `4` | 生成 1–500 台确定性服务器。 |
| `seed` | `1` | 控制服务器状态、地址和区域的确定性分布。 |
| `operation_delay_ms` | `0` | 模拟操作延迟，范围 0–30000。 |
| `failure_rate` | `0` | 模拟电源操作失败概率，范围 0–1。 |
| `health_mode` | `healthy` | `healthy`、`authentication_failure`、`network_failure` 或 `rate_limited`。 |
| `console_profile` | `embedded` | `embedded`、`window`、`portal` 或 `none`。 |

当前界面暴露最常用的 `server_count` 和 `console_profile`；其余字段可用于 API 和自动化测试。凭据对象只接受一个非空 `token` 字段。

## 故障与同步语义

- `authentication_failure` 会将连接标记为离线。
- `network_failure` 与 `rate_limited` 会标记为受限，并由任务系统按策略重试。
- 一次失败的同步不会清空上一次成功同步的服务器。
- 删除连接采用软删除，并从可见统一清单中移除其服务器。

Mock 适配器已经实现电源和控制台契约，但界面中的开机、关机、重启按钮会在里程碑 3 启用，VNC/串行与服务商后台回退会在里程碑 4 启用。

## 凭据密钥轮换

生成新密钥：

```bash
openssl rand -base64 32
```

轮换时同时保留旧版本，并把活动版本指向新版本：

```dotenv
CREDENTIAL_KEYS=1:<旧密钥>,2:<新密钥>
CREDENTIAL_ACTIVE_KEY_VERSION=2
```

旧密钥必须保留到所有引用它的凭据都已重新加密。数据库备份不包含这些外部密钥；应把密钥放入独立的密钥管理或备份系统。
