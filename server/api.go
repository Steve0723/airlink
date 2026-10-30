package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const maxChunkBytes = 32 << 20

type Server struct {
	store *Store
	state *State
	port  int

	mu         sync.RWMutex // 保护 state.Token / PreferredIP
	cfg        string       // state.json 路径
	mobilePage []byte       // /j 页面，给手机浏览器用
	uiPage     []byte       // /ui 桌面端页面

	// onClip 由窗口注入：手机发来剪贴板时回调，用于自动复制到 Windows 剪贴板。
	onClip func(ClipItem)
}

func (s *Server) token() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Token
}

func (s *Server) deviceName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.DeviceName
}

func (s *Server) rotateToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Token = randomToken()
	_ = saveJSON(s.cfg, s.state)
	return s.state.Token
}

func (s *Server) relayURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.RelayURL
}

func (s *Server) relayKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.RelayKey
}

// setRelay 保存中转配置；传空表示关掉公网兜底。
func (s *Server) setRelay(url, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.RelayURL = strings.TrimRight(strings.TrimSpace(url), "/")
	s.state.RelayKey = strings.TrimSpace(key)
	_ = saveJSON(s.cfg, s.state)
}

func (s *Server) deviceID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.DeviceID
}

func (s *Server) preferredIP() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.PreferredIP
}

// setPreferredIP 记住用户手动选择的网卡地址；传空表示恢复自动。
func (s *Server) setPreferredIP(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.PreferredIP == ip {
		return
	}
	s.state.PreferredIP = ip
	_ = saveJSON(s.cfg, s.state)
}

// orderedEndpoints 把用户选中的地址排到最前，其余保留作兜底。
// 手机端按顺序逐个探测，第一个通的就用。
func (s *Server) orderedEndpoints() []Endpoint {
	eps := Candidates(s.port)
	pref := s.preferredIP()
	if pref == "" {
		return eps
	}
	first := make([]Endpoint, 0, 1)
	rest := make([]Endpoint, 0, len(eps))
	for _, e := range eps {
		if e.IP == pref {
			first = append(first, e)
		} else {
			rest = append(rest, e)
		}
	}
	return append(first, rest...)
}

// ---------- 中间件 ----------

func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-AirLink-Token")
		if tok == "" {
			tok = r.URL.Query().Get("t")
		}
		if !tokenEqual(tok, s.token()) {
			writeErr(w, http.StatusUnauthorized, "配对码无效")
			return
		}
		next(w, r)
	}
}

// loopbackOnly 保护控制台接口。这些接口会返回配对码，绝不能从局域网访问。
func (s *Server) loopbackOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			writeErr(w, http.StatusForbidden, "仅限本机访问")
			return
		}
		next(w, r)
	}
}

// ---------- 响应工具 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"code": strconv.Itoa(code), "message": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return dec.Decode(v)
}

// ---------- 配对 ----------

// handlePing 不需要配对码：手机拿到候选地址列表后，靠它判断哪个 IP 真的可达。
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"app":     "airlink",
		"version": appVersion,
		"device":  s.deviceName(),
	})
}

func (s *Server) handleHandshake(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Device string `json:"device"`
	}
	_ = readJSON(r, &body)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"device":   s.deviceName(),
		"version":  appVersion,
		"chunkMax": maxChunkBytes,
	})
}

// ---------- 手机登记 ----------

// handleRegisterPhone 手机配对成功后上报自己的 ID，电脑才知道该往哪投递。
func (s *Server) handleRegisterPhone(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.ID) == "" {
		writeErr(w, http.StatusBadRequest, "缺少手机 ID")
		return
	}
	if len(body.ID) > 64 {
		writeErr(w, http.StatusBadRequest, "手机 ID 过长")
		return
	}
	s.upsertPhone(body.ID, body.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) upsertPhone(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if name == "" {
		name = "手机"
	}
	for i := range s.state.Phones {
		if s.state.Phones[i].ID == id {
			s.state.Phones[i].Name = name
			s.state.Phones[i].LastSeen = time.Now()
			_ = saveJSON(s.cfg, s.state)
			return
		}
	}
	s.state.Phones = append(s.state.Phones, PhoneInfo{ID: id, Name: name, LastSeen: time.Now()})
	_ = saveJSON(s.cfg, s.state)
}

