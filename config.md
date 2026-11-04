# 配置参考

这份文档列了 `wol.yaml` 的所有字段，包括类型、默认值、什么时候生效、运行时行为和校验规则。可以直接复制的模板在 [`configs/wol.example.yaml`](configs/wol.example.yaml)。

> LuCI 的「配置参数 → LuCI 表单」能编辑 `server`、`config_panel`、`auth`、`log`、`probe`。字段含义以本文档为准；设备和 SSH/IoT 凭据建议在服务自带的 `/config` 或 YAML 原文里维护。
>
> 旧版本的 `relay` / `relay_clients` / `server.access_token` 已经移除；配置里还留着会让严格 YAML 解析报错。

## 加载与热重载

| 行为 | 说明 |
| --- | --- |
| 启动 | `wol -config /path/wol.yaml`，默认 `./wol.yaml`；密钥文件默认 `wol.secrets.yaml`，可用 `-secrets` 指定 |
| 文件不存在 | 自动创建父目录并生成默认配置（空 `devices`、`config_panel.enabled: true`、无密码）；已有文件（包括校验失败的）绝不覆盖 |
| 密钥文件 | 默认与配置同目录、同名前缀 + `.secrets.yaml`；AES-256-GCM 加密；缺失时视为空，权限应为 `0600` |
| 主密钥 | 默认在构建时通过 `-ldflags -X wol/internal/secrets.EmbeddedKey=...` 内嵌到二进制；`WOL_SECRETS_KEY`（32 字节 hex/base64）可覆盖；旧版 `<secrets>.key` 仅用于迁移/回退 |
| 明文迁移 | 旧配置里的 `config_panel.password`、SSH 密码/私钥/口令、IoT key 会在首次启动时迁移到加密文件，主配置自动清理；配置 `.bak` 会剥离凭据；旧版 `access_token` 字段不再支持，需手动删除 |
| 仅校验 | `wol -config wol.yaml -check`，通过后输出 `configuration OK: ...` 并退出；文件不存在时同样会先生成默认配置；只读检查，不执行迁移落盘 |
| 解析 | YAML 严格模式：未知字段、未知层级会直接报错，避免拼写错误静默失效 |
| 处理流程 | 读取主配置 → 读取密钥文件 → 合并凭据 → `Normalize()` → `Validate()`；失败则不启动 |
| 自动重载 | 每 2 秒比较主配置和密钥文件的 mtime/size，变化后重载。devices / probe 或运行时凭据变化会重建相关子系统；仅 `title`、`auth`、`config_panel`、`log` 等变化只更新内存配置。重载失败保留旧配置继续运行 |
| 手动重载 | `POST /api/reload`；Unix 下发送 `SIGHUP` |
| 网页保存 | `PUT /api/config`（需要配置面板认证和 `X-WOL-Config: 1`）会校验 → 写密钥文件 → 写主配置；两个文件分别保留 `.bak` 备份；保存会重写 YAML，原有注释丢失 |
| 必须重启 | `server.listen`、`server.tls_cert`、`server.tls_key`、`server.read_timeout`、`server.shutdown_timeout`、`log.level`、`log.format` 变更不会热生效 |
| 时长写法 | Go duration（`"500ms"`、`"5s"`、`"1m30s"`）或纯数字（单位秒）。`≤0` 视为未设置并回落默认值 |
| 相对路径 | `tls_cert`、`tls_key`、`ssh.private_key_file` 相对**配置文件所在目录**；`ssh.private_key_file` 支持 `~` |
| 规范化 | MAC 统一为大写冒号格式；广播地址只接受 IPv4；字符串字段会去掉首尾空白 |

最小可用配置：

```yaml
server:
  listen: "8090"
devices:
  - key: "nas"
    name: "NAS"
    macs: ["AA:BB:CC:DD:EE:FF"]
```

默认生成的文件包含配置面板、`auth` 默认值、日志、探测和空设备列表，可直接访问 `/config` 添加设备；默认配置以 `0600` 权限写入。

## server

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `listen` | string | `"8090"` | 监听端口或绑定地址；支持纯数字 `"8090"`（自动补成 `:8090`）、`":8090"` 或 `"192.168.1.10:8090"`；端口必须为数字 0–65535 |
| `title` | string | `"网络唤醒"` | 面板标题，写入 `<title>` 和页头 |
| `tls_cert` | string | `""` | HTTPS 证书路径；必须与 `tls_key` 成对配置 |
| `tls_key` | string | `""` | HTTPS 私钥路径 |
| `read_timeout` | duration | `"15s"` | HTTP 请求读取超时；修改后需要重启进程 |
| `shutdown_timeout` | duration | `"15s"` | 收到退出信号后，等待 HTTP 连接优雅关闭的最长时间 |

