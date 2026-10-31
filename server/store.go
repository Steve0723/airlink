package main

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// 分片默认 4 MiB：弱网下重传代价小，也不至于让并发分片撑爆手机内存。
	defaultChunkSize = 4 << 20
	minChunkSize     = 256 << 10
	maxTexts         = 100
)

type FileEntry struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

type TextEntry struct {
	ID      string    `json:"id"`
	Content string    `json:"content"`
	From    string    `json:"from"`
	At      time.Time `json:"at"`
}

// Upload 是上传中文件的状态，落盘在 incoming/{id}.json，进程重启后仍可续传。
type Upload struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ChunkSize   int64     `json:"chunkSize"`
	TotalChunks int       `json:"totalChunks"`
	Received    []bool    `json:"received"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (u *Upload) ReceivedCount() int {
	n := 0
	for _, ok := range u.Received {
		if ok {
			n++
		}
	}
	return n
}

func (u *Upload) Complete() bool {
	return u.ReceivedCount() == u.TotalChunks
}

type Store struct {
	mu       sync.Mutex
	// defaultFilesDir 是数据目录下的 files/，filesDir 是当前生效的
	// （用户可以在设置里改到别处，改完旧文件仍然留在默认目录）
	defaultFilesDir string
	filesDir        string
	incDir          string
	texts    []TextEntry
	textPath string
	clips    []ClipItem
}

func NewStore(dataDir string) (*Store, error) {
	defaultFiles := filepath.Join(dataDir, "files")
	s := &Store{
		defaultFilesDir: defaultFiles,
		filesDir:        defaultFiles,
		incDir:          filepath.Join(dataDir, "incoming"),
		textPath:        filepath.Join(dataDir, "texts.json"),
	}
	for _, d := range []string{s.filesDir, s.incDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if raw, err := os.ReadFile(s.textPath); err == nil {
		_ = json.Unmarshal(raw, &s.texts)
	}
	if raw, err := os.ReadFile(s.clipPath()); err == nil {
		_ = json.Unmarshal(raw, &s.clips)
	}
	s.cleanOrphans()
	return s, nil
}

// cleanOrphans 清理 24 小时前没传完的残留分片。
func (s *Store) cleanOrphans() {
	entries, err := os.ReadDir(s.incDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(s.incDir, e.Name()))
	}
}

func fileID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))[:12]
}

// SetFilesDir 切换文件保存目录。传空串表示回到默认目录。
func (s *Store) SetFilesDir(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = s.defaultFilesDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s.mu.Lock()
	s.filesDir = dir
	s.mu.Unlock()
	return nil
}

// FilesDir 返回当前保存目录。
func (s *Store) FilesDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filesDir
}

// DefaultFilesDir 返回默认保存目录。
func (s *Store) DefaultFilesDir() string {
	return s.defaultFilesDir
}

func (s *Store) Files() []FileEntry {
	// 当前目录优先，再把默认目录里剩下的补进来，
	// 这样用户改过保存位置之后，之前收到的文件也还在列表里。
	dirs := []string{s.FilesDir()}
	if def := s.defaultFilesDir; def != dirs[0] {
		dirs = append(dirs, def)
	}

	seen := make(map[string]bool)
	var out []FileEntry

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if seen[name] {
				continue
			}
			seen[name] = true

			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, FileEntry{
				ID:      fileID(name),
				Name:    name,
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out
}

// Find 按 ID 找文件。ID 是文件名的哈希，所以不需要额外索引文件。
func (s *Store) Find(id string) (FileEntry, string, error) {
	for _, f := range s.Files() {
		if f.ID != id {
			continue
		}
		// 文件可能还在改位置之前的目录里，两个都找一遍
		for _, dir := range []string{s.FilesDir(), s.defaultFilesDir} {
			p := filepath.Join(dir, f.Name)
			if _, err := os.Stat(p); err == nil {
				return f, p, nil
			}
		}
		return FileEntry{}, "", os.ErrNotExist
	}
	return FileEntry{}, "", os.ErrNotExist
}

func (s *Store) Delete(id string) error {
	_, path, err := s.Find(id)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// SaveStream 供电脑端拖拽上传使用：直接落盘，不分片（本机回环，没有弱网问题）。
func (s *Store) SaveStream(name string, r io.Reader, size int64) (FileEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = s.uniqueName(safeName(name))
	dst := filepath.Join(s.filesDir, name)
	f, err := os.Create(dst)
	if err != nil {
		return FileEntry{}, err
	}
	n, err := io.Copy(f, r)
	cerr := f.Close()
	if err != nil {
		os.Remove(dst)
		return FileEntry{}, err
	}
	if cerr != nil {
		os.Remove(dst)
		return FileEntry{}, cerr
	}
	return FileEntry{ID: fileID(name), Name: name, Size: n, ModTime: time.Now()}, nil
}

func (s *Store) BeginUpload(rawName string, size, chunkSize int64) (*Upload, error) {
	if size <= 0 {
		return nil, errors.New("文件大小无效")
	}
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	if chunkSize < minChunkSize {
		chunkSize = minChunkSize
	}
	name := safeName(rawName)
	total := int((size + chunkSize - 1) / chunkSize)

	s.mu.Lock()
	defer s.mu.Unlock()

	// 同名同大小的未完成上传，直接复用进度，实现跨进程续传。
	for _, up := range s.listUploads() {
		if up.Name == name && up.Size == size && !up.Complete() {
			return up, nil
		}
	}

	up := &Upload{
		ID:          randomToken()[:16],
		Name:        name,
		Size:        size,
		ChunkSize:   chunkSize,
		TotalChunks: total,
		Received:    make([]bool, total),
		CreatedAt:   time.Now(),
	}
	f, err := os.Create(s.partPath(up.ID))
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	if err := s.saveUpload(up); err != nil {
		return nil, err
	}
	return up, nil
}

func (s *Store) WriteChunk(id string, idx int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	up, err := s.loadUpload(id)
	if err != nil {
		return err
	}
	if idx < 0 || idx >= up.TotalChunks {
		return fmt.Errorf("分片序号越界: %d", idx)
	}
	f, err := os.OpenFile(s.partPath(id), os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteAt(data, int64(idx)*up.ChunkSize)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	up.Received[idx] = true
	return s.saveUpload(up)
}

func (s *Store) UploadStatus(id string) (*Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUpload(id)
}

func (s *Store) FinishUpload(id string) (FileEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	up, err := s.loadUpload(id)
	if err != nil {
		return FileEntry{}, err
	}
	if !up.Complete() {
		return FileEntry{}, fmt.Errorf("还有 %d 个分片未到达", up.TotalChunks-up.ReceivedCount())
	}
	// 重复上传（比如反复备份同一批照片）直接复用已有文件，不再生成 "IMG_001 (2).jpg"。
	// 只比大小不够——大小相同但内容不同的两个文件会被误判成重复，那是数据丢失。
	// 所以再比一次 SHA-256，只在完全一致时才去重。
	existing := filepath.Join(s.filesDir, up.Name)
	if info, statErr := os.Stat(existing); statErr == nil && info.Size() == up.Size &&
		sameContent(existing, s.partPath(id)) {
		_ = os.Remove(s.partPath(id))
		_ = os.Remove(s.metaPath(id))
		return FileEntry{
			ID:      fileID(up.Name),
			Name:    up.Name,
			Size:    up.Size,
			ModTime: info.ModTime(),
		}, nil
	}

	name := s.uniqueName(up.Name)
	dst := filepath.Join(s.filesDir, name)
	if err := os.Rename(s.partPath(id), dst); err != nil {
		return FileEntry{}, err
	}
	_ = os.Remove(s.metaPath(id))
	return FileEntry{ID: fileID(name), Name: name, Size: up.Size, ModTime: time.Now()}, nil
}

// sameContent 比较两个文件的内容是否完全一致。
func sameContent(a, b string) bool {
	ha, err1 := hashFile(a)
	hb, err2 := hashFile(b)
	return err1 == nil && err2 == nil && string(ha) == string(hb)
}

func hashFile(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func (s *Store) AddText(content, from string) TextEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	t := TextEntry{
		ID:      randomToken()[:10],
		Content: strings.TrimSpace(content),
		From:    from,
		At:      time.Now(),
	}
	s.texts = append([]TextEntry{t}, s.texts...)
	if len(s.texts) > maxTexts {
		s.texts = s.texts[:maxTexts]
	}
	_ = saveJSON(s.textPath, s.texts)
	return t
}

func (s *Store) Texts() []TextEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TextEntry, len(s.texts))
	copy(out, s.texts)
	return out
}

func (s *Store) partPath(id string) string { return filepath.Join(s.incDir, id+".part") }
func (s *Store) metaPath(id string) string { return filepath.Join(s.incDir, id+".json") }

func (s *Store) saveUpload(up *Upload) error { return saveJSON(s.metaPath(up.ID), up) }

func (s *Store) loadUpload(id string) (*Upload, error) {
	if !isHexish(id) {
		return nil, os.ErrNotExist
	}
	raw, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return nil, err
	}
	up := &Upload{}
	if err := json.Unmarshal(raw, up); err != nil {
		return nil, err
	}
	return up, nil
}

func (s *Store) listUploads() []*Upload {
	entries, err := os.ReadDir(s.incDir)
	if err != nil {
		return nil
	}
	var out []*Upload
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.incDir, e.Name()))
		if err != nil {
			continue
		}
		up := &Upload{}
		if json.Unmarshal(raw, up) == nil {
			out = append(out, up)
		}
	}
	return out
}

func (s *Store) uniqueName(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	candidate := name
	for i := 2; i < 1000; i++ {
		if _, err := os.Stat(filepath.Join(s.filesDir, candidate)); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
		candidate = fmt.Sprintf("%s (%d)%s", base, i, ext)
	}
	return fmt.Sprintf("%s-%d%s", base, time.Now().Unix(), ext)
}

// isHexish 挡掉 ../ 之类的路径穿越尝试。
func isHexish(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// safeName 去掉路径分隔符和 Windows 非法字符，并规避保留设备名。
func safeName(raw string) string {
	name := filepath.Base(strings.ReplaceAll(raw, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	name = strings.Trim(name, ".")
	if name == "" {
		return "unnamed"
	}
	upper := strings.ToUpper(name)
	if i := strings.IndexByte(upper, '.'); i >= 0 {
		upper = upper[:i]
	}
	switch upper {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		name = "_" + name
	}
	return name
}
