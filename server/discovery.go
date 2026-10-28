package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

// mDNS 广播让手机不用每次扫码就能找到这台电脑。
//
// 关键取舍：TXT 里**不放配对码**。mDNS 是局域网内公开广播，谁都能读到；
// 带上配对码等于把文件读写权限广播出去。这里只广播设备 ID 和设备名，
// 手机靠"以前扫过码、存了 token"来复用配对。
const mdnsService = "_airlink._tcp"

// startDiscovery 注册 mDNS 服务，返回停止函数。
// 失败不致命：没有 mDNS 就退回扫码，功能不受影响。
func (s *Server) startDiscovery() func() {
	instance := "AirLink-" + shortID(s.deviceID())
	txt := []string{
		"d=" + s.deviceID(),
		"n=" + s.deviceName(),
		"v=1",
	}

	srv, err := zeroconf.Register(instance, mdnsService, "local.", s.port, txt, nil)
	if err != nil {
		log.Printf("mDNS 广播失败（不影响扫码使用）: %v", err)
		return func() {}
	}
	verbosef("mDNS 已广播: %s.%s 端口 %d", instance, mdnsService, s.port)
	return srv.Shutdown
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// probeMDNS 是自检用：浏览本机广播出去的服务，确认 mDNS 真的生效。
func probeMDNS(timeout time.Duration) []string {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return []string{"无法创建解析器: " + err.Error()}
	}

	entries := make(chan *zeroconf.ServiceEntry, 16)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	go func() { _ = resolver.Browse(ctx, mdnsService, "local.", entries) }()

	var out []string
	deadline := time.After(timeout)
	for {
		select {
		case e := <-entries:
			if e == nil {
				continue
			}
			addrs := make([]string, 0, 2)
			for _, ip := range e.AddrIPv4 {
				addrs = append(addrs, ip.String())
			}
			out = append(out, fmt.Sprintf("%s  %s:%d  %v",
				e.ServiceInstanceName(), strings.Join(addrs, ","), e.Port, e.Text))
		case <-deadline:
			return out
		}
	}
}