## auth

快速面板 / API、配置面板共用**一个访问密码**；密码本身只保存在加密 secrets 文件的 `config_panel.password_hash`（Argon2id），本段只控制哪些入口需要它。

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `quick_panel` | bool | `false` | 快速面板与 API 是否需要访问密码；开启且已设密码时 `/`、`/quickcontrol` 显示登录页，`/api/devices…`、`/api/reload` 需要密码 |
| `config_panel` | bool | `true` | `/config`、`/api/config*` 是否需要访问密码；仅在 `config_panel.enabled: true` 时生效 |
| `trusted_networks_enabled` | bool | `false` | 是否启用来源地址白名单；关闭时信任所有地址 |
| `trusted_networks` | string[] | `[]` | 白名单，IPv4/IPv6 地址或 CIDR；启用时不能为空 |

规则：

- 未设置访问密码时两个作用域开关都不生效（等同于无密码访问）；设置/修改密码请用 `/config` 的「信任凭据」。
- 交互式页面通过 `POST /api/auth/login` 获取 HttpOnly + SameSite=Strict 会话 Cookie（12 小时滑动续期）；`POST /api/auth/logout` 退出。两个作用域共用同一个 Cookie。
- 直接 API 调用在 `quick_panel: true` 时支持 `Authorization: Bearer <password>` 或 `X-Access-Token: <password>`；不要把密码放进查询参数。
- 所有非 GET 请求（唤醒、关机、探测、重载、配置写入）都要带 `X-WOL-Config: 1` 请求头，防止跨站表单提交。
- `/api/health`、`/api/info`、`/api/auth/*`、静态资源和登录页不做密码校验。
- 旧版 `auth.api` 字段已废弃并忽略；API 保护跟随 `quick_panel`。
- 可信网段启用后，除 `/assets/*` 和 `/favicon.svg` 外的所有请求都要求来源地址命中列表；配置面板保存时会校验当前 IP，不在新列表内会拒绝保存。它看的是直连地址，放在反向代理后面时所有请求都会变成代理 IP；这种场景请在代理层限制访问。
- 密码修改后所有旧会话立即失效。
- `/api/info` 返回 `auth` 对象（各入口是否启用/需要密码、是否设置密码、可信网段），可用于外部集成。

## config_panel

独立运行时的网页配置中心：`http://<主机>:<端口>/config`。

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | 是否提供 `/config` 配置面板；关闭时 `/config` 与 `/api/config*` 返回 404 |

行为：

- 首次运行没有密码，`/config` 直接放行；进入「信任凭据」设置共享访问密码。
- `auth.config_panel` 打开且密码非空时，`/config` 与 `/api/config*` 需要登录；未登录只会看到登录遮罩，页面骨架仍可加载。
- 密码保存为密钥文件中的 Argon2id 哈希，无法逆向读取；勾选“清除访问密码”可恢复无密码访问。
- 登录成功后设置 HttpOnly + SameSite=Strict 会话 Cookie（12 小时，滑动续期）；登录失败按 IP 限速。
- 密码修改后所有旧会话立即失效，需要重新登录。
- 写操作（登录、退出、`PUT /api/config`、SSH 指纹探测）要求请求头 `X-WOL-Config: 1`，用于防止跨站表单提交。
- 配置面板可编辑基础设置、设备（含探测/SSH/IoT）、探测与日志；保存后自动校验并应用，监听地址/TLS/日志等级等需要重启进程。
- 关闭配置面板后只能通过编辑 YAML 重新开启。
- 配置面板 API：`GET /api/config/status`、`POST /api/config/login`、`POST /api/config/logout`、`GET/PUT /api/config`、`POST /api/config/ssh/hostkey`。
- `GET /api/config` 不会回传已保存的密码、私钥或 IoT Key，只返回 `secrets` 里的“是否已设置”。`PUT` 时这些字段留空表示保持原值，填新值表示覆盖；设备的 `clear_secrets: true` 会清空该设备已保存的凭据（主配置不保存这个字段）。

```yaml
config_panel:
  enabled: true
```

