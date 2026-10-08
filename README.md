# lanwatch

通过路由器采集局域网设备状态，展示设备在线区间，并通过钉钉或邮件发送通知。

后端使用 Go + SQLite，前端使用 React + TypeScript + Vite。前端构建产物嵌入 Go 二进制，运行时无需 Node.js。

## 功能

- ASUS SSH 采集：读取邻居表、DHCP 租约，并在路由器侧执行 ping 复核及周期发现。
- TP-Link HTTP 采集：读取设备表、ARP 表与 LAN 配置，支持明文密码或已编码的登录密码。
- 多源在线状态判定、连续缺席确认、短暂断线区间合并及抖动标记。
- 设备搜索、在线筛选、固定服务器标记、在线率与时间轴。
- 钉钉加签机器人、SMTP 邮件、通知策略、单设备订阅与发送记录。
- 管理员登录，通知渠道凭据加密存储，SQLite 数据持久化。

适配器已在 ASUS RT-AC54U 与 TP-Link TL-WDR5600 的部署环境中使用。其他型号或固件是否兼容取决于 SSH 命令、数据格式及 HTTP 协议。

## 快速部署

以下命令在 Linux 上执行，需 Docker Engine、Docker Compose v2、Node.js/npm，以及访问 npm、Go 模块代理和镜像仓库的网络。前端已在 Node.js 24 环境构建；Go 工具链使用 `1.26.2` 容器。

### 1. 获取项目

```bash
mkdir -p ~/release
cd ~/release
git clone git@github.com:ider-zh/lanwatch.git
cd lanwatch
```

私有仓库需配置有访问权限的 GitHub SSH 密钥，也可以使用已配置认证的 HTTPS 地址克隆。

### 2. 配置凭据与采集源

```bash
cp .env.example .env
chmod 600 .env
openssl rand -hex 32
```

编辑 `.env`，将最后一条命令生成的随机值填入 `LANWATCH_SECRET`，并替换模板密码：

| 环境变量 | 用途 |
| --- | --- |
| `LANWATCH_SECRET` | 通知渠道凭据的加密密钥来源，需长期保留并随数据库备份 |
| `LANWATCH_ADMIN_PASSWORD` | 仅在首次启动、用户表为空时创建 `admin` 使用 |
| `ASUS_PASSWORD` | ASUS SSH 登录密码 |
| `TPLINK_PASSWORD` | TP-Link 登录密码；是否已编码由 YAML 配置决定 |

若首次启动时 `LANWATCH_ADMIN_PASSWORD` 留空，程序会生成随机密码并写入 `data/initial_admin.txt`。已有管理员后，修改此变量不会重置账户密码。

编辑实际挂载的 `deploy/lanwatch.yaml`：

- ASUS：确认 `host`、`username`、`bridge` 与 `subnets`，并启用路由器 SSH。
- TP-Link：确认 `base_url`，包括管理端口；`subnets` 对应路由器的 LAN 网段。
- 没有使用的采集源设置 `enabled: false`，至少保留一个可用源。
- `public_url` 填写实际访问地址；Docker 部署保留 `db_path: /data/lanwatch.db`。
- 凭据通过 `${ASUS_PASSWORD}`、`${TPLINK_PASSWORD}` 等环境变量占位符注入。

**TP-Link 编码方式：** 当前 `deploy/lanwatch.yaml` 的 `password_encoded` 为 `true`，表示 `.env` 中存放浏览器登录请求里的已编码 `login.password`，直接发送。若填写的是路由器明文管理密码，必须改成 `false`，由程序编码。`deploy/lanwatch.example.yaml` 默认采用明文模式。

### 3. 构建前端与后端

必须先构建前端，再构建 Go 二进制。当前 Dockerfile 仅打包 `build/lanwatch`，不会自动编译源码。

```bash
cd web
npm ci
npm run typecheck
npm run build
cd ..

docker run --rm \
  -e CGO_ENABLED=0 \
  -e GOPROXY=https://goproxy.cn,direct \
  -v "$PWD:/src" \
  -v lanwatch-go-mod:/go/pkg/mod \
  -v lanwatch-go-build:/root/.cache/go-build \
  -w /src golang:1.26.2-alpine \
  go build -trimpath -ldflags='-s -w' -o build/lanwatch ./cmd/lanwatch
```

`GOPROXY` 可按网络环境调整。容器编译产物默认对应构建主机架构。

### 4. 启动

```bash
docker network inspect web >/dev/null 2>&1 || docker network create web
docker compose config --quiet
docker compose up -d --build
docker compose ps
curl --noproxy '*' -fsS http://127.0.0.1:9123/api/health
```

默认自动合并 `docker-compose.override.yml`，将 `9123` 端口发布到主机所有接口。浏览器访问 `http://<主机IP>:9123`，使用 `admin` 和初始化密码登录。

主 Compose 文件保留了 Traefik 标签，但不会安装或启动 Traefik。若使用域名 HTTPS，需要自行准备反向代理、DNS 与证书，并调整文件中的域名及 Traefik 配置。仅使用基础 Compose 文件可取消附加文件中的主机端口发布：

