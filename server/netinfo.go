package main

import (
	"net"
	"sort"
	"strconv"
	"strings"
)

// Endpoint 是一个可被手机访问的候选地址。
// Kind 取值：lan（物理网卡，优先级最高）/ virtual（虚拟机、VPN 等）/ mesh（组网工具）。
type Endpoint struct {
	IP    string `json:"ip"`
	Port  int    `json:"port"`
	Kind  string `json:"kind"`
	Iface string `json:"iface"`
}

func (e Endpoint) Addr() string {
	return net.JoinHostPort(e.IP, strconv.Itoa(e.Port))
}

// virtualHints 命中这些名字的网卡基本都是虚拟的。
// 装了 Hyper-V / WSL / Docker / 各类代理软件的 Windows 机器上，
// 随手取第一个非回环地址几乎必然是错的——这是"扫码连不上"的头号原因。
var virtualHints = []string{
	"vethernet", "vmware", "virtualbox", "hyper-v", "loopback",
	"tap-", "tun", "utun", "zerotier", "tailscale", "wsl",
	"bluetooth", "npcap", "vpn", "docker", "hamachi", "radmin", "clash", "wintun",
	"easytier", "netbird", "wireguard", "openvpn", "sangfor", "anyconnect",
}

func Candidates(port int) []Endpoint {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Endpoint
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			kind := classify(ip4, ifi.Name)
			if kind == "" {
				continue
			}
			out = append(out, Endpoint{IP: ip4.String(), Port: port, Kind: kind, Iface: ifi.Name})
		}
	}
	rank := map[string]int{"lan": 0, "virtual": 1, "mesh": 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Kind] != rank[out[j].Kind] {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		return out[i].IP < out[j].IP
	})
	return out
}

func classify(ip net.IP, iface string) string {
	if ip.IsLoopback() {
		return ""
	}
	// 100.64.0.0/10 是运营商级 NAT 段，Tailscale / ZeroTier 也用它。
	mesh := ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127
	if !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !mesh {
		return ""
	}
	name := strings.ToLower(iface)
	for _, h := range virtualHints {
		if strings.Contains(name, h) {
			if strings.Contains(name, "tailscale") || strings.Contains(name, "zerotier") || strings.Contains(name, "easytier") || strings.Contains(name, "netbird") {
				return "mesh"
			}
			return "virtual"
		}
	}
	if mesh {
		return "mesh"
	}
	if ip.IsLinkLocalUnicast() {
		return "virtual"
	}
	return "lan"
}

// qrHostList 把候选地址拼成二维码里用的紧凑形式：ip:port,ip:port
func qrHostList(eps []Endpoint) string {
	parts := make([]string, 0, len(eps))
	for _, e := range eps {
		parts = append(parts, e.Addr())
	}
	return strings.Join(parts, ",")
}