密码哈希保存在默认加密文件 `wol.secrets.yaml` 的 `config_panel.password_hash` 分组中。忘记密码时无法直接编辑加密文件；可删除加密文件（及 `.bak`）后重启（会同时清空 SSH/IoT 凭据），重新进入配置面板设置。主密钥内嵌在二进制中，不需要通过删除密钥文件来重置。

## 密钥与加密文件

默认加密文件与主配置同目录、同名前缀 + `.secrets.yaml`：

```text
/etc/wol/wol.yaml
/etc/wol/wol.secrets.yaml          # AES-256-GCM 加密
/etc/wol/wol.secrets.yaml.bak      # 加密备份
# 新构建默认没有 key 文件：主密钥内嵌在 /usr/bin/wol 中
# /etc/wol/wol.secrets.key         # 旧版磁盘主密钥，仅迁移/回退用，0600
```

主密钥优先级：

1. 环境变量 `WOL_SECRETS_KEY`（32 字节 hex/base64），最高优先级，适合容器/密钥管理系统注入。
2. 构建时注入的 `wol/internal/secrets.EmbeddedKey`（推荐，见主 README 的构建章节）。
3. 旧版 `-secrets-key` / `<secrets>.key` 文件，仅用于兼容和迁移；在存在内嵌密钥或环境变量时不会使用。

内嵌密钥的构建不会创建 key 文件。升级时如果检测到旧 key 文件，且旧 secrets 是用它加密的，启动会先回退到旧密钥解密，再用内嵌密钥重新加密，并把旧文件改名为 `<secrets>.key.legacy`（0600）；确认无需回滚后删除 `.legacy`。

加密文件不能手工编辑，请在 `/config` 中设置凭据。解密后的逻辑结构：

```text
config_panel    -> password_hash (argon2id)   # 快速面板/API 与配置面板共享
devices
  <secret_ref>  -> ssh.password / private_key / passphrase
                   iot.bemfa_secret_key / blinker_auth_key
```

规则：

- 设备通过 `secret_ref` 引用分组；`secret_ref` 默认等于设备 `key`，同一 `secret_ref` 不能被多个设备复用。
- 主配置中不保存凭据；如果手工写入旧字段，启动时会迁移到加密文件并清理主配置。
- `password_hash` 不可逆；其余字段加密保存，但运行时需要解密。
- 主密钥优先取 `WOL_SECRETS_KEY`，其次是内嵌的 `.wol-embedded-key`。仓库里这份是本地构建和官方 Release 共用的一把，公开可见；它和发布二进制一样能被提取，只用来避免“加密文件还在、密钥文件丢了”。
- 加密文件、`.bak` 和迁移遗留的 `.key.legacy` 都应限制权限（0600）；真实凭据不要写进主配置，也不要提交到仓库。
- 主密钥变更需要重新加密（当前没有自动轮换命令）；变更前先备份旧密钥/旧二进制和加密文件。
- 系统被 root 入侵后仍可从二进制、进程内存或环境变量中提取主密钥并解密；加密主要防止配置/备份被拷贝后的泄露，内嵌密钥只是避免“密钥文件直接放在部署目录”的低级泄露，不能阻止在线 root。

## log

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `level` | string | `"info"` | `debug` / `info` / `warn` / `error`；`warning` 等同 `warn`，未知值按 `info` |
| `format` | string | `"text"` | `text` 或 `json`；未知值按 `text` |

日志输出到 stdout。`debug` 会记录每个 HTTP 请求（方法、路径、状态码、耗时）。

## probe

全局默认探测参数，设备可在 `devices[].reachability` 中覆盖。

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `interval` | duration | `"15s"` | 设备探测周期；`devices[].reachability.interval` 可覆盖 |
| `timeout` | duration | `"2s"` | 单个目标的探测超时；`devices[].reachability.timeout` 可覆盖 |

探测器每 1 秒检查一次到期设备；服务启动后约 1 秒内开始首轮探测。多个目标并发探测，总耗时约等于单个 `timeout`，不是目标数 × timeout。

## devices

设备列表，数组顺序即面板显示顺序。

