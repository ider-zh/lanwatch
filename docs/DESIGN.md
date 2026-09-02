# lanwatch — 内网设备上下线监控设计

> 版本 v1 / 2026-09-02
> 状态：待评审。文档中所有"实测"结论均来自本机对两台路由器的实际探测，非推测。

---

## 1. 目标与非目标

### 1.1 目标

| 编号 | 需求 | 出处 |
|---|---|---|
| G1 | 每 60s 检测一次内网设备在线状态 | 用户 |
| G2 | 数据源仅限抓取两台路由器，本机不做主动探测 | 用户决策 |
| G3 | 覆盖 `192.168.1.0/24` 与 TP-Link 背后的 `192.168.99.0/24` | 实测拓扑 |
| G4 | Web 前端，所有页面需登录 | 用户 |
| G5 | 以时间轴形式展示所有设备的在线区间 | 用户 |
| G6 | 通知渠道默认支持邮件 + 钉钉 Webhook | 用户 |
| G7 | 可为设备在渠道间自由订阅（选设备 → 选渠道） | 用户 |
| G8 | 上下线通知需抑制抖动，默认离线超时才告警 | 用户决策 |

### 1.2 非目标

- 不做流量分析、DPI、端口扫描、设备指纹画像
- 不控制路由器（不写配置、不踢设备）
- 不做多租户；单管理员 + 可选只读账号
- 不做 HA / 集群；单进程 + 单 SQLite 文件

---

## 2. 环境勘察结论（实测）

### 2.1 主机

- `192.168.1.220/24`，网关 `192.168.1.1`，网卡 `eno1`
- Go 1.26.2 / Node 26 / Python 3.13 / Docker（用户组已加入）
- Go module 走 `goproxy.cn` 可用，npm 可用
- `ping` 带 `cap_net_raw`，`fping` 存在，**无** arp-scan/nmap/arping
- 本机 **不可达** `192.168.99.0/24`（TP-Link NAT 背后），该子网只能靠抓 TP-Link

### 2.2 ASUS RT-AC54U — `192.168.1.1`

| 项 | 实测结果 |
|---|---|
| 开放端口 | 22(SSH)、80(HTTP) |
| SSH | `admin:<密码>` 密码登录可用（Go `x/crypto/ssh` 验证通过）；密码经 `${ASUS_PASSWORD}` 注入，不入库 |
| HTTP | 所有路径（含不存在路径）Basic 认证后一律返回 `200` + 空体；`login.cgi` POST 返回空 → **Web 侧不可用** |
| 系统 | `Linux RT-AC54U 3.4.113 mips`，AsusWRT |
| 内核信息源 | `ip neigh show dev br0`（带 `REACHABLE/STALE/DELAY` 状态）、`/proc/net/arp` |
| DHCP 租约 | `/tmp/dnsmasq.leases`（当前仅 1 条 → 大多数设备为静态 IP） |
| 无线 | 接口 `ra0/rai0/ra1/rai1`（Ralink）；`iwpriv ra0 get_mac_table` 当前为空；`wl`/`brctl showmacs`/`nvram` 不可用 |
| 主动复核 | 路由器侧 `ping -c1 -W1 <ip>` 可用（0.7ms） |

**结论：SSH 是 ASUS 唯一可靠通道**，`ip neigh` 提供带状态的邻居表，是 `192.168.1.0/24` 的主数据源。
（采集脚本见**附录 B**，协议速查见**附录 A**，可运行的验证脚本见 `scripts/tplink_probe.py`。）

**已跑通的采集脚本**（单次 exec，实测输出见下）：
```sh
for ip in $PROBE_IPS; do (ping -c1 -W1 $ip >/dev/null 2>&1 && echo "P $ip") & done; wait
echo NEIGH; ip -4 neigh show dev br0; echo LEASES; cat /tmp/dnsmasq.leases
```
```
P 192.168.1.3
P 192.168.1.229
NEIGH
192.168.1.230 lladdr d8:cb:8a:9c:8b:b1 REACHABLE
192.168.1.99  INCOMPLETE          ← ping 不存在的 IP 会产生该条目
192.168.1.100 lladdr 18:fb:7b:a3:1b:e3 STALE
LEASES
360 18:fb:7b:a3:1b:e3 192.168.1.100 idrac-GVC8J92 01:18:fb:7b:a3:1b:e3
```
**两个解析陷阱**：
1. **无 `lladdr` 的条目（`INCOMPLETE`/`FAILED`）必须丢弃** —— ping 不存在的 IP 会自己造出一条，否则会凭空多出一堆"设备"。
2. `STALE` 不等于在线（上例 `.100` 是 iDRAC，长期空闲），需按 §4.2 做 ping 复核。

### 2.3 TP-Link TL-WDR5600 — `192.168.1.3:8888`

| 项 | 实测结果 |
|---|---|
| 开放端口 | **仅 8888**（无 SSH/Telnet/SNMP） |
| 固件 | `TL-WDR5600 1.0`，`sw_version 1.0.5 Build 150528` |
| 工作模式 | **路由模式**：WAN=`192.168.1.3`，LAN=`192.168.99.1/24` |
| 认证 | 见下，已实测登录成功 |

**认证流程（完整还原，已验证）**

1. 密码混淆 `orgAuthPwd(pwd)`：`securityEncode("RDpbLfCPsJZ7fiv", pwd, DIC)`
   - `DIC` 为 255 字符字典，硬编码在 `/web-static/lib/slp-ajax.js`
   - 算法：`for i in range(max(len(key), len(pwd))): out += DIC[(ord(key[i]) ^ ord(pwd[i])) % 255]`，越界侧用 `0xBB` 填充
   - 示例（测试用假密码）：`DevPass123` → `t7zU2drULeefbwK`；真实密码经 `${TPLINK_PASSWORD}` 注入，不入库
2. **登录请求 POST 到站点根路径 `/`**（不是 `/ds`；浏览器用 `xhr.open("POST", "")` 解析为当前页），
   `Content-Type: application/json; charset=UTF-8`，body：
   ```json
   {"method":"do","login":{"password":"3UMirgYUcTefbwK"}}
   ```
   成功响应：`{"stok":"5106bbac9483fe12800fbbc2cafafd00","error_code":0}`
3. 后续请求 `POST /stok=<stok>/ds`，同 Content-Type：
   ```json
   {"method":"get","hosts_info":{"name":"host_info"}}
   {"method":"get","ip_mac_bind":{"name":"sys_arp"}}
   {"method":"get","network":{"name":"lan"}}
   {"method":"get","device_info":{"name":"info"}}
   ```

**端点地图（实测枚举：18 个模块 × 全部表名，43 个端点有数据）**

| 模块/表 | 用途 | 是否用于采集 |
|---|---|---|
| `hosts_info / host_info` | **当前在线主机**（mac/ip/hostname/type/ssid/up|down_speed/blocked） | ✅ 主源 |
| `hosts_info / device` | 用户在路由器上给设备起的**别名**（当前为空） | ✅ 名称富化 |
| `ip_mac_bind / sys_arp` | ARP 表（跨 WAN+LAN，含 hostname） | ✅ 补充 |
| `ip_mac_bind / user_bind` | IP-MAC 静态绑定（当前为空） | ➖ 富化 |
| `network / lan` | LAN 网段（**每次刷新，地址可能变**） | ✅ 判定子网 |
| `wireless / wlan_host_2g\|5g`、`ath0\|ath1` | SSID 配置（**含明文 Wi-Fi 密码，禁止落日志**） | ❌ |
| `upnpd / upnp_lease` | UPnP 端口映射，会暴露历史客户端 IP（**陈旧，非在线信号**） | ❌ |
| `system / clock_status` | 路由器时钟（`totalsec`） | ❌ 仅参考 |
| 其余 30+ | wan/ddns/firewall/ddns/guest/wds/route 等 | ❌ |