// removePhone 忘掉一台手机。已投递的中转条目会自然过期，不做特殊处理。
func (s *Server) removePhone(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]PhoneInfo, 0, len(s.state.Phones))
	for _, p := range s.state.Phones {
		if p.ID != id {
			out = append(out, p)
		}
	}
	s.state.Phones = out
	_ = saveJSON(s.cfg, s.state)
}

func (s *Server) phones() []PhoneInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PhoneInfo, len(s.state.Phones))
	copy(out, s.state.Phones)
	return out
}

// ---------- 文件 ----------

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"files": s.store.Files()})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	entry, path, err := s.store.Find(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法读取文件")
		return
	}
	defer f.Close()

	// ServeContent 自带 Range / If-Modified-Since，安卓端断点续传直接吃这个。
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(entry.Name))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, entry.Name, entry.ModTime, f)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- 上传 ----------

func (s *Server) handleUploadInit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		Size      int64  `json:"size"`
		ChunkSize int64  `json:"chunkSize"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	up, err := s.store.BeginUpload(body.Name, body.Size, body.ChunkSize)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          up.ID,
		"name":        up.Name,
		"chunkSize":   up.ChunkSize,
		"totalChunks": up.TotalChunks,
		"received":    up.Received,
	})
}

func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "分片序号无效")
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxChunkBytes)
	data, err := io.ReadAll(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "分片读取失败")
		return
	}
	if err := s.store.WriteChunk(id, idx, data); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "index": idx, "bytes": len(data)})
}

func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request) {
	up, err := s.store.UploadStatus(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "上传任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          up.ID,
		"name":        up.Name,
		"chunkSize":   up.ChunkSize,
		"totalChunks": up.TotalChunks,
		"received":    up.Received,
		"complete":    up.Complete(),
	})
}

func (s *Server) handleUploadFinish(w http.ResponseWriter, r *http.Request) {
	entry, err := s.store.FinishUpload(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "file": entry})
}

// ---------- 文本 ----------

func (s *Server) handleGetText(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"texts": s.store.Texts()})
}

func (s *Server) handlePostText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
		From    string `json:"from"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Content) == "" {
		writeErr(w, http.StatusBadRequest, "内容为空")
		return
	}
	if body.From == "" {
		body.From = "手机"
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "text": s.store.AddText(body.Content, body.From)})
}

// ---------- 剪贴板 ----------

func (s *Server) handleGetClips(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"clips": s.store.Clips()})
}

