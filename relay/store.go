package main

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 公网短链用 8 位码：32^8 ≈ 1.1 万亿，枚举不现实。
// 局域网分享用 4 位没问题（同网段 + 时间窗），但公网上 4 位只有 100 万种，可以被扫。
const (
	codeLen      = 8
	codeAlphabet = "23456789abcdefghijkmnpqrstuvwxyz"
)

type Item struct {
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Downloads int       `json:"downloads"`
}

func (i Item) Expired() bool {
	return time.Now().After(i.ExpiresAt)
}

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

func (s *Store) indexPath() string { return filepath.Join(s.dir, "index.json") }
func (s *Store) blobPath(code string) string { return filepath.Join(s.dir, code+".bin") }

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

// Reserve 先占一个码并建好空文件，返回 code 和写入路径。
func (s *Store) Reserve(name string, ttl time.Duration) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	code := randomCode()
	for i := 0; i < 20; i++ {
		if _, exists := s.items[code]; !exists && code != "" {
			break
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

func (s *Store) Touch(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, ok := s.items[code]; ok {
		it.Downloads++
		_ = s.saveLocked()
	}
}

func (s *Store) Path(code string) string { return s.blobPath(code) }

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

// Purge 删掉过期条目和对应文件——中转服务的文件本来就是临时的。
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

// Stats 给 /healthz 用。
func (s *Store) Stats() (int, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, it := range s.items {
		total += it.Size
	}
	return len(s.items), total
}