**关键优化：一次请求可并行取多个模块**（实测通过），采集每轮只需 **1 次 POST**：
```json
{"method":"get",
 "hosts_info":  {"table":"device", "name":"host_info"},
 "ip_mac_bind": {"name":"sys_arp"},
 "network":     {"name":"lan"}}
```
→ 响应同时含 `hosts_info{device[],host_info[]}`、`ip_mac_bind{sys_arp[]}`、`network{lan}`。

**数据样例（已验证）**

`host_info`（`type`: `0`=有线 `1`=主 Wi-Fi `2`=访客 Wi-Fi；`type>1` 或 `blocked=1` 时 UI 侧跳过）：
```json
{"mac":"3C-0A-F3-D3-54-43","ip":"192.168.99.101","hostname":"honor",
 "type":"1","ssid":"LMD326","up_speed":"0","down_speed":"0","blocked":"0"}
```
`sys_arp`（**字符串值为 URL 编码的 UTF-8，必须 decode**，`%e5%8c%bf...` = `匿名主机`，需视为"无主机名"）：
```json
{"ip":"192.168.1.220","mac":"4C-CC-6A-4A-EE-4A","hostname":"%e5%8c%bf%e5%90%8d%e4%b8%bb%e6%9c%ba","status":0}
```

**`host_info` 就是官方设备列表源**：`ParentControl.htm`（家长控制）用它填充设备下拉框 → 语义为"当前在线主机"。
实测连采 3 轮（间隔 30s，脚本 `/tmp/tp_collect.py`）结果完全一致：`.99` 子网仅 1 台（honor / 192.168.99.101 / Wi-Fi / SSID LMD326）。

**风控约束（实测）**：密码错误 10 次后锁定 20 分钟（`ESYSLOCKED`）。已消耗 5 次失败额度。
→ 设计上严禁运行时快速重试登录。

### 2.4 拓扑

```
                    Internet
                       |
              ASUS RT-AC54U  192.168.1.1/24   ← SSH 采集（ARP/neigh/leases）
                       |
        +--------------+--------------+------------------+
        |                             |                  |
   192.168.1.x 设备              本机 .220        TP-Link WDR5600
   （约 9 台，含 .4/.100/          （运行 lanwatch）   WAN 192.168.1.3:8888  ← HTTP 采集
     .210/.222/.224/                                 |
     .227/.229/.230）                        192.168.99.0/24 设备
                                              （.101 等，仅 TP-Link 可见）
```

---

## 3. 总体架构

```
┌─────────────────────────── 单进程 lanwatch (Go) ───────────────────────────┐
│                                                                            │
│  Collector (ticker 60s, single-flight)                                     │
│      ├── source.ASUS     (SSH: ip neigh / proc-net-arp / dnsmasq.leases)   │
│      ├── source.TPLink   (HTTP: hosts_info / sys_arp)                      │
│      └── (可扩展 source.Source 接口)                                        │
│                          │ []Observation                                   │
│                          ▼                                                 │
│  Presence Engine  ──归并多源证据──▶ 状态机(确认计数/抖动抑制)               │
│                          │ Event{online|offline|new|ip_changed|flapping}   │
│                          ▼                                                 │
│  Notify Engine ──策略匹配──▶ 去重/冷却/限流/聚合 ──▶ Channel{email|dingtalk}│
│                          │                                                 │
│                          ▼                                                 │
│  Store (SQLite WAL)  ────  devices / sessions / samples / rules / logs     │
│                          │                                                 │
│                          ▼                                                 │
│  HTTP API (chi) ──auth middleware──▶ /api/*                                │
│  Static  ──go:embed web/dist──▶ /*                                         │
└────────────────────────────────────────────────────────────────────────────┘
                          ▲
                   React SPA (Vite/TS/Tailwind)
```

**技术选型**

| 层 | 选型 | 理由 |
|---|---|---|
| 后端 | Go 1.26 + `chi/v5` | 单二进制、SSH/HTTP 客户端成熟、并发模型适合定时采集 |
| DB | SQLite（`modernc.org/sqlite`，纯 Go 无 CGO）＋ WAL | 单机内网工具，量级极小（见 §7 容量估算），零运维 |
| 迁移 | 自建：内嵌 `migrations/*.sql` + `PRAGMA user_version` | 避免额外依赖 |
| 前端 | React 19 + TS + Vite + Tailwind v4 + TanStack Query + react-router | 配置型 UI 交互多，SPA 体验更好；产物 `go:embed` 进二进制 |
| 图表 | 不引图表库，时间轴用绝对定位 div 自绘 | 甘特式区间图 CSS 即可，避免重型依赖 |
| 部署 | systemd 本机运行（`User=lanwatch`），无特权 | 抓取模式不需要 `CAP_NET_RAW` |

---

## 4. 数据采集层

### 4.1 适配器接口

```go
// internal/source
type Source interface {
    ID() string                                    // "asus-1", "tplink-1"
    Kind() string                                  // "asus-ssh" | "tplink-http"
    Collect(ctx context.Context) ([]Observation, error)
    Close() error
}

type Observation struct {
    MAC        string   // 统一大写、连字符分隔
    IP         string
    Hostname   string
    LinkType   LinkType // Wired | Wifi2G | Wifi5G | Guest | Unknown
    SSID       string
    Present    bool     // 该源本轮是否认为在线
    Confidence int      // 0-100，证据强度
    SourceID   string
}
```

多源证据归并：同一 MAC 被多源观测时取 `Confidence` 最高的属性，`Present` 取 **任一源为真**。
（例：TP-Link 自身 MAC 同时出现在 ASUS 的 ARP 和 TP-Link 的 sys_arp 中。）

### 4.2 ASUS SSH 适配器

**会话管理**：长连接复用 + 30s keepalive；断线指数退避重连（1s→2s→4s…上限 30s）。
**主机密钥**：首次连接记录 SHA256 指纹到 DB（`source_host_keys`），后续变更则拒绝连接并告警（防中间人）。

**每轮执行**：单次 `exec`，脚本内置于二进制（完整脚本见**附录 B**）。分两种轮次：

| 轮次 | 频率 | 动作 | 实测耗时 |
|---|---|---|---|
| 常规轮 | 每轮 | 只对上一轮**非 REACHABLE** 的 IP 复核 ping（通常 0–5 个）+ 读 neigh/leases | < 1s |
| 发现轮 | 每 10 轮 | 全 /24 扫描（64 并发，分 4 批 `wait`） | **5s**（实测，命中 13 台） |

**环境限制（实测）**：路由器是 BusyBox **v1.24.2**，
`xargs` **不支持 `-P`**（并发只能用 shell 后台任务分批次），`command -v` 不存在，`seq`/`$(( ))` 可用。
→ 发现轮超时设 **15s**，常规轮 10s。

**判定与证据强度**

| 证据 | Confidence |
|---|---|
| `ip neigh` = `REACHABLE` | 90 |
| 路由器侧 ping 通 | 85 |
| `ip neigh` = `DELAY`/`PROBE` | 60 |
| `ip neigh` = `STALE` | 40 |
| 仅出现在 `/proc/net/arp` | 25 |
| 仅 dnsmasq 租约未过期 | 15 |

`Present = Confidence >= 70`（可配）。

