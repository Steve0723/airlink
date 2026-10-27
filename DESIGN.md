# 跨屏互传 · 技术方案（Windows + Android）

> 工作名 **AirLink**，可随时改。目标：Windows 电脑与安卓手机之间传文件，同一 WiFi 走局域网，不同网络走互联网，不需要注册账号。
>
> 适用范围：**仅 Windows 10/11 + Android 8.0+**。不涉及 iOS / macOS，这个约束砍掉了原方案里最麻烦的一整块。
>
> **实现状态（v0.1.0）**：已按"双端原生"落地——Windows 端是 Walk 写的原生 Win32 窗口，安卓端是 Kotlin 原生 App。
> 因此本文 §3.1 里"浏览器必须走 WebRTC"的论证不再适用于主路径（App 不受 mixed content 限制，直接 HTTP），
> 但那份论证保留下来，因为 `/j` 网页版兜底仍然受同样的约束。

---

## 0. 一句话方案

**电脑装一个常驻托盘程序，手机扫码即用。** 扫码后按「局域网直连 → 公网 P2P → TURN 中继 → 离线投递箱」四级自动降级，用户全程只看到一句"正在连接"。

手机端两条路径并行：浏览器（零安装，给别人传）和 APK（自己日常用，满速）。

---

## 1. 排除 iOS 之后，哪些结论变了

先说结论：**方案整体更简单了，但不是简单一点点，是少了一整个技术分支。**

### 1.1 最大的变化：大文件不再是问题

原方案里最致命的限制是 iOS Safari 不能用 Blob 之外的落盘方式，超过 2 GB 必崩。安卓上这个问题不存在：

> **下载方向直接给一个 HTTP 链接，让 Android 系统的 DownloadManager 接管。**

`<a href="https://.../file/xxx?t=token" download>` 或 `window.location` 触发系统下载，由 DownloadManager 流式写入磁盘——**零内存压力、自带断点续传、通知栏显示进度、锁屏也在下**。这是安卓相对 iOS 最大的优势，完全不需要 File System Access API 那套东西。

所以浏览器路径能传 10 GB 文件，这在 iPhone 上做不到。

### 1.2 分发成本归零

APK 可以直接下载安装，不用过应用商店审核，不用等审核周期，不用为了合规改功能。这直接改变了投入判断：

| | 含 iOS | 仅 Android |
|---|---|---|
| 二期出 App | 15 天（Flutter 跨端 + 双端适配） | **10 天（Kotlin 原生）** |
| 分发 | App Store 审核，每次更新重审 | 直接放个 APK 链接 |
| 上架成本 | 开发者账号 + 隐私清单 + 审核沟通 | 0 |

**砍掉 iOS 也就砍掉了用 Flutter 的唯一理由。** 单平台直接用 Kotlin 原生：前台服务、SAF、MediaStore、DownloadManager 集成都更顺，省掉一层插件桥接和一堆 `flutter_webrtc` 的后台传输坑。

### 1.3 局域网可以走 HTTP 满速

App 不受浏览器安全上下文约束，可以直接 `http://192.168.1.20:6027`，不需要 WebRTC，不需要证书。局域网内 HTTP/2 上传能跑到数百 Mbps，接近 WiFi 上限。浏览器路径仍是 WebRTC（原因见 §3.1）。

### 1.4 但仍然要正视的限制

| 限制 | 后果 | 解法 |
|---|---|---|
| 浏览器 WebRTC 约 50–100 Mbps | 传大文件慢 | 改用 APK，走局域网 HTTP |
| Chrome 切后台/锁屏会被冻结 | 浏览器路径不能后台传 | APK + 前台服务 |
| 国产 ROM 杀后台比 iOS 更野蛮 | 后台传输随时被杀 | 引导加白名单，见 §9 |
| Android 14+ 前台服务类型限制 | 长时传输可能被系统掐 | 声明 `dataSync` + 电池优化豁免 |

**关键结论：浏览器负责「临时、给别人传、零安装」，APK 负责「自己日常、大文件、后台」。两者不是替代关系。**

---

## 2. 总体架构

四个部件：

