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
	From    string    `json:"from"` // 电脑 / 手机
	At      time.Time `json:"at"`
}

func (s *Store) clipPath() string {
	return filepath.Join(filepath.Dir(s.filesDir), "clips.json")
}

func (s *Store) AddClip(content, from string) ClipItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := ClipItem{
		ID:      randomToken()[:10],
		Content: strings.TrimSpace(content),
		From:    from,
		At:      time.Now(),
	}
	s.clips = append([]ClipItem{item}, s.clips...)
	if len(s.clips) > maxClips {
		s.clips = s.clips[:maxClips]
	}
	_ = saveJSON(s.clipPath(), s.clips)
	return item
}

func (s *Store) Clips() []ClipItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ClipItem, len(s.clips))
	copy(out, s.clips)
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