**解析规则（实测踩坑）**
1. **丢弃无 `lladdr` 的条目**：ping 不存在的 IP 会让内核生成 `INCOMPLETE` 邻居项，不过滤就会凭空多出一堆设备。
2. `.240`、`.80` 只在全段扫描命中、不在 `ip neigh` 里 → 两个输出要**取并集**，不能只读 neigh。
3. dnsmasq 租约行格式 `<lease秒数> <MAC> <IP> <hostname> <client-id>`，另有 `duid` 行需跳过。

**关键：为什么不能只看 ARP**
路由器邻居表存在 STALE 残留（设备离开后仍保留数分钟），直接判在线会**漏报离线**。
因此默认策略 = 先读 `ip neigh`，对非 REACHABLE 的条目**在路由器上补一发 ping 复核**；
只有复核失败才计入 miss。全段 `ping` 仅在"新设备发现轮"执行，常态开销 ≈ 0。

### 4.3 TP-Link HTTP 适配器

**会话管理**：登录一次拿 `stok` 后复用；`error_code == -40401`（未认证）时**退避后重登**，
且重登前先判断 `data.code` 是否为 `ESYSLOCKED(-40404)`，锁定则**停止重试并告警**（见 §6.4 风控、附录 A 错误码表）。
连续失败退避：60s → 120s → 300s（封顶）。

**每轮只需 1 次 POST**（实测支持多模块并行查询），复用同一 `stok`，超时 8s：
```json
{"method":"get",
 "hosts_info":  {"table":"device", "name":"host_info"},
 "ip_mac_bind": {"name":"sys_arp"},
 "network":     {"name":"lan"}}
```

**解析与证据强度**

1. 先用 `network.lan.ipaddr` 推出自身 LAN 前缀（**每轮刷新**，路由器改网段时自动跟随）
2. `hosts_info.host_info` → 主源。IP 落在 LAN 前缀内 → `Present=true, Confidence=85`；
   落在外侧（WAN 侧被反向观测到的设备）→ `Confidence=20`，**不作为在线证据**
3. `hosts_info.device` → 用户自定义别名，优先于 `hostname`
4. `ip_mac_bind.sys_arp` → 仅用于补齐未出现在 `host_info` 的 LAN 侧条目（`Confidence=60`），
   WAN 侧条目 `Confidence=25` 且**不参与在线判定**（交给 ASUS 源，避免越权判定）
5. `hostname == "匿名主机"`（decode 后）视为无主机名，置空
6. `blocked=1` 的设备仍算在线，但打 `blocked` 标记

**`sys_arp` 不可用作在线判据**：实测它只含 5 条且陈旧（WAN 侧仅 3 台，而 ASUS 实际有 9 台），
与 `host_info` 不等价。

### 4.4 采集轮次与失败语义（关键）

- 单飞：上一轮未结束则跳过本轮并记录 `skipped`
- 每源独立 goroutine，超时：SSH 10s / HTTP 8s
- **采集失败 ≠ 设备离线**：某源连续失败 ≥3 轮 → 标记源 `unhealthy`，该源观测**不计入判定**，
  设备状态转 `unknown`（而非 `offline`），并触发"采集源异常"告警
- 所有时间戳**以采集主机时钟为准**：两台路由器与主机时钟可能不同步，绝不采用路由器本地时间

---

## 5. Presence Engine（在线状态机）

### 5.1 参数（默认，均可配）

| 参数 | 默认 | 说明 |
|---|---|---|
| `interval` | 60s | 采集周期 |
| `miss_threshold` | 2 | 连续 N 轮未观测到 → 判定 offline |
| `hit_threshold` | 1 | 连续 N 轮观测到 → 判定 online |
| `session_merge_gap` | 120s | 离线间隔 < 该值则合并为同一在线区间（防止时间轴碎裂） |
| `min_offline_notify_sec` | 120s | 离线持续不足该值不发通知 |
| `flap.window` | 10m | 抖动检测窗口 |
| `flap.threshold` | 4 | 窗口内状态切换 ≥ N 次 → flapping |
| `flap.suppress_sec` | 30m | 进入 flapping 后抑制通知时长 |
| `discover_sweep_every` | 10 | 每 N 轮做一次全段发现扫描 |

### 5.2 状态机

```
                 hit_threshold 满足            miss_threshold 满足
      (unknown) ─────────────────▶ online ◀──────────────── offline
                                     │                         │
                                     └── 抖动检测（滑动窗口）───┘
                                                │
                                          flap ≥ threshold
                                                ▼
                                            flapping（抑制通知）
```

- `unknown` 仅在系统启动或源异常时出现，`unknown` **永不触发通知**
- 区间合并：`offline` 后 `session_merge_gap` 内重新 `online` → 关闭的 session 重新打开，不产生新区间
- 事件类型：`online` / `offline` / `new_device` / `ip_changed` / `hostname_changed` / `flapping`

### 5.3 新设备处理（用户决策：自动纳入 + 通知）

- 未登记 MAC 首次出现 → 自动建设备记录，`status=new`，写入 `first_seen`
- 触发 `new_device` 事件，按策略通知（含 MAC、IP、hostname、厂商、来源路由器、SSID）
- 前台提供"忽略该 MAC"/"合并到已有设备"操作
- 抑制噪音：同一 MAC 首次出现后 24h 内不重复发 new_device；随机 MAC 设备（OUI 随机位 + 无 hostname）默认打 `likely_random_mac` 标记，可在策略中排除

---

## 6. 通知引擎

### 6.1 渠道

| 渠道 | 配置项 | 说明 |
|---|---|---|
| `email` | host/port/username/password/from/to[]/cc[]/tls(`ssl`\|`starttls`\|`none`)/subject 模板 | 密码存 DB 时用 AES-GCM 加密（密钥来自 `LANWATCH_SECRET` 或 keyring），API 永不回传明文 |
| `dingtalk` | webhook URL、secret（加签）、at_mobiles[]、at_all、msgtype(`markdown`\|`text`) | 加签：`sign = urlencode(base64(HMAC_SHA256(ts+"\n"+secret)))`，附 `&timestamp=` |
| `webhook`（扩展位） | URL、method、headers、body 模板 | 为后续企业微信/飞书/Telegram 预留 |

渠道支持"测试发送"按钮，保存前先验证连通性。

### 6.2 策略模型

两套机制并存，统一在引擎里展开为候选规则：

**(a) 设备订阅（对应需求"选设备 → 选渠道"）**
```
device_channel_subscriptions(device_mac, channel_id, events[])
```
语义：该设备的这些事件发给这些渠道。UI 上就是设备行的渠道多选 + 事件勾选。

**(b) 通知规则（细粒度策略）**
```go
type Rule struct {
    ID       int64
    Name     string
    Enabled  bool
    Scope    Scope     // All | Devices[] | Tag[] | NewDevices
    Events   []EventType
    ChannelIDs []int64
    MinOfflineSec int  // 仅 offline 事件：持续多久才告警
    QuietHours  [][2]int  // 免打扰时段
    CooldownSec int       // 同设备同规则同事件冷却
    DigestSec   int       // 0=立即；>0 则窗口内聚合为一条
}
```

**匹配顺序**：设备订阅 → 规则 → 全局默认规则。命中即发（不叠加重复发送）。

### 6.3 发送管控