| 字段 | 类型 | 默认 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `key` | string | 自动生成 | 建议显式设置 | 设备唯一标识，用于 API 路径；不能包含 `/`、`?`、`#` |
| `secret_ref` | string | 同 `key` | 否 | 凭据分组引用；默认使用 `key`，不同设备不能重复 |
| `name` | string | 同 `key` | 否 | 面板显示名 |
| `macs` | string[] | — | **是** | MAC 列表；支持 `:` 或 `-` 分隔，自动转为大写冒号格式 |
| `broadcast_ips` | string[] | `[]` | 否 | 魔法包广播地址；留空自动枚举本机所有活动 IPv4 网卡的定向广播地址，只接受 IPv4 |
| `port` | int | `9` | 否 | WOL UDP 端口；非法值回落 `9` |
| `repeat` | int | `5` | 否 | 每个 MAC × 广播地址的发送次数；合法范围 1–10，越界回落 `5` |
| `reachability` | object | — | 否 | 在线探测，见下文 |
| `ssh` | object | — | 否 | SSH 唤醒/关机，见下文 |
| `iot` | object | — | 否 | 语音助手接入，见下文 |

`key` 生成规则（不显式设置时）：

- 优先用 `name`，没有则用第一个 MAC；转小写、空格转 `-`、去除 `[a-z0-9_-]` 之外的字符，再加 4 位随机十六进制后缀。
- 因为后缀每次重载都会重新生成，**生产环境务必显式设置稳定的 `key`**，否则热重载后 API 路径会变化。
- 重复的显式 `key` 不会报错，而是追加数字后缀（当前实现从 `-3` 开始），建议自行保证唯一。

### 唤醒与关机决策

