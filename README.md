# AirLink · 手机电脑互传

Windows 原生桌面程序 + 安卓原生 App。同一个 WiFi 下扫码即传，文件不经过任何服务器。

## 它是什么

- **电脑端**：单个 `AirLink.exe`，无运行时依赖，打开就是上面这个窗口。显示二维码，手机扫一下就连上了。
- **手机端**：原生安卓 App，扫码配对、分片上传、前台服务保活、断点续传。
- **零安装兜底**：不装 App 也行。用手机相机扫同一个二维码，会打开一个网页版，功能一样（只是没有后台续传）。
- **剪贴板互传**：电脑点「发送剪贴板」，手机端点「复制」就粘上了；手机点「发送手机剪贴板」，电脑直接写入系统剪贴板，不用再点一次。
- **公网兜底**：手机离开 WiFi 时，文件自动改走中转服务器，电脑开机后自动收下。不用手动切换。
- **自动发现**：电脑通过 mDNS 广播自己，手机连不上原地址时会自动找回新 IP，不用重新扫码。

二维码里装的是候选 IP 列表，手机逐个探测，第一个通的就用——所以电脑上装了 VPN、Docker、WSL、虚拟机也不会连错。

## 目录结构

```
airlink/
├── relay/               公网兜底中转（Go，纯标准库，可选部署）
├── server/              Windows 端（Go + Walk 原生 Win32 控件）
│   ├── main.go          启动、命令行参数
│   ├── gui.go           窗口、二维码、IP 选择器、文件列表
│   ├── api.go           HTTP 接口
│   ├── store.go         文件存储、分片、断点续传
│   ├── netinfo.go       网卡枚举与虚拟网卡过滤
│   ├── config.go        配对码与配置持久化
│   ├── app.manifest     Common Controls v6（缺了 Walk 会崩）
│   ├── rsrc.syso        由 manifest + 图标生成的资源
│   └── web/             /j 手机网页版
├── android/             安卓端（Kotlin + Compose）
│   └── app/src/main/java/com/airlink/app/
│       ├── MainActivity.kt   界面、扫码、文件列表
│       ├── Api.kt            HTTP 客户端
│       ├── TransferService.kt 前台服务、分片上传
│       └── Data.kt           数据模型与配置
├── tools/               测试与构建脚本
├── .github/workflows/   两个平台一起出包
└── DESIGN.md            技术方案（含取舍论证）
```

## 跑起来

### 1. 电脑端

从 [Releases](../../releases) 下载 `AirLink-Windows.exe`，双击运行。

或者自己编译：

```bash
cd server
go build -trimpath -ldflags "-s -w -H=windowsgui" -o AirLink.exe .
```

首次运行 Windows 会弹防火墙提示，**必须允许「专用网络」**。如果当前 WiFi 被系统识别为「公用网络」，Windows 会静默拦截所有入站连接，需要到「设置 → 网络 → 属性」里改成专用。

### 2. 手机端

装 `AirLink-Android.apk`，打开后点「扫码连接」，对准电脑窗口里的二维码。

没装 App 也可以：用手机相机扫同一个二维码，浏览器会打开网页版。

## 构建

### 本地编译 Windows

```bash
cd server
go build -trimpath -ldflags "-s -w -H=windowsgui" -o AirLink.exe .
```

`-H=windowsgui` 表示不带控制台窗口。调试时去掉它，或加 `-no-gui` 用命令行模式看日志。

**改了 `app.manifest` 或 `icon.ico` 之后必须重新生成 `rsrc.syso`**，否则界面起不来：

```bash
go run github.com/akavel/rsrc@latest -manifest app.manifest -ico icon.ico -o rsrc.syso
```

### 本地编译安卓

用 Android Studio 打开 `android/` 目录，或者：

```bash
cd android
gradle assembleDebug
# 产物：app/build/outputs/apk/debug/app-debug.apk
```

需要 JDK 17、Android SDK（compileSdk 34）、Gradle 8.7。

### 用 GitHub Actions 编译

推到 GitHub 就行，`.github/workflows/build.yml` 会同时产出两个平台的安装包：

- `windows-latest` 编出 `AirLink-Windows.exe`
- `ubuntu-latest` 编出 `AirLink-Android.apk`

在 Actions 页面的 Artifacts 里下载。打 `v*` 标签（比如 `v0.1.0`）会自动创建 Release 并附上这两个文件。

## 接口

所有接口都要求 `X-AirLink-Token` 头（或 `?t=` 参数）等于配对码。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/ping` | 健康检查，**不需要配对码**，手机靠它探测哪个 IP 可达 |
| POST | `/api/v1/handshake` | 校验配对码 |
| GET | `/api/v1/files` | 文件列表 |
| GET | `/api/v1/files/{id}` | 下载，支持 Range |
| DELETE | `/api/v1/files/{id}` | 删除 |
| POST | `/api/v1/upload/init` | 初始化上传，返回已收到的分片位图 |
| PUT | `/api/v1/upload/{id}/{index}` | 上传一个分片 |
| GET | `/api/v1/upload/{id}/status` | 查询进度 |
| POST | `/api/v1/upload/{id}/finish` | 合并 |
| GET/POST | `/api/v1/text` | 文本互传 |
| GET/POST | `/api/v1/clipboard` | 剪贴板记录（文本） |
| POST | `/api/console/clipboard` | 电脑端推送本机剪贴板 |

## 公网兜底

局域网直连是主路径，但它有个硬边界：**手机一离开 WiFi 就连不上电脑**。

AirLink 的处理方式是自动兜底，不是让你手动切换：

```
手机发文件
   ↓