```bash
docker compose -f docker-compose.yml up -d --build
```

此模式需要通过 `web` 网络上的反向代理访问。后续管理时应使用相同的 `-f` 参数。

## 配置通知

1. 登录后打开「通知渠道」，创建钉钉或邮件渠道并启用。
2. 钉钉填写完整 Webhook 和加签密钥，选择 Markdown 或文本消息；邮件填写 SMTP 服务及收件人信息。
3. 点击「测试」验证。此操作会实际发送消息。
4. 在「通知策略」选择设备范围、事件与通知渠道，或者在设备列表中单独订阅设备。
5. 在通知记录中检查发送结果。

只有渠道而没有匹配的策略或订阅，不会自动发送事件通知。策略未选择渠道时也不会发送。默认冷却为 300 秒，用于减少重复事件通知；它不是每条消息固定等待 5 分钟。

渠道配置通过应用 API 加密后存入 SQLite。不要把 Webhook、加签密钥或真实密码写入源码、测试样例及提交记录。

## 在线判定与通知时间

默认每 60 秒采集一次，确认在线需要 1 次命中，判定离线需要连续 2 次缺席。正常采集下，从设备消失到判定离线通常约 1～2 分钟，路由器缓存、设备休眠及采集故障可能影响判定。

离线消息中的时间含义：

- **时间**：程序确认离线的事件时间。
- **连续未检测到**：从本次首轮连续缺席到确认离线的时长，不是此前在线区间的长度，也不代表精确断网时间。
- **最后确认在线**：程序最近一次实际观测到在线的时间。重启后尚未取得在线观测时省略。

设备页的「最近上线」记录进入在线状态的时间，与通知中的「最后确认在线」不同。短暂离线后在默认 120 秒内恢复，在线区间可能合并。

设备以 MAC 标识。随机 MAC 改变后会被识别为新设备，当前不提供可靠的跨 MAC 身份合并。

## 日常维护

从项目根目录执行：

```bash
docker compose logs --tail 100 -f lanwatch
docker compose stop lanwatch
docker compose start lanwatch
```

修改 `.env` 后需要重建容器以重新注入变量，单纯 `restart` 不会读取新的环境变量：

```bash
docker compose up -d --force-recreate
```

修改 YAML 后可执行 `docker compose restart lanwatch`。更新代码后，先 `git pull --ff-only`，再重复前端、Go 构建与 `docker compose up -d --build`；仅构建 Docker 镜像不会更新已嵌入的前端。

数据位于 `data/`，包含 SQLite 数据库及可能存在的 WAL 文件。备份时停止服务后完整备份该目录，并单独安全保留 `.env` 与实际 YAML 配置，再启动服务。容器写入的数据可能属于 root，需要具备相应权限。恢复时配套使用原 `LANWATCH_SECRET`，否则已有通知渠道凭据无法解密。

## 测试与目录

前端构建完成后，可通过容器执行 Go 测试：

```bash
docker run --rm \
  -e GOPROXY=https://goproxy.cn,direct \
  -v "$PWD:/src" \
  -v lanwatch-go-mod:/go/pkg/mod \
  -v lanwatch-go-build:/root/.cache/go-build \
  -w /src golang:1.26.2-alpine go test ./...
```

| 路径 | 内容 |
| --- | --- |
| `cmd/lanwatch/` | 程序入口及管理员初始化 |
| `internal/source/` | 路由器适配器与采集调度 |
| `internal/presence/` | 在线状态机 |
| `internal/notify/` | 通知渠道、模板及发送逻辑 |
| `internal/api/`、`internal/store/` | HTTP API、SQLite 存储 |
| `web/` | React 前端与嵌入资源 |
| `deploy/` | 运行配置及模板 |
| `scripts/tplink_probe.py` | TP-Link 手工诊断工具 |
| `docs/DESIGN.md` | 设计与协议参考，包含尚未实现的规划 |

## 常见问题与当前限制

- **TP-Link 登录失败**：先检查明文/已编码模式是否匹配。不要反复尝试错误密码；适配器有退避与锁定保护。
- **前端更新未生效**：确认重新构建 Go 二进制和镜像，再强制刷新页面。
- **修改管理员环境变量无效**：该变量仅用于首次初始化，不是密码重置接口；不要通过删除数据库重置密码，否则会丢失业务数据。
- **健康接口正常但没有设备数据**：同时检查页面中的数据源状态与容器日志；当前健康接口不足以单独确认每个采集源正常工作。
- **部分设置尚未接入执行逻辑**：配置中保留了数据保留周期、最小离线通知时长，策略中保留了静默时段及聚合秒数等字段，但当前实现未完整执行这些设置，不应依赖它们实现延迟告警或自动清理。
- **通知链接**：部分手工/API 路径使用 `public_url`，自动事件通知引擎仍使用监听地址构造链接；经反向代理访问时可能出现不可达链接。

`.env`、数据库、构建产物均已列入忽略规则；部署时仍需自行替换模板值并妥善保管备份。
