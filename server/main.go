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
	publicURL := flag.String("public-url", "", "对外分享的域名，如 https://drop.example.com（配合 frp 或 Cloudflare Tunnel）")
	relayURL := flag.String("relay", "", "公网中转服务地址，如 https://drop.example.com（配置后分享可选上传到公网）")
	relayKey := flag.String("relay-key", "", "中转服务的上传密钥")
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
	sharePage, err := fs.ReadFile(sub, "share.html")
	if err != nil {
		fatal("分享页面缺失: %v", err)
	}

	srv := &Server{
		store:      store,
		state:      state,
		port:       *port,
		cfg:        statePath,
		mobilePage: mobilePage,
		sharePage:  sharePage,
		publicBase: *publicURL,
		relayURL:   *relayURL,
		relayKey:   *relayKey,
	}
	handler := srv.routes(http.FileServer(http.FS(sub)))

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

	// 每分钟清一次过期分享记录（只删记录，文件保留）。
	go func() {
		for range time.Tick(time.Minute) {
			if n := store.PurgeExpiredShares(); n > 0 {
				verbosef("清理了 %d 条过期分享", n)
			}
		}
	}()

	if *noGUI {
		printBanner(device, *port, srv.orderedEndpoints(), srv.qrContent())
		waitSignal()
		return
	}
	runGUI(srv)
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