- **去重**：`(device, rule, event)` 维度冷却（默认 300s）
- **限流**：钉钉官方每机器人 20 条/分钟 → 引擎内置令牌桶（默认 15/min/渠道），超限入队并在窗口结束时合并为一条摘要
- **聚合**：`digest_sec > 0` 时，窗口内事件合并为一条 markdown 列表
- **重试**：失败按 30s / 2m / 10m 重试 3 次，仍失败记 `notify_log(status=failed)` 并在 UI 标红
- **审计**：所有发送写 `notify_logs`（含渲染后的 payload、错误、耗时）

### 6.4 风控（针对实测到的约束）

- TP-Link 登录失败计数持久化；已失败 ≥7 次时**禁止自动重登**，改为告警等待人工处理
- `ESYSLOCKED` 响应 → 立即置该源为 `locked`，停止所有登录尝试，恢复时间取 20min 后
- ASUS SSH 失败退避，避免对路由器 httpd/sshd 造成压力

### 6.5 消息模板

Go `text/template`，内置变量 `{{.Device.Name}} {{.Device.IP}} {{.Device.MAC}} {{.Event.Type}} {{.Event.At}} {{.Duration}}` 等。
钉钉默认 markdown 示例：

```
### 设备离线告警
- 设备：{{.Device.Name}}（{{.Device.IP}} / {{.Device.MAC}}）
- 事件：离线，已持续 {{.Duration}}
- 上次在线：{{.LastSeen}}
- 来源：{{.SourceName}}
```

---

## 7. 数据模型（SQLite）

```sql
-- 设备
CREATE TABLE devices (
  mac            TEXT PRIMARY KEY,          -- 大写连字符
  name           TEXT NOT NULL DEFAULT '',  -- 用户别名
  hostname       TEXT NOT NULL DEFAULT '',
  vendor         TEXT NOT NULL DEFAULT '',  -- OUI 厂商
  current_ip     TEXT,
  link_type      TEXT NOT NULL DEFAULT 'unknown',
  ssid           TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL DEFAULT 'new',  -- new|tracked|ignored
  likely_random_mac INTEGER NOT NULL DEFAULT 0,
  first_seen     INTEGER NOT NULL,
  last_seen      INTEGER NOT NULL,
  notes          TEXT NOT NULL DEFAULT ''
);

-- 在线区间（时间轴数据源）
CREATE TABLE presence_sessions (
  id          INTEGER PRIMARY KEY,
  mac         TEXT NOT NULL REFERENCES devices(mac),
  start_at    INTEGER NOT NULL,
  end_at      INTEGER,                    -- NULL = 进行中
  duration_s  INTEGER,                    -- 关闭时回填
  end_reason  TEXT                        -- offline|merged|flapping|source_lost
);
CREATE INDEX idx_sessions_mac_time ON presence_sessions(mac, start_at);

-- 每轮原始采样（短周期保留）
CREATE TABLE probe_samples (
  id         INTEGER PRIMARY KEY,
  round_id   INTEGER NOT NULL,
  mac        TEXT NOT NULL,
  ip         TEXT,
  present    INTEGER NOT NULL,
  confidence INTEGER NOT NULL,
  source_id  TEXT NOT NULL
);
CREATE INDEX idx_samples_round ON probe_samples(round_id);

CREATE TABLE probe_rounds (
  id          INTEGER PRIMARY KEY,
  started_at  INTEGER NOT NULL,
  finished_at INTEGER,
  ok          INTEGER,
  skipped     INTEGER NOT NULL DEFAULT 0,
  error       TEXT
);

-- IP 变更历史
CREATE TABLE device_ip_history (
  mac TEXT NOT NULL, ip TEXT NOT NULL,
  first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL,
  PRIMARY KEY (mac, ip)
);

-- 渠道 / 规则 / 订阅
CREATE TABLE channels (
  id INTEGER PRIMARY KEY, type TEXT NOT NULL, name TEXT NOT NULL,
  config_enc BLOB NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL
);
CREATE TABLE notify_rules (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  scope TEXT NOT NULL, scope_ref TEXT NOT NULL DEFAULT '',   -- JSON
  events TEXT NOT NULL,                                       -- JSON array
  min_offline_sec INTEGER NOT NULL DEFAULT 120,
  quiet_hours TEXT NOT NULL DEFAULT '[]',
  cooldown_sec INTEGER NOT NULL DEFAULT 300,
  digest_sec INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL
);
CREATE TABLE rule_channels (rule_id INTEGER NOT NULL, channel_id INTEGER NOT NULL, PRIMARY KEY(rule_id, channel_id));
CREATE TABLE device_channel_subscriptions (
  mac TEXT NOT NULL, channel_id INTEGER NOT NULL, events TEXT NOT NULL, PRIMARY KEY(mac, channel_id)
);

CREATE TABLE notify_logs (
  id INTEGER PRIMARY KEY, rule_id INTEGER, channel_id INTEGER NOT NULL,
  mac TEXT, event TEXT NOT NULL, status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0, error TEXT,
  payload TEXT, created_at INTEGER NOT NULL, sent_at INTEGER
);
CREATE INDEX idx_nlog_time ON notify_logs(created_at);

-- 采集源健康
CREATE TABLE source_state (
  id TEXT PRIMARY KEY, kind TEXT NOT NULL,
  healthy INTEGER NOT NULL DEFAULT 1, last_ok_at INTEGER,
  consecutive_fail INTEGER NOT NULL DEFAULT 0,
  last_error TEXT, locked_until INTEGER, host_key TEXT
);

-- 认证
CREATE TABLE users (
  id INTEGER PRIMARY KEY, username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'admin',
  created_at INTEGER, last_login_at INTEGER
);
CREATE TABLE sessions (
  token TEXT PRIMARY KEY, user_id INTEGER NOT NULL,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, last_seen_at INTEGER
);

-- 每日汇总（用于统计与长周期图表）
CREATE TABLE device_daily_stats (
  mac TEXT NOT NULL, day TEXT NOT NULL,        -- YYYY-MM-DD
  online_s INTEGER NOT NULL, sessions INTEGER NOT NULL, flaps INTEGER NOT NULL,
  PRIMARY KEY (mac, day)
);
```

**容量估算**（20 台设备，60s 周期）
- `probe_samples`：20 × 1440 = 28,800 行/天 → 7 天 ≈ 20 万行，约 15 MB
- `presence_sessions`：约 100 行/天 → 180 天 ≈ 1.8 万行
- 全库半年 < 50 MB，SQLite 完全无压力

**保留策略**：`probe_samples` 7 天 / `notify_logs` 30 天 / `presence_sessions` 180 天 / `device_daily_stats` 永久。
每日 03:10 执行清理（分批删除避免长事务）。

---

## 8. 认证与权限

| 项 | 设计 |
|---|---|
| 初始账号 | 启动且 `users` 为空时创建：`LANWATCH_ADMIN_PASSWORD` 指定，否则随机生成并写入 `/var/lib/lanwatch/initial_admin.txt`（0600）+ 打印到日志 |
| 密码哈希 | bcrypt cost 12 |
| 会话 | 32 字节随机 token，SHA256 后存 DB；Cookie `HttpOnly` + `SameSite=Lax`（HTTPS 时加 `Secure`）；有效期 12h，活跃滚动续期 |
| 限流 | 登录失败：同 IP 10 次/5min、同账号 10 次/5min；失败写审计日志 |
| 中间件 | `/api/*` 全需认证，白名单仅 `/api/auth/login`、`/api/health` |
| 前端 | react-router 守卫，401 跳登录 |
| 传输 | 默认监听 `127.0.0.1:8080`；需内网直连时改 `listen_addr: 0.0.0.0:8080`，文档建议前置 Caddy/Nginx 终止 TLS |

