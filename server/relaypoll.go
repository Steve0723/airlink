package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 公网兜底：手机在外网时把文件投递到 relay 的"信箱"，
// 电脑这边轮询把东西取回来。两端配对关系不变，不产生任何公开链接。

type relayItem struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (s *Server) relayEndpoint(path string) string {
	return strings.TrimSuffix(s.relayURL(), "/") + path
}

func (s *Server) relayList() ([]relayItem, error) {
	u := s.relayEndpoint("/api/v1/inbox?device=" + s.deviceID())
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-AirLink-Key", s.relayKey())

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relay 返回 %d", resp.StatusCode)
	}

	var out struct {
		Items []relayItem `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// relayFetch 把一条中转文件落到 files/。
// 已经存在同名且内容一致的，直接丢弃——重复投递不该产生副本。
func (s *Server) relayFetch(it relayItem) error {
	req, err := http.NewRequest(http.MethodGet, s.relayEndpoint("/api/v1/inbox/"+it.Code), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-AirLink-Key", s.relayKey())

	resp, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("relay 返回 %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(s.store.incDir, "relay-*.part")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	n, err := io.Copy(tmp, resp.Body)
	cerr := tmp.Close()
	if err != nil || cerr != nil {
		os.Remove(tmpName)
		return fmt.Errorf("下载中断")
	}

	name := safeName(it.Name)
	dst := filepath.Join(s.store.filesDir, name)
	if info, statErr := os.Stat(dst); statErr == nil && info.Size() == n && sameContent(dst, tmpName) {
		os.Remove(tmpName)
		return nil
	}

	final := s.store.uniqueName(name)
	if err := os.Rename(tmpName, filepath.Join(s.store.filesDir, final)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func (s *Server) relayAck(code string) {
	req, err := http.NewRequest(http.MethodDelete, s.relayEndpoint("/api/v1/inbox/"+code), nil)
	if err != nil {
		return
	}
	req.Header.Set("X-AirLink-Key", s.relayKey())
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (s *Server) pullRelayOnce() {
	// 没配中转就什么都不做，设置里随时可以打开
	if s.relayURL() == "" {
		return
	}
	items, err := s.relayList()
	if err != nil {
		verbosef("轮询中转失败: %v", err)
		return
	}
	for _, it := range items {
		if err := s.relayFetch(it); err != nil {
			verbosef("取回 %s 失败: %v", it.Name, err)
			continue
		}
		s.relayAck(it.Code)
		verbosef("已从公网中转取回: %s (%d 字节)", it.Name, it.Size)
	}
}

// startRelayPoller 常驻运行；没配中转时每轮直接返回，配好后无需重启。
func (s *Server) startRelayPoller() {
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.pullRelayOnce()
		}
	}()
}