| 部件 | 职责 | 位置 |
|---|---|---|
| **Windows Agent** | 局域网文件服务、WebRTC 端点、托盘、二维码、文件落盘 | 用户电脑，单 .exe |
| **安卓浏览器** | 扫码、选文件、进度、触发系统下载 | 零安装 |
| **安卓 APK**（二期） | 后台传输、局域网满速、相册批量、剪贴板 | 直装 |
| **云端** | 信令、TURN、短链投递箱 | 一台 VPS + coturn |

信令服务只转发几百字节的 SDP，**不碰文件数据**。数据要么直连，要么经 TURN，要么走对象存储。三种情况成本模型完全不同，必须分开核算。

---

## 3. 传输通道

### 3.1 通道① 局域网直连

**浏览器路径必须用 WebRTC，不能用自签名 HTTPS。** 两个原因：

1. 手机上的页面从云端 HTTPS 加载，向 `http://192.168.x.x` 发请求会被 **mixed content** 拦截；
2. 用自签名证书的话，安卓 Chrome 每次都要手动信任，体验不可接受。

WebRTC 不受 mixed content 限制，同网段时 ICE 优先选 host candidate，数据包不经过路由器，延迟低于 5ms。

**APK 路径直接走 HTTP。** App 不受浏览器安全约束，`POST /lan/v1/upload/{id}` 带 `Content-Range` 分片，HTTP/2 多路复用，能跑满 WiFi。局域网内明文可接受（或叠一层应用层 AES-GCM，见 §4）。

两条路径打到同一个 Windows Agent，只是入口不同。

### 3.2 通道② 公网 P2P

标准 ICE：云端信令交换 SDP → STUN 拿公网映射 → 双向打洞。

家用宽带（锥形 NAT）成功率约 80%；企业网络和部分移动网络是对称 NAT，**占比超过 35%，直连成功率不足 50%**。所以 TURN 是必需品，不是可选项。

> **省事选项**：如果你能接受两端都装 Tailscale / ZeroTier，通道②③ 可以整体跳过——组网后对方的 `100.x.x.x` 地址直接走通道①的局域网 HTTP，零额外开发。代价是手机端要常驻一个 VPN 应用，耗电且和某些应用冲突。建议作为「高级用户模式」而不是主路径。

### 3.3 通道③ TURN 中继

- **必须开 TURN over TLS 443**。国内部分运营商对 UDP 做 QoS 限速甚至封锁，TCP 443 最不容易被拦。
- 成本：中继双向计费，1 GB 文件实际消耗 2 GB 带宽。月传 500 GB 大约几十到一百多元。
- 按地域就近部署。

### 3.4 通道④ 离线投递箱

电脑关机或两端不同时在线时，降级为「上传后生成短链」：文件存对象存储，4 位短链 + 可选提取码，24 小时自动过期。

这条路径和你已有的 **quickdrop** 几乎完全重合——短链、提取码、过期清理、访问计数都是现成的。**直接复用，不要重写。**

---

## 4. 配对与安全

### 二维码内容

```
https://airlink.example.com/j/{roomId}#t={一次性token}&k={公钥指纹}&h={候选IP列表}
```

- `roomId`：6 位房间号，信令服务用它撮合两端
- `t`：32 字节随机 token，**5 分钟有效、用一次即失效**，防止二维码被拍照后他人蹭连
- `k`：Windows Agent 的 X25519 公钥指纹
- `h`：局域网候选地址列表（base64 编码），见 §10.1——**这是 Windows 上最容易出问题的地方**

**把公钥指纹放进二维码，扫码本身就完成了身份认证**，不需要让用户核对数字。二维码是带外的可信信道，中间人伪造不了，因为它拿不到私钥。

### 加密分层

| 层 | 手段 | 防什么 |
|---|---|---|
| 传输层 | DTLS（WebRTC 内置）/ TLS（HTTP 路径） | 链路窃听 |
| 应用层 | X25519 协商 → AES-256-GCM 分块加密 | **TURN 服务器和云端窥探** |
| 身份 | 二维码公钥指纹 | 中间人替换 |

应用层加密不能省。TURN 中继时数据经过你的服务器，"服务器只转发密文"是可以写进隐私说明的硬承诺，也是与商业网盘产品的差异点。