仅 `admin` 可写；`viewer` 只读（后续如需）。

---

## 9. API 设计（REST / JSON）

```
POST   /api/auth/login            {username,password} -> Set-Cookie
POST   /api/auth/logout
GET    /api/auth/me

GET    /api/devices                        ?q=&status=&online=&sort=
PATCH  /api/devices/:mac                   {name,notes,status}
GET    /api/devices/:mac/sessions          ?from=&to=
GET    /api/devices/:mac/subscriptions
PUT    /api/devices/:mac/subscriptions
POST   /api/devices/merge                  {from:[mac],to:mac}

GET    /api/timeline                       ?from=&to=&macs=   -> 区间数据（时间轴）
GET    /api/stats/overview                 今日在线/新设备/告警数/源健康
GET    /api/stats/daily                    ?mac=&days=

GET    /api/sources                        源列表与健康
POST   /api/sources/:id/test               立即采集一次

GET/POST/PUT/DELETE /api/channels
POST   /api/channels/:id/test               {sample_event}

GET/POST/PUT/DELETE /api/rules
GET    /api/notifications                  发送日志（分页/筛选）
POST   /api/notifications/:id/retry

GET/PUT /api/settings                      采集间隔、保留策略、抖动参数
GET    /api/health                         无鉴权，进程存活
```

---

## 10. 前端设计

### 10.1 页面

| 路由 | 页面 | 要点 |
|---|---|---|
| `/login` | 登录 | 简洁表单，错误提示，登录后跳原路径 |
| `/` | 总览 | 卡片：当前在线 / 今日新设备 / 近 1h 事件 / 源健康；下方最近事件流 + 迷你时间轴（Top N 设备） |
| `/timeline` | **在线区间时间轴** | 核心页：行 = 设备，X 轴 = 时间（6h/24h/7d/自定义），条 = 在线区间；支持搜索、按在线率排序、只看已订阅、悬浮 tooltip、点击条查看详情、时区固定本地 |
| `/devices` | 设备 | 表格：名称/MAC/IP/厂商/最近上线/状态/今日在线率/订阅渠道；支持多选批量订阅渠道、批量忽略、合并 |
| `/channels` | 通知渠道 | 邮件 / 钉钉表单（密码与 secret 掩码存储），测试发送，启用开关 |
| `/rules` | 通知策略 | 规则列表 + 编辑器：设备/标签多选 → 事件多选 → 渠道多选 → 免打扰/冷却/聚合 |
| `/notifications` | 通知日志 | 时间/mac/事件/渠道/状态/错误，支持重试 |
| `/settings` | 设置 | 采集参数、保留策略、源配置（只读，含健康状态）、用户管理、导出 CSV |

### 10.2 时间轴实现要点

不引图表库，纯 CSS：

```tsx
<div className="relative h-6">           // 每行
  {sessions.map(s => (
    <div className="absolute h-4 rounded"
         style={{
           left:  `${(s.start - from) / range * 100}%`,
           width: `${Math.max((s.end - s.start) / range * 100, 0.2)}%`,
         }} />
  ))}
</div>
```

- 数据由 `GET /api/timeline?from&to` 一次返回，前端不分页（设备 <200）
- 颜色：正常在线蓝、抖动黄、短时离线（<merge_gap）灰、当前进行中绿色呼吸
- 7 天视图下区间过密时按小时降采样（后端返回前先聚合到 5min 桶）

---

## 11. 配置与部署

### 11.1 配置文件 `/etc/lanwatch/config.yaml`（0600）

```yaml
listen_addr: 127.0.0.1:8080
public_url: https://lanwatch.220.toop.ai   # 经反代时必填，用于通知/确认链接前缀
db_path: /var/lib/lanwatch/lanwatch.db
secret: ${LANWATCH_SECRET}          # 渠道凭据加密密钥，env 注入

collect:
  interval: 60s
  discover_sweep_every: 10
  ssh_timeout: 10s
  http_timeout: 8s

presence:
  miss_threshold: 2
  hit_threshold: 1
  session_merge_gap: 120s
  min_offline_notify_sec: 120s
  flap: { window: 10m, threshold: 4, suppress: 30m }

retention:
  probe_samples_days: 7
  sessions_days: 180
  notify_logs_days: 30

sources:
  - id: asus-1
    kind: asus-ssh
    host: 192.168.1.1:22
    username: admin
    password: ${ASUS_PASSWORD}
    bridge: br0
    subnets: [192.168.1.0/24]
    confirm_ping: true          # 对非 REACHABLE 条目在路由器上复核

  - id: tplink-1
    kind: tplink-http
    base_url: http://192.168.1.3:8888
    password: ${TPLINK_PASSWORD}
    subnets: [192.168.99.0/24]
```

### 11.2 systemd

`/etc/systemd/system/lanwatch.service`：

```ini
[Unit]
Description=LAN device presence monitor
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=lanwatch
Group=lanwatch
ExecStart=/opt/lanwatch/lanwatch --config /etc/lanwatch/config.yaml
Restart=always
RestartSec=5s
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/lanwatch
EnvironmentFile=/etc/lanwatch/lanwatch.env

[Install]
WantedBy=multi-user.target
```

**注意**：抓取模式**不需要** `CAP_NET_RAW` / `NET_ADMIN`，因此可完整使用 `ProtectSystem=strict` 等加固项。
数据库备份：`sqlite3 /var/lib/lanwatch/lanwatch.db ".backup /var/backups/lanwatch-$(date +%F).db"`，每日 cron。

### 11.3 Docker + Traefik 部署（推荐：非常用端口 + 次级域名）

目标：应用本身只监听**非常用内部端口**（容器 `9123`），对外统一由本机 Traefik v3 经 **HTTPS 次级域名** `lanwatch.220.toop.ai`（entrypoint `websecure`/443，Cloudflare DNS-01 证书）反代，数据库持久化在项目内 `./data`。

**前置（复用 `/home/ider/docker_job/traefik-compose` 既有栈）**
- 外部网络 `web` 已存在（`docker network create web` 或由 traefik 栈创建）。
- Traefik 已配置 `entrypoints.websecure` + `certificatesResolvers.cloudflare`（DNS-01），`providers.docker.network=web`、`exposedByDefault=false`。
- 域名 `lanwatch.220.toop.ai`（或 `*.220.toop.ai` 通配）DNS 记录指向本机公网 IP。

**文件**
- `Dockerfile`：多阶段（node 构建 `web/dist` → golang 构建二进制 → alpine 运行期），`//go:embed all:dist` 已内嵌前端。
- `docker-compose.yml`：服务 `lanwatch` 挂 `web` 外部网络，仅用 Traefik label 暴露，无对外 `ports` 映射；`env_file: .env` 注入凭据；卷 `./data:/data` 持久化。
- `deploy/lanwatch.yaml`：生产配置，`listen_addr: 0.0.0.0:9123`、`public_url: https://lanwatch.220.toop.ai`、`db_path: /data/lanwatch.db`，所有密码以 `${...}` 占位符出现（真实值在 `.env`，已被 `.gitignore` 忽略）。

**关键 Traefik label**
```yaml
- "traefik.enable=true"
- "traefik.http.routers.lanwatch.rule=Host(`lanwatch.220.toop.ai`)"
- "traefik.http.routers.lanwatch.entrypoints=websecure"
- "traefik.http.routers.lanwatch.tls.certresolver=cloudflare"
- "traefik.http.services.lanwatch.loadbalancer.server.port=9123"
```

