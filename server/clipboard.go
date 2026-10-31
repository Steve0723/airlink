package main

import (
	"path/filepath"
	"strings"
	"time"
)

const maxClips = 20

// ClipItem 是一条剪贴板记录。
// 只存文本：截图之类的二进制走文件传输通道更合适，不必塞进剪贴板。
type ClipItem struct {
	ID      string    `json:"id"`
	Content string    `json:"content"`
	From    string    `json:"from"`           // 电脑 / 手机
	Kind    string    `json:"kind,omitempty"` // text=文字互传 / clip=剪贴板同步
	At      time.Time `json:"at"`
}

// 记录类型
const (
	KindText = "text"
	KindClip = "clip"
)

func (s *Store) clipPath() string {
	return filepath.Join(filepath.Dir(s.filesDir), "clips.json")
}

func (s *Store) AddClip(content, from, kind string) ClipItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	if kind == "" {
		kind = KindText
	}
	item := ClipItem{
		ID:      randomToken()[:10],
		Content: strings.TrimSpace(content),
		From:    from,
		Kind:    kind,
		At:      time.Now(),
	}
	s.clips = append([]ClipItem{item}, s.clips...)
	if len(s.clips) > maxClips {
		s.clips = s.clips[:maxClips]
	}
	_ = saveJSON(s.clipPath(), s.clips)
	return item
}

// Clips 返回记录。传 kind 时只返回该类型（text / clip），不传返回全部。
// 旧数据没有 kind 字段，一律当作 text。
func (s *Store) Clips(kinds ...string) []ClipItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	want := ""
	if len(kinds) > 0 {
		want = kinds[0]
	}

	out := make([]ClipItem, 0, len(s.clips))
	for _, c := range s.clips {
		k := c.Kind
		if k == "" {
			k = KindText
		}
		if want != "" && k != want {
			continue
		}
		out = append(out, c)
	}
	return out
}

// LatestFrom 返回指定来源最近的一条，没有则返回零值。
func (s *Store) LatestFrom(from string) (ClipItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clips {
		if c.From == from {
			return c, true
		}
	}
	return ClipItem{}, false
}

// DeleteClip 删掉一条记录，返回是否真的删到了。
func (s *Store) DeleteClip(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]ClipItem, 0, len(s.clips))
	found := false
	for _, c := range s.clips {
		if c.ID == id {
			found = true
			continue
		}
		out = append(out, c)
	}
	if !found {
		return false
	}
	s.clips = out
	_ = saveJSON(s.clipPath(), s.clips)
	return true
}

// ClearClips 清空全部记录。
func (s *Store) ClearClips() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clips = nil
	_ = saveJSON(s.clipPath(), s.clips)
}