先试局域网直连（快，跑满 WiFi）
   ↓ 连不上
投递到 relay 中转（慢一点，但跨网可用）
   ↓
电脑端每 3 秒轮询，取回来存进文件列表
```

用户视角只有一件事：文件出现在了电脑上。中转是自动的，没有开关，也没有"分享链接"这种额外概念——**它始终是"发给这台电脑"，不是"发布一个链接"**。

### 部署 relay

一台有公网 IP 的服务器，域名 A 记录指过来：

```bash
cd relay
cp .env.example .env      # 填域名，密钥用 openssl rand -hex 24 生成
docker compose up -d      # Caddy 自动申请 HTTPS
curl https://你的域名/healthz
```

不用 Docker 也行，`relay/` 是纯标准库的 Go 程序：

```bash
go build -o relay . && ./relay -addr :8080 -data ./data -key 你的密钥
```

### 打开客户端的兜底

```bash
AirLink.exe -relay https://drop.example.com -relay-key 你的密钥
```

配好后二维码里会多带三个参数（`r` 中转地址、`d` 设备 ID、`k` 密钥）。手机在局域网内扫码时把这些存下来，之后离开 WiFi 发送文件就会自动走中转。

### relay 里没有公开页面

这一点和 quickdrop 有本质区别：

| | quickdrop | AirLink relay |
|---|---|---|
| 产出 | 公开短链，任何人可访问 | 无链接，只有绑定设备能取 |
| 存储 | 文件长期存放 | 取走即删 |
| 身份 | 账号体系 | 上传密钥 + 设备 ID |
| 用途 | 发布文件 | 两端之间的通道 |

relay 只有 4 个接口，全部要密钥：投递、列出待收、下载、删除。没有网页，没有索引页，没有列表接口能跨设备查询。

### 自动发现

电脑启动时通过 mDNS 广播 `_airlink._tcp`，TXT 记录只有三项：

```
d=<设备ID>  n=<设备名>  v=1
```

**没有配对码**。mDNS 是局域网内公开广播，谁都能读到——带上配对码等于把文件读写权限广播出去。

所以自动发现的作用不是"免配对"，而是**免重新配对**：手机第一次仍需扫码，之后电脑 IP 变了（DHCP 重新分配、换网络），手机连不上时会自己用 mDNS 把新地址找回来。用系统自带的 `NsdManager`，没引第三方库。

### 已知的取舍

- **二维码里带了 relay 密钥**。这意味着二维码一旦泄露，别人可以往你 relay 上的**任意设备**投递（但读不到已有文件，因为设备 ID 不同）。要收紧的话，需要 relay 给每台设备签发独立 token，目前没做。
- **兜底是单向的**：手机 → 电脑。电脑 → 手机在异地时还做不到，因为手机不常驻轮询（后台轮询太耗电）。
- **中转是明文**。文件在 relay 上以原始字节存在，取走即删。你自己运维这台服务器，威胁模型是"服务器被攻破"。
- **mDNS 会广播设备名和设备 ID**。不含任何密钥，但同一网络下能被看到。介意的话可以在系统防火墙里屏蔽 5353/UDP。

## 已知限制

- **只支持 Windows + 安卓**，没有 iOS / macOS 计划。
- **局域网 HTTP 明文**。配对码 + 一次性 token 挡的是同网段的误连，不是主动攻击。在意的话可以套一层 TLS，代价是要处理证书信任。
- **公网传输还没做**。目前只能在同一个局域网内用。跨网需要 P2P 打洞 + TURN，见 `DESIGN.md` 的 P2 阶段。
- 安卓国产 ROM（小米、华为、OPPO、vivo）会杀后台，需要手动允许自启动、关闭电池优化，否则锁屏后传输可能中断。
- **剪贴板只传文本**，不传图片。截图走文件传输更合适，不必塞进剪贴板。
- **网页版读不到系统剪贴板**：`http://` 不是安全上下文，`navigator.clipboard` 不可用。网页版只能发文字、收文字。
- 安卓 10 起，App 只有在窗口有焦点时才能读剪贴板——所以是"点按钮发送"，不是后台自动同步。

## 排错

**手机扫完码连不上**

1. 看电脑窗口「网络地址」下拉框，确认选中的是 `WLAN` 或以太网对应的那个地址，不是 `vEthernet`、`VMware` 之类。
2. 确认手机和电脑在同一个 WiFi（手机连的是不是 5G 蜂窝网络？）。
3. 关掉电脑上的代理软件 TUN 模式（Clash 之类），它会劫持局域网流量。
4. 公共 WiFi 常开 AP 隔离，终端之间不能互访，这种情况换手机热点。

**窗口打不开 / 一闪而过**

多半是 `rsrc.syso` 和 `app.manifest` 不匹配，重新生成一次（见上文）。或者用 `-no-gui` 启动看报错。

**配对码对不上**

点「重置」，手机重新扫码。重置后旧配对码立即失效。