**运行**
```bash
cp .env.example .env          # 填入 LANWATCH_SECRET / ASUS_PASSWORD / TPLINK_PASSWORD / LANWATCH_ADMIN_PASSWORD

# 本机无法访问 Docker Hub（golang/node 基础镜像拉不下来），故先在宿主机预构建
# 1) 前端产物（需 web/node_modules 已安装）
cd web && npm run build && cd ..
# 2) Go 静态二进制（含内嵌 web/dist，CGO 关掉以便 alpine 运行）
CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o build/lanwatch ./cmd/lanwatch

# 3) 打包并启动（Dockerfile 仅把 build/lanwatch 拷入 alpine:latest）
docker compose up -d --build
```
`docker compose logs -f lanwatch` 看启动；容器健康后 Traefik 自动签发证书并注册路由。
（改了 Go 代码后，重复步骤 2+3 即可；`build/lanwatch` 已被 `.gitignore` 忽略，不会入库。）

**为何用非常用端口 + 反代**：直连 `:8080` 既暴露明文也占用常用端口；改为内部 `9123` + Traefik 终止 TLS，既收敛攻击面又统一证书管理。注意 `public_url` 必须设为对外地址，否则通知邮件里的"确认订阅"链接会是内网地址、无法点击。

---

## 12. 可观测性与自监控

- `/api/health` 返回进程与最近一轮采集状态
- 源健康写入 `source_state`，UI 顶部常驻指示灯
- **采集源异常本身要告警**：连续 3 轮失败 → 走"系统通知"渠道（默认所有启用渠道）
- 结构化日志（JSON）到 stdout，由 journald 收集
- 关键指标（可选 Prometheus `/metrics`）：`lanwatch_round_duration_seconds`、`lanwatch_devices_online`、`lanwatch_notify_sent_total`、`lanwatch_source_failures_total`

---

## 13. 测试策略

| 层 | 方法 |
|---|---|
| source/ASUS | 把命令执行抽象为 `SSHRunner` 接口，测试注入 fake runner 返回固定 `ip neigh` 文本 → 覆盖解析与状态判定 |
| source/TPLink | `httptest` 起假服务，回放实测抓到的真实响应样本（含 URL 编码 hostname）；额外测试 `orgAuthPwd` 编码与 `-40404` 锁定响应处理 |
| 协议回归 | `scripts/tplink_probe.py` 保留为活文档：改适配器前后各跑一次 `devices`，输出应一致 |
| presence | 表驱动：构造观测序列 → 断言状态机、区间合并、抖动抑制 |
| notify | 假 channel 收集消息；验证冷却、限流、聚合、重试 |
| api | `httptest` + 临时 DB；验证鉴权中间件与限流 |
| e2e | 手工：断开一台 `.99` 设备与一台 `.1` 设备，观察时间轴与通知 |

---

## 14. 里程碑

> 文件级任务分解与对应产出见 **§16**。

| 阶段 | 内容 | 验收 |
|---|---|---|
| M0 | 骨架：配置、SQLite+迁移、collector 调度、ASUS SSH 适配器、presence 状态机、鉴权 API | `192.168.1.0/24` 设备能正确进出在线区间，CLI 打印事件 |
| M1 | 前端 MVP：登录、设备列表、时间轴 | 能看到 24h 在线区间 |
| M2 | TP-Link 适配器 + 多源归并 | `192.168.99.0/24` 设备出现；验证 `hosts_info` 在线语义 |
| M3 | 通知引擎（邮件+钉钉）、策略 UI、设备↔渠道订阅 | 拔网线 → 2 分钟后钉钉+邮件到；抖动不刷屏 |
| M4 | 通知日志/重试/限流、源健康告警、保留策略、systemd 部署、README | 7×24 稳定运行 |
| M5 | 打磨：批量操作、CSV 导出、暗色模式、Prometheus metrics | — |

---

## 15. 风险与开放问题

| # | 风险 | 应对 |
|---|---|---|
| R1 | TP-Link 登录 10 次失败锁 20 分钟（**已消耗 5 次**） | 运行时退避 + 失败计数持久化 + 锁定即停；凭据变更仅在设置页手动触发一次验证 |
| R2 | `host_info` 是否为严格"当前在线"语义（设备离线后是否立刻消失） | 已确认它是官方设备列表源（`ParentControl.htm` 用它填设备下拉框）且 3 轮稳定；**但"离线即消失"仍待 M2 用真实断开验证**：手动关掉 honor 手机 Wi-Fi，观察 2 轮内是否消失。若残留，则改为"连续 N 轮未出现即判离线"并调大 `miss_threshold` |
| R8 | `wireless/wlan_host_*`、`ath0/ath1` 返回体含**明文 Wi-Fi 密码** | 采集器绝不请求这些表；日志/错误上报统一脱敏 |
| R3 | ASUS 邻居表 STALE 残留导致漏报离线 | 路由器侧 ping 复核（默认开）；`miss_threshold=2` 双保险 |
| R4 | 手机随机 MAC 造成新设备噪音 | `likely_random_mac` 标记 + 策略可排除 + 24h 去重 |
| R5 | 两台路由器与主机时钟不同步 | 全部时间戳以主机为准 |
| R6 | ASUS 固件升级后 SSH/Web 行为变化 | 源健康告警 + 适配器可插拔；升级后跑一次冒烟 |
| R7 | 每分钟 SSH exec 对路由器负载 | 长连接复用 + 常态只对非 REACHABLE 复核；后期可降频到 2min |
| Q1 | 是否需要监控路由器自身上下线（.1 / .3 本身）？ | 建议纳入：路由器即设备，同样适用在线区间与通知（M0 顺带实现） |
| Q2 | 是否需要 HTTPS？ | 内网建议前置 Caddy；若直连 HTTP，强制强密码 + 登录限流 |

---

## 16. 实施任务分解

### 16.1 目录结构

```
lan_discovery/
├── cmd/lanwatch/main.go            启动、信号、优雅退出
├── internal/
│   ├── config/config.go            YAML + ${ENV} 展开 + 校验
│   ├── store/                      sqlite.go / migrations/*.sql / 各仓储
│   ├── source/
│   │   ├── source.go               Source、Observation 接口
│   │   ├── scheduler.go            ticker + 单飞 + 退避 + 轮次记录
│   │   ├── asus/                   SSH 适配器（runner.go 可注入，便于测试）
│   │   └── tplink/                 HTTP 适配器
│   ├── presence/                   engine.go / state.go / flap.go
│   ├── notify/
│   │   ├── engine.go  dedup.go  ratelimit.go  template.go
│   │   └── channel/                email.go / dingtalk.go / webhook.go
│   ├── api/                        router.go / auth.go / middleware.go / 各 handler
│   └── web/embed.go                go:embed all:../../web/dist
├── web/                            React SPA 源码
├── scripts/tplink_probe.py         已验证的协议参考实现（排障 + 回归比对）
├── deploy/                         lanwatch.service / install.sh / backup.sh
└── docs/DESIGN.md
```

### 16.2 任务清单