- **唤醒**：配置了 `ssh.wake_command` 时先走 SSH；SSH 失败则回退魔法包。魔法包发送数量 = `macs × broadcast_ips × repeat`。
- **关机**：配置了 `ssh.shutdown_command` 时走 SSH；未配置则报错。
- 所有命令都来自配置文件，API 不接受任意命令。
- 巴法云/点灯科技触发的开关同样走上述唤醒、关机决策。
- 状态字段（`can_wakeup`、`state_conflict`、`connection_state` 等）的完整含义见 [README 状态含义](README.md#状态含义)。

### reachability

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | 是否启用周期性在线探测 |
| `targets` | string[] | `[]` | 探测目标（IP 或域名）；启用时必填且每个目标都要合法 |
| `method` | string | `"ping"` | `ping` / `tcp` / `auto`；启用时只接受这三种 |
| `tcp_port` | int | `0` | `method: tcp` 时必填（1–65535），例如 `445`、`22` |
| `interval` | duration | 继承 `probe.interval` | 本设备探测周期；`≤0` 继承全局 |
| `timeout` | duration | 继承 `probe.timeout` | 本设备单次探测超时；`≤0` 继承全局 |

探测行为：

- `ping`：调用系统 `ping` 命令（Windows `ping -n 1 -w <ms>`；Linux `ping -c 1 -W <秒>`；macOS `ping -c 1 -t <秒>`）。系统里必须有 `ping`，Docker 镜像已安装 `iputils`。
- `tcp`：对 `target:tcp_port` 建立 TCP 连接，连接成功即在线。
- `auto`：`tcp_port > 0` 时用 TCP，否则用 ping。
- 多目标并发探测：全部成功 = `Reachable`，部分成功 = `PartiallyReachable`，全部失败 = `Unreachable`，无目标 = `Unknown`。
- 设备启用 SSH 且 `targets` 为空、`ssh.host` 非空时，会自动把 `ssh.host` 填进 `targets`。
- `ssh.host` 为空且 `targets` 非空时，会自动使用 `targets[0]` 作为 SSH 主机。
- 手动触发：`POST /api/devices/{key}/probe`；若已有探测在执行，直接返回当前状态。

### ssh

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | 开启 SSH 能力 |
| `host` | string | `reachability.targets[0]` | SSH 主机；启用时必填且必须是合法 IP/域名 |
| `port` | int | `22` | SSH 端口；非法值回落 `22` |
| `user` | string | — | 登录用户；启用时必填 |
| `password` | string | `""` | 密码认证；保存在密钥文件中（旧字段会自动迁移） |
| `private_key` | string | `""` | 内联 PEM 私钥；保存在密钥文件中（旧字段会自动迁移） |
| `private_key_file` | string | `""` | 私钥文件路径，相对配置文件目录，支持 `~`；路径本身不是敏感值 |
| `passphrase` | string | `""` | 加密私钥的口令；保存在密钥文件中（旧字段会自动迁移） |
| `host_key` | string | `""` | 服务器主机指纹；支持 `SHA256:...` 或裸 base64 |
| `allow_unknown_host_key` | bool | `false` | `true` 时跳过指纹校验（不安全，仅建议首次探测时临时使用） |
| `shutdown_command` | string | `""` | 关机命令；非交互执行，普通 Linux 用户需要 NOPASSWD sudo（见下文） |
| `wake_command` | string | `""` | 可选；配置后「唤醒」改为通过 SSH 执行该命令 |
| `timeout` | duration | `"10s"` | 连接、握手、命令执行的超时（各阶段分别计时） |

约束与行为：

- 启用后：`user` 必填；密钥文件中的 `ssh.password` / `ssh.private_key` / 配置中的 `private_key_file` 至少一种；`shutdown_command` / `wake_command` 至少一个；`host_key` 必填，除非 `allow_unknown_host_key: true`。
- 主机指纹通过配置面板的“探测主机指纹”或 `GET /api/devices/{key}/ssh/hostkey` 获取（`auth.quick_panel: true` 时需要访问密码），返回 `fingerprint`、`configured`、`trusted`。推荐流程见 [README SSH 章节](README.md#ssh-关机与主机指纹)。
- 命令通过远端登录 shell 执行；超时会发送 `SIGTERM` 并关闭会话，结果标记 `uncertain`。
- 若命令已开始但连接在返回退出码前断开（常见于重启类命令），视为可能已执行：返回成功且 `uncertain: true`。
- 命令输出最多保留 32 KB。
- SSH 唤醒失败会回退到魔法包；关机失败则返回错误。

### 关机命令与 sudo（Linux）

SSH 会话不会分配交互终端，普通用户的 `shutdown` 会权限不足，`sudo` 也会因无法输入密码而失败。必须在**执行命令的机器**上配置 NOPASSWD。推荐只授权固定辅助脚本：

```sh
# /usr/local/sbin/wol-shutdown
#!/bin/sh
exec /sbin/shutdown -h now
```

```sh
# /etc/sudoers.d/wol（把 wol 换成实际运行用户/SSH 登录用户）
wol ALL=(root) NOPASSWD: /usr/local/sbin/wol-shutdown
# sudo chmod 0755 /usr/local/sbin/wol-shutdown
# sudo chmod 0440 /etc/sudoers.d/wol
```

关机命令填写 `sudo -n /usr/local/sbin/wol-shutdown`。配置面板的关机命令字段提供「一键填入」预设：Linux 辅助脚本、sudo 免密、root、Windows、macOS。

配置面板还提供「Linux · sudo 密码」预设：

```sh
echo '你的sudo密码' | sudo -S systemctl poweroff
```

`sudo -S` 从标准输入读取密码，适合临时使用；密码会出现在命令行进程参数和 YAML 配置中，长期运行建议改用 NOPASSWD 或辅助脚本。

### iot.bemfa（巴法云）

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | 是否接入巴法云 |
| `secret_key` | string | `""` | 巴法云私钥；启用时必填，同时作为 MQTT Client ID；保存在密钥文件（旧字段自动迁移） |
| `topic` | string | `""` | 开关主题；启用时必填，必须以 `001` 结尾，且全配置内唯一 |

- 相同 `secret_key` 的多个设备共用一个 MQTT 连接（按私钥分组），每个设备使用不同 `topic`。
- Broker 固定为 `mqtts://bemfa.com:9503`（TLS 1.2），无需额外配置。
- 收到 `on` / `true` 执行唤醒，`off` / `false` 执行关机；状态同时回写到 `<topic>/up`（retained）。
- 连接断开后 5 秒重连；连接失败后 10 秒重试。
- 面板显示状态：`未启用`、`连接中`、`已连接`、`重连中`、`连接失败: ...`。

### iot.blinker（点灯科技）

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | 是否接入点灯科技 |
| `auth_key` | string | `""` | DIY 设备 Auth Key；启用时必填且全配置内唯一；保存在密钥文件（旧字段自动迁移） |
| `device_type` | string | `"outlet"` | 设备类型：`outlet`、`light`、`multi_outlet`、`sensor`、`fan`、`aircondition` |

- 启动后向 `iot.diandeng.tech` 获取设备信息，并同步小爱（MIOT）、天猫精灵（AliGenie）、小度（DuerOS）的语音助手类型。
- `device_type` 会转小写；配置校验不限制取值，但非上述值时 MIOT/AliGenie 可能无法识别，DuerOS 会回退为 `SOCKET`。
- MQTT 心跳周期 599 秒；断线重连从 5 秒开始指数退避，上限 60 秒。
- 语音/App 下发 `on` 执行唤醒，`off` 执行关机。

## 校验规则速查

启动或 `-check` 时会对以下条件报错：

- `server.listen` 必须能被解析为端口或 `host:port`；纯数字会自动补 `:`；端口必须是 0–65535 的数字；`tls_cert` 与 `tls_key` 必须同时设置或同时留空。
- 每个设备 `macs` 不能为空且必须合法；`broadcast_ips` 必须是 IPv4；`key` 不能包含 `/`、`?`、`#`。
- `secret_ref` 默认等于 `key`，同一 `secret_ref` 不能被多个设备复用。
- `reachability.enabled: true` 时 `targets` 不能为空、目标必须合法、`method` 必须是 `ping`/`tcp`/`auto`；`method: tcp` 时 `tcp_port` 必须在 1–65535。
- `ssh.enabled: true` 时：`host`、`user` 必填；密码/私钥至少一种；`shutdown_command`/`wake_command` 至少一个；`host_key` 必填（除非 `allow_unknown_host_key: true`）。
- `iot.bemfa.enabled: true` 时：密钥文件中的 `secret_key`、主配置中的 `topic` 必填；`topic` 必须以 `001` 结尾且不能与其他设备重复。
- `iot.blinker.enabled: true` 时：密钥文件中的 `auth_key` 必填且不能与其他设备重复。
- `auth.trusted_networks` 中每一项必须是合法的 IPv4/IPv6 地址或 CIDR；`trusted_networks_enabled: true` 时列表不能为空。

## 开发提示

配置相关代码位置：

| 文件 | 内容 |
| --- | --- |
| [`internal/config/config.go`](internal/config/config.go) | 结构体与 YAML tag、`Normalize()` 默认值、`Validate()` 校验、`ResolvePath()` |
| [`internal/httpapi/auth.go`](internal/httpapi/auth.go) | 共享访问密码、入口作用域、会话登录/退出、可信网段 |
| [`internal/httpapi/configpanel.go`](internal/httpapi/configpanel.go) | 配置面板会话、配置读写 API |
| [`internal/config/save.go`](internal/config/save.go) | 配置原子保存、备份、恢复与默认配置生成 |
| [`internal/secrets/`](internal/secrets) | 加密 secrets 文件、主密钥、Argon2id 密码哈希、旧明文迁移 |
| [`internal/core/manager.go`](internal/core/manager.go) | 设备运行时、唤醒/关机决策、探测调度、热重载 |
| [`internal/wol/magic.go`](internal/wol/magic.go) | 魔法包、自动广播地址检测 |
| [`internal/reach/reach.go`](internal/reach/reach.go) | ping/TCP 探测与聚合状态 |
| [`internal/sshpwr/ssh.go`](internal/sshpwr/ssh.go) | SSH 认证、主机指纹、命令执行 |
| [`internal/iot/`](internal/iot) | 巴法云、点灯科技接入 |
| [`cmd/wol/main.go`](cmd/wol/main.go) | 启动参数、HTTP 服务器、配置监听与信号处理 |

新增配置字段的步骤：

1. 在 `internal/config/config.go` 对应结构体加 YAML tag。
2. 需要默认值/规范化的，在 `Normalize()` 中处理。
3. 有约束的，在 `Validate()` 中追加校验；涉及敏感值的字段应放入 `internal/secrets` 的密钥文件结构，并在合并后校验。
4. 在运行时包中读取并使用该字段。
5. 更新本文件与 `configs/*.example.yaml`。
6. 执行 `go test ./...` 和 `wol -config wol.yaml -check`。

已知注意点：

- 自动生成的设备 `key` 带随机后缀，每次重载都会变；生产配置最好显式写 `key`。
- 重复 `key` 的自动后缀从 `-3` 开始（当前实现细节），不要依赖该编号。
- `reachability.method` 只在 `enabled: true` 时校验；关闭时填错不会报错。
- 热重载不会重启 HTTP 监听器，但 `auth`、`config_panel` 是每次请求实时读取的，修改后立即生效；访问密码变化会让旧会话失效。
- `auth.trusted_networks` 为空时 JSON 返回 `null`，等价于没有白名单条目。
