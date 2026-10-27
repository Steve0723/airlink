package main

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// 短码字符集沿用 quickdrop：去掉 0/o/1/l/i 这类容易看错的字符。
const shareAlphabet = "23456789abcdefghijkmnpqrstuvwxyz"

const (
	shareCodeLen = 4
	maxShares    = 200
)

// Share 是一条分享记录。
//
// 和 quickdrop 的关键差异：过期只让链接失效，不删文件。
// quickdrop 的文件是"临时中转"，过期该清；AirLink 的文件是用户自己的库，
// 删掉就是数据丢失，所以这里只删记录。
type Share struct {
	Code      string    `json:"code"`
	FileID    string    `json:"fileId"`
	FileName  string    `json:"fileName"`
	Size      int64     `json:"size"`
	Password  string    `json:"password,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
	Downloads int       `json:"downloads"`
}

func (s Share) Expired() bool {
	return !s.ExpiresAt.IsZero() && time.Now().After(s.ExpiresAt)
}

func (s *Store) sharesPath() string {
	return strings.TrimSuffix(s.textPath, "texts.json") + "shares.json"
}

func randomShareCode() string {
	b := make([]byte, shareCodeLen)
	max := big.NewInt(int64(len(shareAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "aaaa"
		}
		b[i] = shareAlphabet[n.Int64()]
	}
	return string(b)
}

func (s *Store) AddShare(fileID, password string, ttl time.Duration) (Share, error) {
	entry, _, err := s.Find(fileID)
	if err != nil {
		return Share{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	code := randomShareCode()
	for i := 0; i < 50 && s.findShareLocked(code) != nil; i++ {
		code = randomShareCode()
	}

	share := Share{
		Code:      code,
		FileID:    entry.ID,
		FileName:  entry.Name,
		Size:      entry.Size,
		Password:  strings.TrimSpace(password),
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(ttl),
	}
	s.shares = append([]Share{share}, s.shares...)
	if len(s.shares) > maxShares {
		s.shares = s.shares[:maxShares]
	}
	_ = saveJSON(s.sharesPath(), s.shares)
	return share, nil
}

func (s *Store) Shares() []Share {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Share, 0, len(s.shares))
	for _, sh := range s.shares {
		if !sh.Expired() {
			out = append(out, sh)
		}
	}
	return out
}

func (s *Store) FindShare(code string) (Share, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sh := s.findShareLocked(code); sh != nil {
		return *sh, true
	}
	return Share{}, false
}

func (s *Store) findShareLocked(code string) *Share {
	for i := range s.shares {
		if s.shares[i].Code == code {
			return &s.shares[i]
		}
	}
	return nil
}

func (s *Store) DeleteShare(code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sh := range s.shares {
		if sh.Code == code {
			s.shares = append(s.shares[:i], s.shares[i+1:]...)
			_ = saveJSON(s.sharesPath(), s.shares)
			return true
		}
	}
	return false
}

func (s *Store) IncShareDownload(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sh := s.findShareLocked(code); sh != nil {
		sh.Downloads++
		_ = saveJSON(s.sharesPath(), s.shares)
	}
}

// PurgeExpiredShares 清掉过期记录。只动记录，不动文件。
func (s *Store) PurgeExpiredShares() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	kept := s.shares[:0]
	removed := 0
	for _, sh := range s.shares {
		if sh.Expired() {
			removed++
			continue
		}
		kept = append(kept, sh)
	}
	if removed > 0 {
		s.shares = kept
		_ = saveJSON(s.sharesPath(), s.shares)
	}
	return removed
}

func loadShares(path string) []Share {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []Share
	_ = json.Unmarshal(raw, &out)
	return out
}

// ---------- 接口 ----------

func (s *Server) shareURL(code string) string {
	// 配了公网域名就用域名，否则退回局域网地址。
	if s.publicBase != "" {
		return strings.TrimSuffix(s.publicBase, "/") + "/s/" + code
	}
	base := "http://" + strings.TrimPrefix(s.qrContent(), "http://")
	if i := strings.Index(base, "/j?"); i > 0 {
		base = base[:i]
	}
	return base + "/s/" + code
}

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FileID   string `json:"fileId"`
		Password string `json:"password"`
		TTLHours int    `json:"ttlHours"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if body.TTLHours <= 0 || body.TTLHours > 24*30 {
		body.TTLHours = 24
	}
	if body.Password != "" && (len(body.Password) != 4 || !isDigits(body.Password)) {
		writeErr(w, http.StatusBadRequest, "提取码必须是 4 位数字")
		return
	}

	share, err := s.store.AddShare(body.FileID, body.Password, time.Duration(body.TTLHours)*time.Hour)
	if err != nil {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code":      share.Code,
		"url":       s.shareURL(share.Code),
		"hasPassword": share.Password != "",
		"expiresAt": share.ExpiresAt,
	})
}

func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	shares := s.store.Shares()
	for i := range shares {
		shares[i].Password = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": shares})
}

func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request) {
	if !s.store.DeleteShare(r.PathValue("code")) {
		writeErr(w, http.StatusNotFound, "分享不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleShareInfo 给分享页拿文件信息，不需要配对码——链接本身就是凭证。
func (s *Server) handleShareInfo(w http.ResponseWriter, r *http.Request) {
	share, ok := s.store.FindShare(r.PathValue("code"))
	if !ok || share.Expired() {
		writeErr(w, http.StatusNotFound, "链接已失效")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":         share.FileName,
		"size":         share.Size,
		"hasPassword":  share.Password != "",
		"expiresAt":    share.ExpiresAt,
	})
}

func (s *Server) handleShareDownload(w http.ResponseWriter, r *http.Request) {
	share, ok := s.store.FindShare(r.PathValue("code"))
	if !ok || share.Expired() {
		writeErr(w, http.StatusNotFound, "链接已失效")
		return
	}
	if share.Password != "" && r.URL.Query().Get("p") != share.Password {
		writeErr(w, http.StatusForbidden, "提取码不正确")
		return
	}

	entry, path, err := s.store.Find(share.FileID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "文件已被删除")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法读取文件")
		return
	}
	defer f.Close()

	s.store.IncShareDownload(share.Code)
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(entry.Name))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, entry.Name, entry.ModTime, f)
}

func (s *Server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(s.sharePage)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