| 阶段 | # | 任务 | 主要文件 | 验收标准 |
|---|---|---|---|---|
| **M0** | 1 | 项目骨架 + 配置 | `cmd/`, `internal/config` | 缺凭据/配置非法时启动即失败并明确报错 |
| | 2 | 存储层 + 内嵌迁移 | `internal/store` | 迁移幂等，重复启动无副作用；WAL 已开 |
| | 3 | 采集调度器 | `internal/source/scheduler.go` | 单飞生效（慢轮不叠加）；源超时生效；每轮落 `probe_rounds` |
| | 4 | ASUS SSH 适配器 | `internal/source/asus` | 单测用假 runner 喂实测样本，覆盖：INCOMPLETE 过滤、STALE、租约行解析、`duid` 跳过 |
| | 5 | Presence 引擎 | `internal/presence` | 表驱动覆盖：连续 miss→offline、merge gap 合并同区间、抖动阈值抑制 |
| | 6 | 鉴权 + API 骨架 | `internal/api/auth.go` | 未登录 `/api/*` → 401；登录失败限流生效 |
| **M1** | 7 | 前端脚手架 + 登录页 | `web/` | 登录后可刷新保持会话 |
| | 8 | 设备列表 + 时间轴 | `web/src/pages` | 24h 区间与 DB 中 `presence_sessions` 完全一致 |
| | 9 | 静态资源内嵌 | `internal/web` | 单二进制无需外部文件即可提供页面 |
| **M2** | 10 | TP-Link 适配器 | `internal/source/tplink` | `httptest` 回放真实响应；`-40404` 时立即停止重登 |
| | 11 | 多源归并 | `internal/source` | 同一 MAC（如 `.3` 自身）被两源观测只产生一个会话 |
| | 12 | **R2 现场验证** | — | 手动关掉 honor 手机 Wi-Fi → 2 轮内从 `host_info` 消失；否则按 §15 R2 调整 |
| **M3** | 13 | 渠道模型 + 凭据加密 | `internal/notify/channel` | 任何 GET 响应都不含 smtp 密码 / 钉钉 secret |
| | 14 | 邮件渠道 | `channel/email.go` | STARTTLS 与 SSL 两种模式各测一次真实发送 |
| | 15 | 钉钉渠道 | `channel/dingtalk.go` | 加签正确（对照官方算法）；令牌桶 15/min 生效 |
| | 16 | 策略 + 设备订阅 | `internal/api/rules.go` | 设备行勾选渠道后立即按该渠道发一条 |
| | 17 | 去重/冷却/限流/重试 | `internal/notify` | 单测覆盖：冷却期内不重发、限流后合并、失败重试 3 次 |
| **M4** | 18 | 通知日志页 + 重试 | `web/src/pages` | 失败条目可见错误原因，可手动重试 |
| | 19 | 源健康告警 | `internal/source` | 断网/改密码 → 3 轮内标记 unhealthy 并告警，设备转 `unknown` 不误报离线 |
| | 20 | 保留策略定时任务 | `internal/store` | 过期数据按策略删除，库体积不持续增长 |
| | 21 | systemd 部署 + 备份 | `deploy/` | `systemctl enable --now` 后开机自启；备份脚本可还原 |
| | 22 | README | `README.md` | 含安装、配置、排障三节 |
| **M5** | 23 | 批量操作 / CSV 导出 | — | 可导出设备与区间 |
| | 24 | Prometheus metrics | `internal/api` | 暴露 §12 的 4 个指标 |
| | 25 | 暗色模式 | — | — |

---

## 附录 A — TP-Link 协议速查

**三步流程**

1. 编码密码（参考实现 `scripts/tplink_probe.py:encode_password`）
   ```python
   KEY  = "RDpbLfCPsJZ7fiv"                      # 常量，硬编码在 slp-ajax.js
   DIC  = "yLwVl0zKqws7LgKPRQ84Mdt708T1qQ3Ha7xv3H7NyU84p21BriUWBU43odz3iP4rBL3cD02KZciX" \
          "TysVXiV8ngg6vL48rPJyAUw0HurW20xqxv9aYb4M9wK1Ae0wlro510qXeU07kV57fQMc8L6aLgML" \
          "wygtc0F10a0Dg70TOoouyFhdysuRMO51yY5ZlOZZLEal1h0t9YQW0Ko7oBwmCAHoic4HYbUyVeU3" \
          "sfQ1xtXcPcf1aT303wAQhv66qzW"            # 255 字符
   out  = "".join(DIC[(ord(KEY[i] if i < len(KEY) else chr(0xBB))
                       ^ ord(pwd[i] if i < len(pwd) else chr(0xBB))) % len(DIC)]
                  for i in range(max(len(KEY), len(pwd))))
   # DevPass123 -> t7zU2drULeefbwK（真实密码从环境变量 TPLINK_PASSWORD 读取，不硬编码）
   ```
2. 登录：`POST http://192.168.1.3:8888/`（**根路径，不是 `/ds`**），
   `Content-Type: application/json; charset=UTF-8`，
   body `{"method":"do","login":{"password":"<编码后>"}}`
   → `{"stok":"…","error_code":0}`
3. 取数：`POST /stok=<stok>/ds`，可一次查多模块：
   ```json
   {"method":"get",
    "hosts_info":  {"table":"device", "name":"host_info"},
    "ip_mac_bind": {"name":"sys_arp"},
    "network":     {"name":"lan"}}
   ```

**错误码**（取自 `/web-static/language/cn/error.js` + 实测）

| 值 | 常量 | 含义 | 处理 |
|---|---|---|---|
| `0` | — | 成功 | — |
| `-40209` | `EINVARG` | 参数错误（模块/表不存在、body 形状不对） | 当作该表不可用，跳过 |
| `-40401` | `EUNAUTH` | 认证失败 / stok 失效 | **退避后重登一次** |
| `-40402` | `ECODEUNAUTH` | 验证码未认证 | 同上 |
| `-40404` | `ESYSLOCKED` | 系统锁定 | **停止一切登录尝试**，20min 后再试 |
| `-40405` | `ESYSRESET` | 已恢复出厂 | 告警，要求重新配置 |
| `-40407` | `ESYSCLIENTNORMAL` | 首次登录等其它情况 | 走登录流程 |

**必须遵守的约束**
- 表响应结构为 `[{"x_1":{…}}, {"x_2":{…}}]`，需剥掉外层序号键
- 字符串值是 **URL 编码的 UTF-8**，逐层 decode；decode 后为 `匿名主机` 视为无主机名
- **不要请求 `wireless/wlan_host_*`、`ath0/ath1`**：返回体含明文 Wi-Fi 密码
- 密码错 10 次锁 20 分钟 → 失败计数持久化，`-40404` 即停

---

## 附录 B — ASUS 采集脚本

单次 `exec` 执行，输出用分隔标记分段，便于 Go 侧解析。

**常规轮**（`$1` = 需复核的 IP，空格分隔）
```sh
for ip in $1; do
  (ping -c1 -W1 "$ip" >/dev/null 2>&1 && echo "P $ip") &
done
wait
echo "###NEIGH"
ip -4 neigh show dev br0
echo "###LEASES"
cat /tmp/dnsmasq.leases
```

**发现轮**（每 10 轮；64 并发分 4 批，实测 5s / 13 台命中）
```sh
BASE="${1%.*}"        # 192.168.1
i=1
while [ $i -le 254 ]; do
  j=0
  while [ $j -lt 64 ]; do
    n=$((i+j))
    if [ $n -le 254 ]; then
      (ping -c1 -W1 "$BASE.$n" >/dev/null 2>&1 && echo "P $BASE.$n") &
    fi
    j=$((j+1))
  done
  wait
  i=$((i+64))
done
echo "###NEIGH"
ip -4 neigh show dev br0
```

**解析要点**
- `###NEIGH` 行格式：`<ip> [lladdr <mac>] <STATE>`；**没有 `lladdr` 的（`INCOMPLETE`/`FAILED`）直接丢弃**
- `###LEASES` 行格式：`<lease秒数> <MAC> <IP> <hostname> <client-id>`；`duid` 开头的行跳过
- `P <ip>` 与 `###NEIGH` 的 REACHABLE 条目**取并集**——实测 `.80`、`.240` 只在扫描里出现

