package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type api struct {
	store *Store
	cfg   *config
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"code": strconv.Itoa(code), "message": msg})
}

// authorized 所有接口都要密钥。这是唯一的安全边界——拿到密钥就能给任意设备投递。
func (a *api) authorized(r *http.Request) bool {
	got := r.Header.Get("X-AirLink-Key")
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.cfg.key)) == 1
}

// handlePut 投递一个文件给指定设备。body 是原始字节，元信息走 query。
func (a *api) handlePut(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "密钥无效")
		return
	}

	to := strings.TrimSpace(r.URL.Query().Get("to"))
	if to == "" || len(to) > 64 {
		writeErr(w, http.StatusBadRequest, "缺少目标设备")
		return
	}
	name := safeName(r.URL.Query().Get("name"))

	ttl := a.cfg.defaultTTL
	if v := r.URL.Query().Get("ttl"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 && h <= 24*30 {
			ttl = time.Duration(h) * time.Hour
		}
	}

	code, path, err := a.store.Reserve(to, name, ttl)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法创建存储")
		return
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		a.store.Abort(code)
		writeErr(w, http.StatusInternalServerError, "无法写入文件")
		return
	}

	body := http.MaxBytesReader(w, r.Body, a.cfg.maxBytes)
	n, err := io.Copy(f, body)
	cerr := f.Close()
	if err != nil || cerr != nil {
		a.store.Abort(code)
		writeErr(w, http.StatusRequestEntityTooLarge, "文件过大或传输中断")
		return
	}
	if n == 0 {
		a.store.Abort(code)
		writeErr(w, http.StatusBadRequest, "空文件")
		return
	}
	if err := a.store.Commit(code, n); err != nil {
		a.store.Abort(code)
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"code": code, "size": n})
}

// handleList 返回某设备待收的条目，客户端轮询它。
func (a *api) handleList(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "密钥无效")
		return
	}
	device := strings.TrimSpace(r.URL.Query().Get("device"))
	if device == "" {
		writeErr(w, http.StatusBadRequest, "缺少设备 ID")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": a.store.List(device)})
}

func (a *api) handleDownload(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "密钥无效")
		return
	}
	code := r.PathValue("code")
	item, ok := a.store.Get(code)
	if !ok {
		writeErr(w, http.StatusNotFound, "条目不存在或已过期")
		return
	}
	f, err := os.Open(a.store.Path(code))
	if err != nil {
		writeErr(w, http.StatusNotFound, "文件已被清理")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(item.Name))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, item.Name, item.CreatedAt, f)
}

func (a *api) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "密钥无效")
		return
	}
	if !a.store.Delete(r.PathValue("code")) {
		writeErr(w, http.StatusNotFound, "条目不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *api) handleHealth(w http.ResponseWriter, r *http.Request) {
	count, total := a.store.Stats()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pending": count, "bytes": total})
}

// safeName 只保留文件名本身，挡掉路径穿越和非法字符。
func safeName(raw string) string {
	name := filepath.Base(strings.ReplaceAll(raw, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.Trim(name, "."))
	if name == "" {
		return "file"
	}
	return name
}