func (s *Server) handlePostClip(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
		From    string `json:"from"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Content) == "" {
		writeErr(w, http.StatusBadRequest, "内容为空")
		return
	}
	if len(body.Content) > 256<<10 {
		writeErr(w, http.StatusBadRequest, "文本超过 256 KB")
		return
	}
	if body.From == "" {
		body.From = "手机"
	}
	item := s.store.AddClip(body.Content, body.From)
	if body.From == "手机" && s.onClip != nil {
		go s.onClip(item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "clip": item})
}

// handleDeleteClip 删掉一条记录。电脑和手机都能调。
func (s *Server) handleDeleteClip(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if strings.TrimSpace(id) == "" {
		writeErr(w, http.StatusBadRequest, "缺少记录 ID")
		return
	}
	if !s.store.DeleteClip(id) {
		writeErr(w, http.StatusNotFound, "记录不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleClearClips 清空全部记录。
func (s *Server) handleClearClips(w http.ResponseWriter, r *http.Request) {
	s.store.ClearClips()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handlePushLocalClip 由电脑端窗口调用：读取 Windows 剪贴板后推送。
func (s *Server) handlePushLocalClip(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Content) == "" {
		writeErr(w, http.StatusBadRequest, "剪贴板里没有文本")
		return
	}
	item := s.store.AddClip(body.Content, "电脑")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "clip": item})
}

// ---------- 控制台：文件与设置 ----------

func (s *Server) handleConsoleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.uiPage)
}

func (s *Server) handleConsoleDeleteFile(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleConsoleOpenFile(w http.ResponseWriter, r *http.Request) {
	_, p, err := s.store.Find(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	if err := exec.Command("explorer", p).Start(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleConsoleRevealFile(w http.ResponseWriter, r *http.Request) {
	_, p, err := s.store.Find(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	if err := exec.Command("explorer", "/select,"+p).Start(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleConsoleClips(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"clips": s.store.Clips()})
}

func (s *Server) handleConsoleDeleteClip(w http.ResponseWriter, r *http.Request) {
	if !s.store.DeleteClip(r.PathValue("id")) {
		writeErr(w, http.StatusNotFound, "记录不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleConsoleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"relayURL": s.relayURL(),
		"relayKey": s.relayKey(),
	})
}

func (s *Server) handleConsoleSetSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RelayURL string `json:"relayURL"`
		RelayKey string `json:"relayKey"`
		IP       string `json:"ip"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "参数错误")
		return
	}
	url := strings.TrimSpace(body.RelayURL)
	key := strings.TrimSpace(body.RelayKey)
	if (url == "") != (key == "") {
		writeErr(w, http.StatusBadRequest, "地址和密钥要么都填，要么都留空")
		return
	}
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		writeErr(w, http.StatusBadRequest, "地址要以 http:// 或 https:// 开头")
		return
	}
	s.setRelay(url, key)
	if ip := strings.TrimSpace(body.IP); ip != "" {
		s.setPreferredIP(ip)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- 控制台 ----------

type consoleState struct {
	Device    string      `json:"device"`
	Port      int         `json:"port"`
	Token     string      `json:"token"`
	Version   string      `json:"version"`
	Endpoints []Endpoint  `json:"endpoints"`
	Files     []FileEntry `json:"files"`
	Texts     []TextEntry `json:"texts"`
	QR        string      `json:"qr"`
}

func (s *Server) handleConsoleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, consoleState{
		Device:    s.deviceName(),
		Port:      s.port,
		Token:     s.token(),
		Version:   appVersion,
		Endpoints: Candidates(s.port),
		Files:     s.store.Files(),
		Texts:     s.store.Texts(),
		QR:        s.qrContent(),
	})
}

// qrContent 是二维码内容。用普通 http 地址而不是自定义协议：
// 装了 App 就解析它直连；没装 App 扫码就是浏览器打开 /j，零安装也能传。
func (s *Server) qrContent() string {
	eps := s.orderedEndpoints()
	base := fmt.Sprintf("http://127.0.0.1:%d", s.port)
	if len(eps) > 0 {
		base = "http://" + eps[0].Addr()
	}
	q := url.Values{}
	q.Set("h", qrHostList(eps))
	q.Set("t", s.token())
	q.Set("n", s.deviceName())
	// 带上中转信息：手机离开局域网后靠它走公网兜底
	if s.relayURL() != "" {
		q.Set("r", s.relayURL())
		q.Set("d", s.deviceID())
		q.Set("k", s.relayKey())
	}
	return base + "/j?" + q.Encode()
}

func (s *Server) handleMobilePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(s.mobilePage)
}

func (s *Server) handleConsoleQR(w http.ResponseWriter, r *http.Request) {
	png, err := qrcode.Encode(s.qrContent(), qrcode.Medium, 560)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "二维码生成失败")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (s *Server) handleConsoleRotate(w http.ResponseWriter, r *http.Request) {
	tok := s.rotateToken()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": tok})
}

// handleConsoleUpload 供电脑端拖拽上传：本机回环，直接流式落盘，不分片。
func (s *Server) handleConsoleUpload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.Header.Get("X-File-Name")
	}
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = decoded
	}
	if name == "" {
		writeErr(w, http.StatusBadRequest, "缺少文件名")
		return
	}
	entry, err := s.store.SaveStream(name, r.Body, r.ContentLength)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "file": entry})
}

