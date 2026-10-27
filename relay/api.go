package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
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

func (a *api) authorized(r *http.Request) bool {
	got := r.Header.Get("X-AirLink-Key")
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.cfg.key)) == 1
}

func (a *api) baseURL(r *http.Request) string {
	if a.cfg.base != "" {
		return strings.TrimSuffix(a.cfg.base, "/")
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// handlePut 接收客户端上传：body 是原始文件字节，name/ttl 走 query。
func (a *api) handlePut(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "上传密钥无效")
		return
	}

	name := safeName(r.URL.Query().Get("name"))
	ttl := a.cfg.defaultTTL
	if v := r.URL.Query().Get("ttl"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 && h <= 24*30 {
			ttl = time.Duration(h) * time.Hour
		}
	}

	code, path, err := a.store.Reserve(name, ttl)
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

	item, _ := a.store.Get(code)
	writeJSON(w, http.StatusOK, map[string]any{
		"code":      code,
		"url":       a.baseURL(r) + "/s/" + code,
		"size":      n,
		"expiresAt": item.ExpiresAt,
	})
}

func (a *api) handleInfo(w http.ResponseWriter, r *http.Request) {
	item, ok := a.store.Get(r.PathValue("code"))
	if !ok {
		writeErr(w, http.StatusNotFound, "链接已失效")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":      item.Name,
		"size":      item.Size,
		"expiresAt": item.ExpiresAt,
	})
}

func (a *api) handleDownload(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	item, ok := a.store.Get(code)
	if !ok {
		writeErr(w, http.StatusNotFound, "链接已失效")
		return
	}
	f, err := os.Open(a.store.Path(code))
	if err != nil {
		writeErr(w, http.StatusNotFound, "文件已被清理")
		return
	}
	defer f.Close()

	a.store.Touch(code)
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(item.Name))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, item.Name, item.CreatedAt, f)
}

func (a *api) handleHealth(w http.ResponseWriter, r *http.Request) {
	count, total := a.store.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"files": count,
		"bytes": total,
	})
}

func (a *api) handlePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, sharePageHTML)
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

var _ = fmt.Sprintf

const sharePageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#1d9e75">
<title>文件分享 · AirLink</title>
<style>
  :root { --bg:#f4f6f5; --surface:#fff; --border:#e3e7e5; --text:#1c1f1e; --muted:#6b7370; --accent:#1d9e75; --accent-dark:#0f6e56; }
  * { box-sizing:border-box; -webkit-tap-highlight-color:transparent; }
  body { margin:0; min-height:100vh; background:var(--bg); color:var(--text);
    font-family:-apple-system,"PingFang SC","Noto Sans SC",system-ui,sans-serif;
    display:flex; align-items:center; justify-content:center; padding:24px; }
  .card { width:100%; max-width:380px; background:var(--surface); border:1px solid var(--border);
    border-radius:18px; padding:28px 24px; text-align:center; }
  .logo { width:46px; height:46px; border-radius:13px; background:var(--accent); margin:0 auto 16px; position:relative; }
  .logo::after { content:""; position:absolute; inset:13px 9px; border-top:2.5px solid #fff; border-bottom:2.5px solid #fff; }
  h1 { font-size:17px; margin:0 0 6px; font-weight:600; word-break:break-all; }
  .size { color:var(--muted); font-size:13px; margin:0; }
  .btn { display:block; width:100%; margin-top:18px; padding:14px; border:none; border-radius:12px;
    background:var(--accent); color:#fff; font-size:15px; font-family:inherit; font-weight:500;
    cursor:pointer; text-decoration:none; text-align:center; }
  .btn:active { background:var(--accent-dark); }
  .foot { color:var(--muted); font-size:11px; margin-top:22px; }
</style>
</head>
<body>
<div class="card">
  <div class="logo"></div>
  <h1 id="name">加载中…</h1>
  <p class="size" id="size"></p>
  <a class="btn" id="dl" href="#" style="display:none">下载</a>
  <div class="foot" id="foot"></div>
</div>
<script>
const code = location.pathname.split("/").filter(Boolean).pop();
const $ = (id) => document.getElementById(id);
const fmtSize = (n) => {
  if (n < 1024) return n + " B";
  const u = ["KB","MB","GB","TB"]; let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(n < 10 ? 1 : 0) + " " + u[i];
};
const fmtTime = (iso) => {
  const d = new Date(iso), p = (x) => String(x).padStart(2,"0");
  return d.getMonth() + 1 + "月" + d.getDate() + "日 " + p(d.getHours()) + ":" + p(d.getMinutes());
};
(async () => {
  try {
    const r = await fetch("/s/" + code + "/info", { cache: "no-store" });
    if (!r.ok) throw new Error();
    const info = await r.json();
    $("name").textContent = info.name;
    $("size").textContent = fmtSize(info.size);
    $("dl").href = "/s/" + code + "/f";
    $("dl").style.display = "block";
    $("foot").textContent = "有效期至 " + fmtTime(info.expiresAt);
  } catch {
    $("name").textContent = "链接已失效";
    $("foot").textContent = "分享已过期或文件已被清理";
  }
})();
</script>
</body>
</html>`

var _ = time.Now
