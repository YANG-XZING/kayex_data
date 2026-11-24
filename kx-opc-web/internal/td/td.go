// Package td 封装对 TDengine 的只读查询：通用结果扫描、超级表列结构缓存、值的 JSON 规整。
package td

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	// 注册 taosWS 驱动（WebSocket，经 taosAdapter 6041 端口，无需安装 TDengine 原生库）
	_ "github.com/taosdata/driver-go/v3/taosWS"

	"kx-opc-web/internal/config"
)

const schemaTTL = time.Minute

var tablePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,192}$`)

// DB 是采集库的查询入口，并发安全。
type DB struct {
	db       *sql.DB
	database string
	stable   string
	// 网站自建表每一列的压缩算法和级别
	compress, compressLevel string

	mu        sync.Mutex
	columns   []Column
	columnsAt time.Time
}

// Column 是超级表的一个数据列（不含标签）。
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"` // TDengine 类型名，如 FLOAT、VARCHAR、BOOL
}

// Numeric 返回该列是否为数值列（可以画曲线）。
func (c Column) Numeric() bool {
	switch strings.Fields(c.Type)[0] {
	case "TINYINT", "SMALLINT", "INT", "BIGINT", "FLOAT", "DOUBLE":
		return true
	}
	return false
}

// Result 是一次查询的结果。
type Result struct {
	Columns []string
	Rows    [][]any
}

// Open 创建连接池，不会立即连接。
func Open(cfg config.TDengine) (*DB, error) {
	db, err := sql.Open("taosWS", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("打开 TDengine 连接: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxConns)
	db.SetMaxIdleConns(cfg.MaxConns)
	db.SetConnMaxIdleTime(10 * time.Minute)
	return &DB{db: db, database: cfg.Database(), stable: cfg.SuperTable, compress: cfg.Compress, compressLevel: cfg.CompressLevel}, nil
}

// Close 关闭连接池。
func (d *DB) Close() error { return d.db.Close() }

// Stable 返回超级表的全限定名，如 `kx_opc`.`opc_data`。
func (d *DB) Stable() string { return Quote(d.database) + "." + Quote(d.stable) }

// Table 返回子表的全限定名；子表名必须是字母数字下划线。
func (d *DB) Table(name string) (string, error) {
	if !tablePattern.MatchString(name) {
		return "", fmt.Errorf("子表名不合法: %q", name)
	}
	return Quote(d.database) + "." + Quote(name), nil
}

// Qualified 返回本库中某张表的全限定名，如 `kx_opc`.`web_alarm`。
func (d *DB) Qualified(table string) string { return Quote(d.database) + "." + Quote(table) }

// Query 执行查询并读出全部结果。
func (d *DB) Query(ctx context.Context, query string) (*Result, error) {
	res := &Result{}
	err := d.QueryEach(ctx, query, func(columns []string) { res.Columns = columns }, func(values []any) error {
		res.Rows = append(res.Rows, values)
		return nil
	})
	return res, err
}

// QueryEach 执行查询并逐行回调，不把结果整体读进内存（用于导出等大结果）。
// onColumns 在第一行之前调用一次；row 返回错误时停止读取。每次回调的 values 都是新切片。
func (d *DB) QueryEach(ctx context.Context, query string, onColumns func([]string), row func(values []any) error) error {
	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return err
	}
	if onColumns != nil {
		onColumns(names)
	}
	pointers := make([]any, len(names))
	for rows.Next() {
		values := make([]any, len(names))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		if err := row(values); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Exec 执行写入或建表语句。
func (d *DB) Exec(ctx context.Context, query string) error {
	_, err := d.db.ExecContext(ctx, query)
	return err
}

// String 把字符串转成 SQL 字符串字面量（单引号包裹，转义反斜杠和单引号）。
func String(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// Now 返回 TDengine 服务器的当前时间（数据的 ts 用的是采集端时钟，和它在同一台机器上）。
func (d *DB) Now(ctx context.Context) (time.Time, error) {
	var now time.Time
	err := d.db.QueryRowContext(ctx, "SELECT NOW()").Scan(&now)
	return now, err
}

// Columns 返回超级表的数据列（不含 ts 和标签），按 DESCRIBE 顺序，缓存一分钟。
func (d *DB) Columns(ctx context.Context) ([]Column, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.columns != nil && time.Since(d.columnsAt) < schemaTTL {
		return d.columns, nil
	}
	res, err := d.Query(ctx, "DESCRIBE "+d.Stable())
	if err != nil {
		if d.columns != nil {
			return d.columns, nil // 查不到时沿用旧结构
		}
		return nil, fmt.Errorf("DESCRIBE: %w", err)
	}
	columns := make([]Column, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 4 {
			continue
		}
		name, typ, note := asString(row[0]), asString(row[1]), asString(row[3])
		if name == "ts" || note == "TAG" {
			continue
		}
		columns = append(columns, Column{Name: name, Type: typ})
	}
	d.columns, d.columnsAt = columns, time.Now()
	return columns, nil
}

// Quote 用反引号包裹标识符（列名区分大小写）。
func Quote(name string) string { return "`" + strings.ReplaceAll(name, "`", "") + "`" }

// UnwrapFunc 去掉函数列名的外壳，如 last_row(`MELT_TEMP`) → MELT_TEMP。
func UnwrapFunc(name string) string {
	if open := strings.IndexByte(name, '('); open >= 0 && strings.HasSuffix(name, ")") {
		name = name[open+1 : len(name)-1]
	}
	return strings.Trim(name, "`")
}

// JSONValue 把查询出的值转换成可以直接 JSON 编码的值：NaN/Inf 变 null，时间格式化为本地时间字符串，[]byte 变字符串。
func JSONValue(v any) any {
	switch x := v.(type) {
	case float32:
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return nil
		}
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
	case time.Time:
		return x.Local().Format("2006-01-02 15:04:05.000")
	case []byte:
		return string(x)
	}
	return v
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// AsString 把查询出的值转换成字符串，NULL 为空串。
func AsString(v any) string { return asString(v) }
