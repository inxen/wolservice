# WOL 网络唤醒

局域网里远程开机、关机的服务。一个 Go 二进制，自带网页面板和配置中心，路由器、NAS、随便一台常开的机器都能跑。

`/` 是快速面板，只有设备卡片和电源开关；`/config` 改配置、加设备、设密码。

## 功能

- 魔法包唤醒，多 MAC、多广播地址，端口和重发次数可调
- 在线探测，用系统 `ping` 或 TCP 连接
- SSH 关机，密码或私钥登录并校验主机指纹；配了 SSH 唤醒命令时优先走 SSH
- TP-Link 摄像头镜头遮蔽（本地接口，遮蔽/取消遮蔽）
- 巴法云、点灯科技，能用小爱、天猫精灵、小度控制
- 中文/英文、明暗主题，快速面板可以装到手机主屏幕

## 构建

需要 Go 1.22 以上。

```bash
bash build.sh        # 产物在 dist/，每次构建会先清掉旧版本
```

仓库里带了一份 `.wol-embedded-key`，本地构建和 GitHub Release 共用这把主密钥。它和发布出去的二进制一样是公开的、能提取，只用来避免“加密文件还在、密钥文件丢了”。想换成自己的密钥就直接替换这个文件，换完已保存的凭据要重新填。

Windows 用 `.\build.ps1`，只想编当前平台可以 `make build`。不内嵌密钥也能跑，只是会退回磁盘密钥文件，日志里会提醒。

## 运行

```bash
cp configs/wol.example.yaml wol.yaml
./dist/wol-windows-amd64.exe -config wol.yaml -check
./dist/wol-windows-amd64.exe -config wol.yaml
```

打开 `http://<主机>:8090/`。第一次启动没有访问密码，进去后在 `/config` 里设一个。

配置文件不存在时会自动生成一份空配置（配置中心开启、没有密码），已有文件不会被覆盖。改配置会自动热加载，改监听地址或 TLS 需要重启。

手机浏览器可以把它添加到主屏幕当 App 用；离线缓存和安装入口只在 HTTPS 或 localhost 下生效。

## 配置与凭据

字段说明、默认值和校验规则都在 [config.md](config.md)，这里只列几个容易踩的点：

- 访问密码、SSH 密码/私钥/口令、巴法云和点灯科技的密钥都存在同目录的 `wol.secrets.yaml`，AES-256-GCM 加密，访问密码只存 Argon2id 哈希。
- 主密钥优先取环境变量 `WOL_SECRETS_KEY`，其次是构建时内嵌的；旧的 `wol.secrets.key` 只用于迁移和回退。
- 内嵌密钥防的是配置和备份被拷走，防不住在线 root，也防不住从二进制里把密钥挖出来。仓库里的 `.wol-embedded-key` 和官方 Release 用的是同一把，能下载就能提取。
- 主密钥丢了，已保存的凭据就解不开，只能删掉 `wol.secrets.yaml` 重填。

## 访问控制

一个密码，两个作用域：

```yaml
auth:
  quick_panel: false    # 快速面板和 /api/devices 等接口
  config_panel: true    # /config 和 /api/config*
```

密码为空时，勾选的作用域也不校验。可信网段可以再限制来源 IP。别把面板直接暴露到公网，要外网访问就配 TLS 或者放到反向代理后面，并在代理层做访问控制。

## HTTP API

非 GET 请求都要带 `X-WOL-Config: 1` 头。开了 `quick_panel` 并且设了密码时，用 `Authorization: Bearer <密码>` 调接口。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查 |
| GET | `/api/info` | 版本、运行时长、认证状态 |
| GET | `/api/devices` | 设备列表和在线状态 |
| POST | `/api/devices/{key}/wake` | 唤醒 |
| POST | `/api/devices/{key}/shutdown` | 关机 |
| POST | `/api/devices/{key}/probe` | 立即探测一次 |
| GET | `/api/devices/{key}/mask` | 读取镜头遮蔽状态 |
| POST | `/api/devices/{key}/mask` | 设置镜头遮蔽，body `{"masked": true}` |
| POST | `/api/reload` | 重载配置 |
| GET/PUT | `/api/config` | 读取或保存配置（配置面板作用域） |

```bash
curl -X POST -H "X-WOL-Config: 1" -H "Authorization: Bearer $PASSWORD" \
  http://127.0.0.1:8090/api/devices/nas/wake
```

唤醒的返回里有实际使用的模式、发包数量和广播地址。SSH 关机如果在拿到退出码前断连，会返回 `uncertain: true`，意思是命令可能已经执行了。

