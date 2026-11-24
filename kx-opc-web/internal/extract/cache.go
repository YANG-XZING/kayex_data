package extract

import (
	"bufio"
	"crypto/rand"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// ErrExpired 表示结果已过期或不存在（原网站提示“数据已过期，请重新查询”）。
var ErrExpired = errors.New("数据已过期，请重新查询")

// Cache 保存查询结果供之后导出：数据写成临时文件，内存里只留分段等元数据，
// 不像原网站那样把最近 30 次查询的全部数据都放在内存里。
type Cache struct {
	dir string
	ttl time.Duration
	max int
	log *slog.Logger

	mu      sync.Mutex
	entries map[string]*cached
	order   []string // 按写入先后
}

type cached struct {
	meta    Result // Table 为空
	path    string
	created time.Time
}

// NewCache 创建缓存目录并清掉上次运行留下的文件。
func NewCache(dir string, ttl time.Duration, max int, log *slog.Logger) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("创建取数缓存目录: %w", err)
	}
	old, _ := filepath.Glob(filepath.Join(dir, "*.gob"))
	for _, f := range old {
		_ = os.Remove(f)
	}
	return &Cache{dir: dir, ttl: ttl, max: max, log: log.With("module", "extract"), entries: map[string]*cached{}}, nil
}

// Put 保存结果，返回 token。
func (c *Cache) Put(r *Result) (string, error) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	path := filepath.Join(c.dir, token+".gob")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	err = gob.NewEncoder(w).Encode(r.Table)
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("保存查询结果: %w", err)
	}
	meta := *r
	meta.Table = nil
	c.mu.Lock()
	c.entries[token] = &cached{meta: meta, path: path, created: time.Now()}
	c.order = append(c.order, token)
	var evict []string
	for len(c.order) > c.max {
		evict = append(evict, c.order[0])
		c.order = c.order[1:]
	}
	for _, t := range evict {
		_ = os.Remove(c.entries[t].path)
		delete(c.entries, t)
	}
	c.mu.Unlock()
	return token, nil
}

// Meta 返回结果的元数据（不读数据文件）。
func (c *Cache) Meta(token string) (*Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[token]
	if !ok || time.Since(e.created) > c.ttl {
		return nil, ErrExpired
	}
	m := e.meta
	return &m, nil
}

// Get 读回完整结果。
func (c *Cache) Get(token string) (*Result, error) {
	meta, err := c.Meta(token)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	path := c.entries[token].path
	c.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrExpired
	}
	defer f.Close()
	var t Table
	if err := gob.NewDecoder(bufio.NewReaderSize(f, 1<<20)).Decode(&t); err != nil {
		return nil, fmt.Errorf("读取查询结果: %w", err)
	}
	meta.Table = &t
	return meta, nil
}

// Sweep 删除过期的结果，定期调用。
func (c *Cache) Sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := c.order[:0]
	for _, t := range c.order {
		if e := c.entries[t]; time.Since(e.created) > c.ttl {
			_ = os.Remove(e.path)
			delete(c.entries, t)
		} else {
			keep = append(keep, t)
		}
	}
	c.order = slices.Clip(keep)
}
