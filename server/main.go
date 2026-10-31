package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

//go:embed web
var webFS embed.FS

//go:embed icon.ico
var iconBytes []byte

var verbose bool

func verbosef(format string, args ...any) {
	if verbose {
		log.Printf(format, args...)
	}
}

func main() {
	port := flag.Int("port", 6027, "监听端口")
	dataDir := flag.String("data", "", "数据目录（默认 %AppData%\\AirLink）")
	name := flag.String("name", "", "设备名（默认计算机名）")
	noGUI := flag.Bool("no-gui", false, "不开窗口，纯命令行运行（调试用）")
	relayURL := flag.String("relay", "", "公网中转服务地址（也可在界面「设置」里填）")
	relayKey := flag.String("relay-key", "", "中转服务的上传密钥")
	mdnsProbe := flag.Bool("mdns-probe", false, "只做 mDNS 自检：广播并浏览几秒后退出")

	// 开发用：把界面渲染成 PNG 后退出，不需要显示器
	snapPath  := flag.String("snapshot", "", "把界面渲染到指定 PNG 后退出")
	snapW     := flag.Int("snapshot-w", 1808, "快照宽度（物理像素）")
	snapH     := flag.Int("snapshot-h", 1240, "快照高度（物理像素）")
	snapScale := flag.Float64("snapshot-scale", 2, "快照 DPI 缩放")
	snapPanel := flag.String("snapshot-panel", "", "快照时打开的面板：text / settings")
	flag.BoolVar(&verbose, "v", false, "输出详细日志")
	flag.Parse()

	log.SetFlags(log.Ltime)

	dir := *dataDir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			base = "."
		}
		dir = filepath.Join(base, "AirLink")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal("无法创建数据目录: %v", err)
	}
	ensureIcon(dir)

	device := *name
	if device == "" {
		device, _ = os.Hostname()
	}
	if device == "" {
		device = "Windows PC"
	}

	statePath := filepath.Join(dir, "state.json")
	state, err := loadState(statePath, device)
	if err != nil {
		fatal("无法读取配置: %v", err)
	}
	// 命令行参数只作为首次运行的初始值，之后以界面设置为准
	if *relayURL != "" && state.RelayURL == "" {
		state.RelayURL = *relayURL
		state.RelayKey = *relayKey
		_ = saveJSON(statePath, state)
	}
	store, err := NewStore(dir)
	if err != nil {
		fatal("无法初始化存储: %v", err)
	}

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		fatal("静态资源缺失: %v", err)
	}
	mobilePage, err := fs.ReadFile(sub, "m.html")
	if err != nil {
		fatal("手机页面缺失: %v", err)
	}

	srv := &Server{
		store:      store,
		state:      state,
		port:       *port,
		cfg:        statePath,
		mobilePage: mobilePage,
	}
	handler := srv.routes(http.FileServer(http.FS(sub)))

	// 快照只渲染界面，不监听端口，所以放在占用检测之前
	if *snapPath != "" {
		if err := snapshotUI(srv, *snapW, *snapH, float32(*snapScale), *snapPath, *snapPanel); err != nil {
			fatal("渲染快照失败: %v", err)
		}
		fmt.Println("已保存", *snapPath)
		return
	}

	// 端口已被占用说明已经有一个 AirLink 在跑，直接把窗口叫到前台就退出，
	// 免得用户双击两次开出一堆后台进程。
	if !*noGUI && portInUse(*port) {
		raiseExisting(*port)
		return
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		if isAddrInUse(err) {
			fatal("端口 %d 已被占用，可能 AirLink 已经在运行", *port)
		}
		fatal("端口 %d 无法监听: %v", *port, err)
	}

	httpSrv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("服务异常退出: %v", err)
		}
	}()

	// 手机发来的文字直接写进 Windows 剪贴板，用户不用再点一次复制。
	// 写失败不影响主流程——记录本身已经存下来了。
	srv.onClip = func(item ClipItem) {
		if err := setClipboard(item.Content); err != nil {
			verbosef("写入剪贴板失败: %v", err)
		}
	}

	srv.startRelayPoller()

	stopDiscovery := srv.startDiscovery()
	defer stopDiscovery()

	if *mdnsProbe {
		time.Sleep(time.Second)
		fmt.Println("--- 局域网内发现的 AirLink 服务 ---")
		lines := probeMDNS(4 * time.Second)
		if len(lines) == 0 {
			fmt.Println("(没发现任何服务)")
		}
		for _, l := range lines {
			fmt.Println(l)
		}
		return
	}

	if *noGUI {
		printBanner(device, *port, srv.orderedEndpoints(), srv.qrContent())
		waitSignal()
		return
	}
	// 托盘在后台跑自己的消息循环，主线程留给 Gio 的界面循环。
	if os.Getenv("AIRLINK_NO_TRAY") == "" {
		go runTray(srv)
	}
	runUI(srv)
}

// raiseExisting 让已经在跑的那个实例把窗口提到前台，然后本进程退出。
func raiseExisting(port int) {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/api/console/raise", port), nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// portInUse 判断本地端口是否已经被监听。
func portInUse(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// ensureIcon 把内嵌图标落到数据目录，供窗口和托盘加载。
func ensureIcon(dir string) {
	path := filepath.Join(dir, "icon.ico")
	if info, err := os.Stat(path); err == nil && info.Size() == int64(len(iconBytes)) {
		return
	}
	_ = os.WriteFile(path, iconBytes, 0o644)
}

func printBanner(device string, port int, eps []Endpoint, qrContent string) {
	fmt.Println()
	fmt.Println("  AirLink " + appVersion + "  ·  " + device)
	fmt.Println("  ─────────────────────────────────────────")
	if len(eps) == 0 {
		fmt.Println("  未找到可用的局域网地址，请检查网络连接")
	}
	for _, e := range eps {
		fmt.Printf("  %-8s http://%s  (%s)\n", e.Kind, e.Addr(), e.Iface)
	}
	fmt.Printf("  控制台   http://127.0.0.1:%d/\n", port)
	fmt.Println()
	if q, err := qrcode.New(qrContent, qrcode.Medium); err == nil {
		fmt.Println(q.ToSmallString(false))
	}
	fmt.Println("  Ctrl+C 退出")
	fmt.Println()
}

func waitSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
}

func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	return strings.Contains(err.Error(), "Only one usage of each socket address")
}

func fatal(format string, args ...any) {
	log.Printf(format, args...)
	os.Exit(1)
}