### 局域网暴露面

Agent 监听内网端口后，同 WiFi 下任何人都能扫到。必须：

- 默认**不响应**未携带有效 token 的请求，直接断开，不做信息回显
- 可选「仅允许已配对设备」，把首次配对成功的设备指纹写入白名单
- 单 IP 连续失败 10 次封禁 10 分钟（quickdrop 的 `security.go` 已有同类逻辑）

---

## 5. 大文件与断点续传

**分片**：4 MiB 一片，浏览器端 4 并发。别贪大——4 MiB 在弱网下重传代价小，也能塞进 DataChannel 缓冲窗口。

**进度记录**：Windows 端用 chunk bitmap 落 SQLite，`GET /upload/{id}/status` 返回已收位图。断线重连只补缺口。

**秒传**：先算 SHA-256（Web Worker 里算，别阻塞 UI），命中已存文件直接完成。

**流控（浏览器路径必做）**：DataChannel 有 `bufferedAmount` 上限，必须监听 `bufferedamountlow` 再喂下一片。**这是最常见的 OOM 原因**——直接 `for` 循环 `send()` 会把几 GB 文件传崩标签页。

**落盘**：
- 浏览器下载 → **DownloadManager 接管**，流式落盘，零内存压力（见 §1.1）
- 浏览器上传 → `slice()` 分片，内存峰值 = 分片大小 × 并发数 ≈ 16 MB，安全
- APK → SAF 或 MediaStore 直接写文件

**文件夹**：先传 manifest（JSON，含相对路径 + 大小 + hash），再并发传文件。浏览器用 `<input type="file" webkitdirectory>` 拿目录结构。

---

## 6. 技术栈

复用 quickdrop 的既有经验（Go 标准库 + `go:embed` 前端 + 单二进制），这套栈在本项目同样成立。

| 层 | 选型 | 说明 |
|---|---|---|
| Windows Agent | Go 1.22 + `getlantern/systray` | 单 .exe，无运行时依赖，约 8 MB |
| Windows 窗口 | `github.com/lxn/walk` | 纯 Go 的 Win32 原生控件，无 cgo、无 WebView2 依赖 |
| WebRTC（PC 侧） | `pion/webrtc` | 纯 Go，不用装 node |
| 信令服务 | Go + `nhooyr.io/websocket` | 无状态，可水平扩展 |
| 元数据 | SQLite | 单机够用，别上 Postgres |
| 对象存储 | 阿里云 OSS / MinIO | 投递箱用 |
| TURN | coturn | TLS 443，就近部署 |
| 反向代理 | Caddy | 自动 HTTPS，WebRTC 强制要求安全上下文 |
| 手机浏览器 | 原生 JS | 不引框架，100 KB 内，秒开 |
| 安卓 APK | Kotlin + OkHttp + 前台服务 | 单平台，不用 Flutter |

**为什么不用 Wails / Fyne**：Wails 要 Node 构建链和 WebView2 运行时，Fyne 要 cgo 和 C 编译器；Walk 是纯 Go 的 Win32 封装，`go build` 直接出单文件。代价是必须嵌入 `app.manifest` 声明 Common Controls v6，否则 `TTM_ADDTOOL` 会失败、窗口起不来——这个坑已经踩过并写进 README。

**为什么还留着 `/j` 网页版**：手机端 App 是主路径，但"临时给别人的手机传个文件"这个场景不该要求对方装 APK。同一套 HTTP 接口，网页版只是薄薄一层 UI。

**P0 不做 mDNS。** 二维码里已经带了 IP 候选列表，扫码 1 秒完成，mDNS 的价值（免扫码自动发现）在这个场景里收益很低，但实现成本不低（Windows 的 mDNS 支持一直有坑）。留到 P3。

前端不引 React/Vue 是有意的：页面只做四件事（扫码、选文件、看进度、下载），上框架纯属负担。quickdrop 的 `public/` 已经验证了这条路。

---

## 7. 接口草案