func (s *Server) handleConsoleText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Content) == "" {
		writeErr(w, http.StatusBadRequest, "内容为空")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "text": s.store.AddText(body.Content, "电脑")})
}

// ---------- 路由 ----------

func (s *Server) routes(static http.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /{$}", static)
	mux.Handle("GET /static/", http.StripPrefix("/static/", static))

	mux.HandleFunc("GET /j", s.handleMobilePage)

	mux.HandleFunc("GET /api/v1/ping", s.handlePing)
	mux.HandleFunc("POST /api/v1/handshake", s.requireToken(s.handleHandshake))

	mux.HandleFunc("POST /api/v1/phone/register", s.requireToken(s.handleRegisterPhone))

	mux.HandleFunc("GET /api/v1/files", s.requireToken(s.handleFiles))
	mux.HandleFunc("GET /api/v1/files/{id}", s.requireToken(s.handleDownload))
	mux.HandleFunc("DELETE /api/v1/files/{id}", s.requireToken(s.handleDelete))

	mux.HandleFunc("POST /api/v1/upload/init", s.requireToken(s.handleUploadInit))
	mux.HandleFunc("PUT /api/v1/upload/{id}/{idx}", s.requireToken(s.handleUploadChunk))
	mux.HandleFunc("GET /api/v1/upload/{id}/status", s.requireToken(s.handleUploadStatus))
	mux.HandleFunc("POST /api/v1/upload/{id}/finish", s.requireToken(s.handleUploadFinish))

	mux.HandleFunc("GET /api/v1/text", s.requireToken(s.handleGetText))
	mux.HandleFunc("POST /api/v1/text", s.requireToken(s.handlePostText))

	mux.HandleFunc("GET /api/v1/clipboard", s.requireToken(s.handleGetClips))
	mux.HandleFunc("POST /api/v1/clipboard", s.requireToken(s.handlePostClip))
	mux.HandleFunc("DELETE /api/v1/clipboard/{id}", s.requireToken(s.handleDeleteClip))
	mux.HandleFunc("DELETE /api/v1/clipboard", s.requireToken(s.handleClearClips))
	mux.HandleFunc("POST /api/console/clipboard", s.loopbackOnly(s.handlePushLocalClip))
	mux.HandleFunc("GET /ui", s.handleConsoleUI)
	mux.HandleFunc("GET /api/console/clips", s.loopbackOnly(s.handleConsoleClips))
	mux.HandleFunc("DELETE /api/console/clips/{id}", s.loopbackOnly(s.handleConsoleDeleteClip))
	mux.HandleFunc("DELETE /api/console/files/{id}", s.loopbackOnly(s.handleConsoleDeleteFile))
	mux.HandleFunc("POST /api/console/files/{id}/open", s.loopbackOnly(s.handleConsoleOpenFile))
	mux.HandleFunc("POST /api/console/files/{id}/reveal", s.loopbackOnly(s.handleConsoleRevealFile))
	mux.HandleFunc("GET /api/console/settings", s.loopbackOnly(s.handleConsoleGetSettings))
	mux.HandleFunc("POST /api/console/settings", s.loopbackOnly(s.handleConsoleSetSettings))

	mux.HandleFunc("GET /api/console/state", s.loopbackOnly(s.handleConsoleState))
	mux.HandleFunc("GET /api/console/qr.png", s.loopbackOnly(s.handleConsoleQR))
	mux.HandleFunc("POST /api/console/rotate", s.loopbackOnly(s.handleConsoleRotate))
	mux.HandleFunc("POST /api/console/upload", s.loopbackOnly(s.handleConsoleUpload))
	mux.HandleFunc("POST /api/console/text", s.loopbackOnly(s.handleConsoleText))

	return logRequests(mux)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if d := time.Since(start); d > 300*time.Millisecond {
			verbosef("%s %s 耗时 %s", r.Method, r.URL.Path, d.Round(time.Millisecond))
		}
	})
}