---

## 附录 C — 完整配置 schema

```yaml
# /etc/lanwatch/config.yaml  权限 0600
listen_addr: 127.0.0.1:8080        # 内网直连改 0.0.0.0:8080（建议前置 TLS）
db_path: /var/lib/lanwatch/lanwatch.db
secret: ${LANWATCH_SECRET}         # 渠道凭据加密密钥（AES-GCM），必填
log_level: info                    # debug|info|warn|error
log_format: json                   # json|text

collect:
  interval: 60s                    # 采集周期
  discover_sweep_every: 10         # 每 N 轮做一次全段扫描
  round_timeout: 10s               # 常规轮单源超时
  sweep_timeout: 15s               # 发现轮超时（实测全段 5s）
  ssh_keepalive: 30s
  ssh_timeout: 10s
  http_timeout: 8s

presence:
  miss_threshold: 2                # 连续未观测 N 轮判离线
  hit_threshold: 1
  session_merge_gap: 120s          # 小于该间隔的离线并入同一区间
  min_offline_notify_sec: 120s     # 离线不足该时长不通知
  confirm_ping: true               # 对非 REACHABLE 条目在路由器上复核
  flap:
    window: 10m
    threshold: 4
    suppress: 30m

retention:
  probe_samples_days: 7
  sessions_days: 180
  notify_logs_days: 30
  prune_at: "03:10"                # 每日执行时间

notify:
  rate_limit_per_channel: 15       # 条/分钟（钉钉官方上限 20）
  retry: [30s, 2m, 10m]
  cooldown_sec: 300                # 同设备同规则同事件冷却

sources:
  - id: asus-1
    kind: asus-ssh
    host: 192.168.1.1:22
    username: admin
    password: ${ASUS_PASSWORD}
    bridge: br0
    subnets: ["192.168.1.0/24"]
    host_key: ""                    # 为空则首次连接时信任并记录指纹
    enabled: true

  - id: tplink-1
    kind: tplink-http
    base_url: http://192.168.1.3:8888
    password: ${TPLINK_PASSWORD}
    subnets: ["192.168.99.0/24"]    # 留空则每轮从 network.lan 推导
    enabled: true
```

环境变量建议放 `/etc/lanwatch/lanwatch.env`（0600），由 systemd `EnvironmentFile` 引入。

---

## 附录 D — API 契约示例

```http
POST /api/auth/login
{"username":"admin","password":"…"}
→ 200 {"user":{"username":"admin","role":"admin"}}   Set-Cookie: lw_session=…; HttpOnly; SameSite=Lax
→ 401 {"error":"invalid credentials"}
→ 429 {"error":"too many attempts"}
```

```http
GET /api/devices?online=true&sort=last_seen
→ 200 {"devices":[
  {"mac":"3C:0A:F3:D3:54:43","name":"","hostname":"honor","ip":"192.168.99.101",
   "vendor":"Huawei","link_type":"wifi","ssid":"LMD326",
   "online":true,"last_seen":"2026-09-02T08:12:00+08:00",
   "first_seen":"2026-09-02T08:00:00+08:00","online_ratio_24h":0.98,
   "status":"new","flapping":false,
   "channels":[1,2]}
]}
```

```http
GET /api/timeline?from=2026-09-01T08:00&to=2026-09-02T08:00
→ 200 {"from":…,"to":…,"devices":[
  {"mac":"…","name":"honor","sessions":[{"start":…,"end":null,"flapping":false}]}
]}
```
> 7 天视图时后端先按 5 分钟桶聚合再返回，避免前端渲染上万个区间。

```http
POST /api/channels
{"type":"dingtalk","name":"钉钉-家庭群",
 "config":{"webhook":"https://oapi.dingtalk.com/robot/send?access_token=…",
           "secret":"SEC…","msgtype":"markdown","at_all":false,"at_mobiles":[]}}
→ 201 {"id":1,"type":"dingtalk","name":"钉钉-家庭群","enabled":true}
```
> `config` 中的 `secret` / smtp `password` 落库时加密，GET 返回 `"secret":"***"`。

```http
PUT /api/devices/3C:0A:F3:D3:54:43/subscriptions
{"channels":[1,2],"events":["online","offline","new_device"]}
→ 204

POST /api/channels/1/test
{"event":"offline","mac":"3C:0A:F3:D3:54:43"}
→ 200 {"ok":true,"elapsed_ms":412}
```

---

## 附录 E — 通知模板与加签

**钉钉加签**（官方算法）
```go
ts     := strconv.FormatInt(time.Now().UnixMilli(), 10)
mac    := hmac.New(sha256.New, []byte(secret))
mac.Write([]byte(ts + "\n" + secret))
sign   := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
target := webhook + "&timestamp=" + ts + "&sign=" + sign
```

**钉钉 markdown 默认模板**
```gotemplate
### {{if eq .Event.Type "offline"}}设备离线{{else if eq .Event.Type "online"}}设备上线{{else}}新设备接入{{end}}
- **设备**：{{.Device.NameOrHost}}（{{.Device.IP}} · {{.Device.MAC}}）
{{if .Device.Vendor}}- **厂商**：{{.Device.Vendor}}{{end}}
{{if .Device.SSID}}- **接入**：{{.Device.LinkTypeCN}} / {{.Device.SSID}}{{end}}
- **时间**：{{.Event.At.Format "2006-01-02 15:04:05"}}
{{if eq .Event.Type "offline"}}- **已离线**：{{.Duration}}（上次在线 {{.LastSeen.Format "15:04:05"}}）{{end}}
{{if .Event.Flapping}}- ⚠️ 该设备短时间内反复上下线，后续通知已临时抑制{{end}}
- **来源**：{{.SourceName}}
```

**邮件模板**
```gotemplate
Subject: [lanwatch] {{.Event.TypeCN}} — {{.Device.NameOrHost}}
Content-Type: text/html; charset=utf-8

<div style="font-family:-apple-system,Segoe UI,sans-serif">
  <h3 style="margin:0 0 12px">{{.Event.TypeCN}}：{{.Device.NameOrHost}}</h3>
  <table cellpadding="4" style="border-collapse:collapse;font-size:14px">
    <tr><td style="color:#888">MAC</td><td><code>{{.Device.MAC}}</code></td></tr>
    <tr><td style="color:#888">IP</td><td>{{.Device.IP}}</td></tr>
    <tr><td style="color:#888">接入方式</td><td>{{.Device.LinkTypeCN}}{{if .Device.SSID}} / {{.Device.SSID}}{{end}}</td></tr>
    <tr><td style="color:#888">发生时间</td><td>{{.Event.At.Format "2006-01-02 15:04:05"}}</td></tr>
    {{if eq .Event.Type "offline"}}
    <tr><td style="color:#888">持续离线</td><td>{{.Duration}}</td></tr>
    {{end}}
    <tr><td style="color:#888">采集源</td><td>{{.SourceName}}</td></tr>
  </table>
  <p style="color:#aaa;font-size:12px;margin-top:16px">
    本邮件由 lanwatch 自动发送 · <a href="{{.BaseURL}}/timeline">查看在线区间</a>
  </p>
</div>
```

**摘要（digest）模式**：`digest_sec > 0` 时，窗口内事件合并为一条，
标题 `[lanwatch] 最近 {{.Count}} 个上下线事件`，正文按设备分组列出 `<时间> <设备> <上线|离线>`。
