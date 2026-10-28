package main

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// 公网兜底用的中转条目：绑定目标设备，不对外公开。
// 码长 8 位避免被枚举，但真正的边界是上传密钥。
const (
	codeLen      = 8
	codeAlphabet = "23456789abcdefghijkmnpqrstuvwxyz"
)

type Item struct {
	Code      string    `json:"code"`
	To        string    `json:"to"` // 目标设备 ID
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (i Item) Expired() bool { return time.Now().After(i.ExpiresAt) }

type Store struct {
	mu    sync.Mutex
	dir   string
	items map[string]*Item
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, items: map[string]*Item{}}
	if raw, err := os.ReadFile(s.indexPath()); err == nil {
		var list []*Item
		if json.Unmarshal(raw, &list) == nil {
			for _, it := range list {
				s.items[it.Code] = it
			}
		}
	}
	return s, nil
}

func (s *Store) indexPath() string        { return filepath.Join(s.dir, "index.json") }
func (s *Store) blobPath(c string) string { return filepath.Join(s.dir, c+".bin") }

func randomCode() string {
	b := make([]byte, codeLen)
	max := big.NewInt(int64(len(codeAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return ""
		}
		b[i] = codeAlphabet[n.Int64()]
	}
	return string(b)
}

// Reserve 先占码并建空文件，返回 code 和写入路径。
func (s *Store) Reserve(to, name string, ttl time.Duration) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	code := randomCode()
	for i := 0; i < 20; i++ {
		if code != "" {
			if _, exists := s.items[code]; !exists {
				break
			}
		}
		code = randomCode()
	}
	if code == "" {
		return "", "", os.ErrInvalid
	}

	f, err := os.Create(s.blobPath(code))
	if err != nil {
		return "", "", err
	}
	f.Close()

	now := time.Now()
	s.items[code] = &Item{
		Code:      code,
		To:        to,
		Name:      name,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	return code, s.blobPath(code), nil
}

func (s *Store) Commit(code string, size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, ok := s.items[code]; ok {
		it.Size = size
	}
	return s.saveLocked()
}

func (s *Store) Abort(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, code)
	_ = os.Remove(s.blobPath(code))
	_ = s.saveLocked()
}

func (s *Store) Get(code string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, ok := s.items[code]; ok && !it.Expired() {
		return *it, true
	}
	return Item{}, false
}

// List 返回某设备待收的条目，按投递时间正序（先投先收）。
// Size 为 0 表示还在上传中，不列出来。
func (s *Store) List(device string) []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Item, 0, 4)
	for _, it := range s.items {
		if it.To == device && !it.Expired() && it.Size > 0 {
			out = append(out, *it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) Path(code string) string { return s.blobPath(code) }

// Delete 收完立刻删，中转上不留东西。
func (s *Store) Delete(code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[code]; !ok {
		return false
	}
	_ = os.Remove(s.blobPath(code))
	delete(s.items, code)
	_ = s.saveLocked()
	return true
}

func (s *Store) saveLocked() error {
	list := make([]*Item, 0, len(s.items))
	for _, it := range s.items {
		list = append(list, it)
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.indexPath())
}

func (s *Store) Purge() (int, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed, freed := 0, int64(0)
	for code, it := range s.items {
		if !it.Expired() {
			continue
		}
		if info, err := os.Stat(s.blobPath(code)); err == nil {
			freed += info.Size()
		}
		_ = os.Remove(s.blobPath(code))
		delete(s.items, code)
		removed++
	}
	if removed > 0 {
		_ = s.saveLocked()
	}
	return removed, freed
}

func (s *Store) Stats() (int, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, it := range s.items {
		total += it.Size
	}
	return len(s.items), total
}