```
# ===== 云端 =====
POST /api/v1/room                 # Agent 调用，创建房间 → {roomId, token, qrUrl}
WS   /ws/signal?room={id}&t={tk}  # 双端接入，转发 SDP / ICE
GET  /j/{roomId}                  # 手机扫码落地页
GET  /s/{code}                    # 投递箱短链

# ===== Windows Agent 本地接口 =====
GET  /lan/v1/hello                # 握手，校验 token，返回设备信息
POST /lan/v1/manifest             # 上传文件夹清单
POST /lan/v1/upload/{fileId}      # 分片上传，带 Content-Range
GET  /lan/v1/upload/{fileId}/status   # 返回 chunk bitmap
GET  /lan/v1/file/{fileId}        # Range 下载，支持断点（DownloadManager 直接吃这个）
POST /lan/v1/text                 # 文本 / 剪贴板互传
```

统一 `{code, message}` 错误结构，沿用 quickdrop 的前端错误处理。

---

## 8. 里程碑

| 阶段 | 内容 | 工期 | 验收标准 |
|---|---|---|---|
| **P0** | Agent + 二维码 + 局域网 WebRTC 传单文件 | 4 天 | 手机扫码 → 传 2 GB 视频，DownloadManager 正常落盘 |
| **P1** | 断点续传 + 多文件 + 文件夹 + 文本互传 | 4 天 | 传到 60% 拔网线，重连后从 60% 继续 |
| **P2** | 公网模式：信令 + STUN + TURN | 7 天 | 手机切 4G 连上家里电脑，打洞失败自动降级 |
| **P3** | 投递箱（复用 quickdrop） | 2 天 | 电脑关机时手机也能发，对方开机收 |
| **P4** | 安卓 APK：后台传输、满速局域网、相册批量 | 10 天 | 锁屏状态下传完 10 GB |

合计约 27 天，比含 iOS 的版本少 8 天。

**P0 做完就能自己天天用了**，这时候再决定要不要继续投入。不要一开始按 P4 架构设计，会把 P0 拖成两个月。

---

## 9. 已知的坑

按踩到的概率排序，Windows 和安卓各占一半——**Windows 那几条比安卓更隐蔽，因为本地测试发现不了**。

### Windows 端

1. **多网卡导致二维码里的 IP 是错的**。装过 Hyper-V、WSL、VMware、Docker Desktop 或任何 VPN（含 Clash、Tailscale）的机器，`net.Interfaces()` 会返回一堆虚拟网卡。如果随手取第一个非回环地址，手机扫码必然连不上。**这是 Windows 上"扫码打不开"的第一大原因。** 解法见 §10.1。
2. **防火墙静默拦截**。首次监听端口会弹「Windows 安全警报」，用户点「取消」就全废，而且之后不再提示。更麻烦的是 WiFi 被标记为「公用网络」时，Windows 默认拒绝所有入站连接，**没有任何弹窗**。必须在 UI 里主动检测并提示。
3. **Clash / 代理软件 TUN 模式**。TUN 会劫持所有流量，包括局域网，导致直连失败。需要在 UI 里检测并提示「关闭 TUN 或把内网网段加入直连规则」。
4. **长路径**。Windows 默认 260 字符上限，接收深层嵌套的长文件名会失败。要么 `\\?\` 前缀，要么提示用户。
5. **单实例**。用户重复双击 .exe 会起两个进程抢同一个端口。用命名互斥量挡住。

### 安卓端

6. **国产 ROM 杀后台**。小米、华为、OPPO、vivo 的后台管理比 iOS 激进得多，前台服务也可能被清。必须引导用户做三件事：允许自启动、电池优化设为「无限制」、锁定最近任务列表。不做引导的话，后台传输成功率不到一半。
7. **Android 14+ 前台服务类型**。API 34 强制声明 `foregroundServiceType`，传输要用 `dataSync`；Android 15 又对 `dataSync` 加了每日时长上限。需要申请电池优化豁免，或改用 `connectedDevice` 类型。
8. **通知权限**。API 33+ 需要 `POST_NOTIFICATIONS` 运行时授权，否则前台服务通知不显示，用户以为没在传。
9. **Scoped Storage**。Android 10+ 不能随便写 `/sdcard`。存到「下载」目录用 MediaStore，存到任意目录用 SAF 让用户选。
10. **AP 隔离**。公共 WiFi / 酒店网络禁止终端互访，局域网通道直接失效——必须无感降级到公网通道，而不是报错。

### 通用

11. **UDP 被运营商限速**。TURN over TLS 443 兜底，客户端做「UDP 8 秒无响应就切 TCP」。
12. **域名备案 + HTTPS**。WebRTC 强制要求安全上下文，`http://` 页面拿不到 `RTCPeerConnection`。国内这一步会卡 1–2 周，提前办。