## SSH 关机

在 `/config` 的设备编辑页填 host、user、密码或私钥、关机命令。先用「探测主机指纹」拿到 `SHA256:...` 填进主机指纹，再打开 SSH 开关。

普通用户执行关机需要免密 sudo，建议只授权一个固定脚本：

```sh
# /usr/local/sbin/wol-shutdown
#!/bin/sh
exec /sbin/shutdown -h now
```

```sh
# /etc/sudoers.d/wol，把 wol 换成实际用户
wol ALL=(root) NOPASSWD: /usr/local/sbin/wol-shutdown
```

关机命令填 `sudo -n /usr/local/sbin/wol-shutdown`。配置面板里有现成的命令预设，Linux、Windows、macOS 的都有。

## TP-Link 摄像头镜头遮蔽

在 `/config` 添加或编辑设备时，把「设备类型」选为 `TP-Link-camera(China)`，再填摄像头 IP、端口（默认 80）、用户名（默认 `admin`）和 TP-Link App 里设置的设备密码。摄像头类型只保留「基础」和「IoT」两个页签，没有在线探测、WOL 开机和 SSH 关机；快速面板的设备卡片会用「镜头遮蔽」圆键替代电源开关。

- 遮蔽 = 关闭摄像头画面，取消遮蔽 = 恢复画面。在线状态在打开或刷新面板读取遮蔽状态时同步获得，不做周期 ping 探测。
- 走摄像头本地 HTTP 接口，不需要 TP-Link 云账号；摄像头设备可以不填 MAC。
- 密码和 SSH 凭据一样保存在加密的 `wol.secrets.yaml`，不会写入主配置。
- IoT（巴法云 / 点灯科技）下发 `on` 时取消遮蔽，`off` 时遮蔽。
- 设备密码错误次数过多会触发摄像头锁定，改配置前请先确认密码；服务端对登录失败有 1 分钟退避。
- 摄像头 80 端口不要映射到公网；整个协议是明文 HTTP，建议只放在可信内网。

## OpenWrt

LuCI 控制页在单独的仓库：[inxen/luci-app-wolservice](https://github.com/inxen/luci-app-wolservice)。APK、更新检查和直接部署脚本都在那边，配置字段参考本仓库的 [config.md](config.md)。

## 部署

systemd：

```ini
[Unit]
Description=WOL service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/wol -config /etc/wol/wol.yaml
Restart=always
RestartSec=3
User=wol
Group=wol
AmbientCapabilities=CAP_NET_RAW
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/etc/wol

[Install]
WantedBy=multi-user.target
```

只用 TCP 探测或 SSH 的话，`CAP_NET_RAW` 可以去掉。

Docker：

```bash
docker build --build-arg WOL_EMBEDDED_KEY="$(cat .wol-embedded-key)" -t wol .
docker run -d --name wol --network host \
  -v "$PWD/wol-config:/app" wol
```

WOL 靠二层广播，建议 `--network host`，Docker Desktop 在 Windows/macOS 上一般送不到物理局域网。配置目录要可写，否则 `/config` 存不了。

交叉编译：

```bash
VERSION=0.7.0 bash build.sh
# dist/wol-windows-amd64.exe, wol-linux-arm64, wol-linux-mipsle, ...
```

Windows 下二进制不是系统服务，用 NSSM、WinSW 或任务计划程序注册开机启动。

## 安全提醒

- 在 `/config` 里设访问密码，按需开启快速面板/API 和配置面板；密码为空时对应入口就是公开的。
- `wol.secrets.yaml` 权限设 `0600`，别提交 Git，别放共享盘。旧 key 文件和 `.key.legacy` 确认没用了就删。
- SSH 尽量用私钥。`allow_unknown_host_key` 只在第一次探测时打开，拿到指纹就关掉。
- 魔法包只支持 IPv4 广播，跨 VLAN 需要路由器放行定向广播，或者用 VPN 把面板放到目标网段。

## 开发

```bash
go test ./...
go vet ./...
go run ./cmd/wol -config wol.yaml
```

目录大概是：`internal/config` 管 YAML 和校验，`internal/core` 管设备运行时和动作，`internal/httpapi` 是 API 和内嵌页面，其余按唤醒、探测、SSH、IoT 分。

## 致谢

行为和协议参考过 [gdy666/lucky](https://github.com/gdy666/lucky)（MIT）。代码是独立实现的，没有包含它的 Web 管理后台。

## 许可

MIT
