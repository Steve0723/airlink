package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type relayResult struct {
	URL       string    `json:"url"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// uploadToRelay 把文件推到公网中转服务，拿回一个域名链接。
// 走的是流式 body，不会把整个文件读进内存。
func (s *Server) uploadToRelay(path, name string, ttl time.Duration) (relayResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return relayResult{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return relayResult{}, err
	}

	u := strings.TrimSuffix(s.relayURL, "/") + "/api/v1/put" +
		"?name=" + url.QueryEscape(name) +
		"&ttl=" + strconv.Itoa(int(ttl.Hours()))

	req, err := http.NewRequest(http.MethodPost, u, f)
	if err != nil {
		return relayResult{}, err
	}
	req.Header.Set("X-AirLink-Key", s.relayKey)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = info.Size()

	// 大文件可能传很久，不设整体超时；只限制建连和首包。
	client := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return relayResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return relayResult{}, fmt.Errorf("中转服务返回 %d，请检查地址和上传密钥", resp.StatusCode)
	}

	var out relayResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return relayResult{}, err
	}
	if out.URL == "" {
		return relayResult{}, fmt.Errorf("中转服务没有返回链接")
	}
	return out, nil
}