---

## 10. Windows 端工程细节

### 10.1 候选 IP 枚举（最容易翻车的地方）

不要"取第一个非回环地址"。正确做法：

```go
// 遍历所有网卡，过滤掉明显不是物理局域网的
skip := []string{"vEthernet", "VMware", "VirtualBox", "Hyper-V", "Loopback",
                 "TAP-", "tun", "utun", "ZeroTier", "Tailscale", "WSL"}

// 保留：私有网段 + 链路本地 + Tailscale 的 100.64/10
keep := func(ip net.IP) bool {
    return ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
           ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1] >= 64 && ip.To4()[1] <= 127
}
```

**过滤掉的不代表丢弃，而是分成两组**：物理网卡地址排前面（高优先级），虚拟网卡地址排后面（低优先级兜底）。

然后：

1. 所有候选地址按优先级排序，全部塞进二维码的 `h` 参数
2. 手机端拿到列表后**并发探测**，每个地址 200ms 超时，第一个返回 `/lan/v1/hello` 200 的就是可用的
3. 全部失败 → 无感降级到公网通道

并发探测这步不能省。靠猜"应该用哪个 IP"在装了代理软件的机器上几乎必然出错，实测探测一遍总共只要 200–400ms，用户完全无感。

### 10.2 防火墙

首次启动时自检，检测顺序：

1. 尝试 `net.Listen` 绑定 `0.0.0.0:6027`，失败说明端口被占
2. 通过 `netsh advfirewall firewall show rule` 查是否已有放行规则
3. 没有则在 UI 上给出**一键提权添加规则**的按钮（UAC 弹窗，管理员权限执行 `netsh advfirewall firewall add rule name="AirLink" dir=in action=allow protocol=TCP localport=6027 profile=private`）
4. 同时检测当前 WiFi 的网络类别，如果是「公用」，直接提示「请把当前 WiFi 改为专用网络」并给出跳转按钮

**不要静默要求管理员权限启动整个程序**，那会让用户觉得可疑。只在用户点按钮时提权。

### 10.3 托盘与自启

- 托盘图标用 `systray`，右键菜单：显示二维码 / 打开控制台 / 已配对设备 / 开机自启 / 退出
- 自启写 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，**不需要管理员权限**，比任务计划更省事
- 关闭窗口 = 最小化到托盘，不是退出。退出要二次确认（用户容易误以为关了其实还在跑）

---

## 11. 与 quickdrop 的关系

| | quickdrop | AirLink |
|---|---|---|
| 场景 | 异步：上传后生成链接，随时取 | 同步：两端同时在线，点对点 |
| 数据路径 | 必经服务器 | 优先直连 |
| 用户 | 需要账号 | 无账号，扫码即用 |

**可复用**：短链生成、提取码校验、过期清理定时器、限流与封禁、`go:embed` 前端构建方式、Docker 部署脚本。

**必须新写**：WebRTC 信令、ICE 处理、分片协议、断点续传、托盘 UI、候选 IP 枚举。

建议独立建仓，把 quickdrop 的 `security.go`、`store.go` 复制成内部小包（**不做 Go module 依赖**——两个项目演进节奏不同，耦合会互相拖累）。

---

## 附：如果只做一件事

只做 **P0 + P1（局域网 + 断点续传，共 8 天）**，它已经覆盖 80% 的真实使用场景——手机传照片到电脑、电脑传安装包到手机，绝大多数时候人和设备就在同一个 WiFi 下。

公网模式是"人在外面想取家里文件"才需要，频次低得多。真需要时，装个 Tailscale 比开发 P2 更快。
