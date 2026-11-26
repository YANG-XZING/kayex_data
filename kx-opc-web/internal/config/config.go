// Package config 加载并校验 config.yaml。
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// Config 对应 config.yaml。
type Config struct {
	Listen string `yaml:"listen"`
	// Accounts 登录账户；为空则不需要登录，所有人都按内部账户（全部功能）
	Accounts []Account `yaml:"accounts"`
	// SessionTTL 登录多久后要重新登录
	SessionTTL time.Duration `yaml:"session_ttl"`
	TDengine   TDengine      `yaml:"tdengine"`
	Realtime   Realtime      `yaml:"realtime"`
	History    History       `yaml:"history"`
	Monitor    Monitor       `yaml:"monitor"`
	Alarm      Alarm         `yaml:"alarm"`
	Extract    Extract       `yaml:"extract"`
	Crystal    Crystal       `yaml:"crystal"`
	HMI        HMI           `yaml:"hmi"`
	Clock      Clock         `yaml:"clock"`
	Areas      []Area        `yaml:"areas"`
	Log        Log           `yaml:"log"`
}

// 账户角色：内部账户看全部功能；客户只看“上位机数据”（管理员勾选的点位）和“上位机记录”
const (
	RoleAdmin    = "admin"
	RoleCustomer = "customer"
)

// Account 是一个登录账户。
type Account struct {
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Role     string `yaml:"role"` // admin / customer
}

// HMI 是上位机数据 / 上位机记录的配置。上位机记录是上位机写入 kayex_data 的操作记录、报警记录（超级表，标签 device_id、device_ip），
// 按扩大后的 ts 范围查候选记录，页面和导出按上位机时间 capture_time 筛选和显示。
type HMI struct {
	Database     string `yaml:"database"`      // 上位机记录所在的库
	RecordStable string `yaml:"record_stable"` // 操作记录：capture_time, name, value, color
	AlarmStable  string `yaml:"alarm_stable"`  // 报警记录：capture_time, content
	// MaxRange 上位机记录一次最多查多长时间
	MaxRange time.Duration `yaml:"max_range"`
}

// Alarm 是告警记录配置。心跳停止由 monitor.heartbeat_timeout 判定，本身已有防抖。
type Alarm struct {
	// SkewConfirm 时间差连续多少轮检查超限才触发、连续多少轮正常才恢复（每轮 monitor.interval）
	SkewConfirm int `yaml:"skew_confirm"`
	// HistoryMaxRange 告警记录一次最多查多长时间
	HistoryMaxRange time.Duration `yaml:"history_max_range"`
	// Record 是否把告警写进 web_alarm。正式部署只能有一个实例写（两个实例都写会记两遍），
	// 本机调试连同一个库时设为 false：照常判定、页面照常显示，只是不写库
	Record bool `yaml:"record"`
}

// Extract 是 PLC / CCD 取数与硬件信息（原 TDengine 取数网站）的配置。表名都是“库.表”。
type Extract struct {
	Database      string         `yaml:"database"`        // PLC、CCD 数据所在的库
	PLCStable     string         `yaml:"plc_stable"`      // PLC 超级表
	CCDStable     string         `yaml:"ccd_stable"`      // CCD 超级表
	DeviceTag     string         `yaml:"device_tag"`      // 区分炉台的标签（IP）
	TsCol         string         `yaml:"ts_col"`          // 主键时间列
	TimeCol       string         `yaml:"time_col"`        // 采集时间列，PLC 与 CCD 按它关联
	HardwareTable string         `yaml:"hardware_table"`  // 硬件信息表
	HardwareIPCol string         `yaml:"hardware_ip_col"` // 硬件信息表的 IP 列
	CrystalLedger string         `yaml:"crystal_ledger"`  // kayex_data 里的晶棒台账（kx-inner-processor 维护），按晶棒导出用
	Timezone      string         `yaml:"timezone"`        // 页面输入的时间按这个时区解释
	Chunk         time.Duration  `yaml:"chunk"`           // 分段查询，每段多长
	MaxRows       int            `yaml:"max_rows"`        // 一次最多多少行
	MaxConcurrent int            `yaml:"max_concurrent"`  // 同时进行的查询数（限制内存峰值）
	CacheDir      string         `yaml:"cache_dir"`       // 查询结果临时文件目录，相对路径基于本文件所在目录
	CacheTTL      time.Duration  `yaml:"cache_ttl"`       // 查询结果保留多久（之后再导出要重新查询）
	CacheMax      int            `yaml:"cache_max"`       // 最多保留多少次查询结果
	PLCPoints     []ExtractPoint `yaml:"plc_points"`      // 页面默认的 PLC 点位
	CCDPoints     []ExtractPoint `yaml:"ccd_points"`      // 页面默认的 CCD 点位
	StepLabels    []StepLabel    `yaml:"step_labels"`     // 工步号与名称，页面上作为可选工步
}

// Crystal 是晶棒数据导出的配置：列名与 kx-inner-processor 写入的一致。
type Crystal struct {
	CrystalColumn string        `yaml:"crystal_column"` // 晶编号列
	FurnaceColumn string        `yaml:"furnace_column"` // 炉次号列
	FirstColumn   string        `yaml:"first_column"`   // 是否首棒列
	StepColumn    string        `yaml:"step_column"`    // 工步号列
	GapLimit      time.Duration `yaml:"gap_limit"`      // 工步时长按心跳（monitor.heartbeat_column）算，相邻两行相差超过这么久算断数，不计入时长
	LedgerTable   string        `yaml:"ledger_table"`   // 晶棒台账（与采集数据同库）
	FurnaceTable  string        `yaml:"furnace_table"`  // 炉次台账（与采集数据同库），读每一炉的熔料 / 预热时间点
	ListDays      []int         `yaml:"list_days"`      // 页面上可选的“最近多少天”
	MaxRange      time.Duration `yaml:"max_range"`      // 一次最多导出多长时间
	StepNames     []StepName    `yaml:"step_names"`     // 工步号的名称
}

// StepName 是一个工步号（一位小数，如 24.1）的名称，Sub 为子工步。
type StepName struct {
	Code string `yaml:"code" json:"code"`
	Name string `yaml:"name" json:"name"`
	Sub  bool   `yaml:"sub" json:"sub"`
}

// ExtractPoint 是一个默认点位。
type ExtractPoint struct {
	Point string `yaml:"point" json:"point"`
	Name  string `yaml:"name" json:"name"`
	On    bool   `yaml:"on" json:"on"`
}

// StepLabel 是工步号与名称。
type StepLabel struct {
	Step  string `yaml:"step" json:"step"`
	Label string `yaml:"label" json:"label"`
}

// History 是历史查询与导出的限制。
type History struct {
	MaxRange time.Duration `yaml:"max_range"` // 曲线一次最多查多长时间
	MaxTags  int           `yaml:"max_tags"`  // 一次最多查几个点位
	// TableMaxRange 查看数据（历史数据表格，分页读取）和导出 CSV 一次最多查多长时间
	TableMaxRange time.Duration `yaml:"table_max_range"`
	// HeartColumn OPC 数据导出 CSV 时紧跟“时间”列加的上位机心跳时间列（HEART_DATETIME 为上位机本地时间）；为空不加
	HeartColumn string `yaml:"heart_column"`
}

// TDengine 是采集数据所在的库（kx-opc-client 写入的超级表）。
type TDengine struct {
	// DSN 使用 driver-go 的 taosWS 格式，如 root:taosdata@ws(10.65.104.171:6041)/kx_opc
	DSN string `yaml:"dsn"`
	// SuperTable 采集数据的超级表，每台炉台一张子表，标签 device_id、device_ip
	SuperTable string `yaml:"super_table"`
	// MaxConns 最多同时打开的连接数
	MaxConns int `yaml:"max_conns"`
	// Compress、CompressLevel 网站自己建的表（opc_point_info、web_alarm）每一列的压缩算法和级别
	Compress      string `yaml:"compress"`
	CompressLevel string `yaml:"compress_level"`
}

// Realtime 是实时数据推送配置。
type Realtime struct {
	// PollInterval 有人在看的炉台多久查一次最新行；采集约每秒一行
	PollInterval time.Duration `yaml:"poll_interval"`
}

// Monitor 是炉台状态（心跳、时间差）检查配置。
type Monitor struct {
	Interval time.Duration `yaml:"interval"`
	// HeartbeatColumn 心跳列，值为 yyyy-MM-dd HH:mm:ss 格式的时间
	HeartbeatColumn string `yaml:"heartbeat_column"`
	// HeartbeatZone 心跳值的时区：HEART_UTC_TIME 为 UTC，HEART_DATETIME 为工控机本地时间（Asia/Shanghai）
	HeartbeatZone string `yaml:"heartbeat_zone"`
	// HeartbeatTimeout 心跳超过该时长没有变化 → 心跳停止
	HeartbeatTimeout time.Duration `yaml:"heartbeat_timeout"`
	// ClockSkewLimit 心跳时间与基准时间之差的绝对值超过该值 → 时间不一致
	ClockSkewLimit time.Duration `yaml:"clock_skew_limit"`
	// TagsRefresh 多久重新读取一次炉台清单（子表标签），新上线的炉台据此出现
	TagsRefresh time.Duration `yaml:"tags_refresh"`
}

// Clock 是基准时间配置：后端不信任本机时钟，定期向这些来源取时间，按先后顺序尝试。
type Clock struct {
	NTPServers   []string      `yaml:"ntp_servers"`
	HTTPURLs     []string      `yaml:"http_urls"` // NTP 都不通时，取这些网址响应头里的 Date（精确到秒）
	SyncInterval time.Duration `yaml:"sync_interval"`
}

// Area 按明确的 IPv4 起止范围分区，范围包含两个端点。
type Area struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
	Area string `yaml:"area" json:"area"`
}

func areaBounds(a Area) (netip.Addr, netip.Addr, bool) {
	from, errFrom := netip.ParseAddr(a.From)
	to, errTo := netip.ParseAddr(a.To)
	return from, to, errFrom == nil && errTo == nil && from.Is4() && to.Is4() && from.Compare(to) <= 0
}

// Log 是日志配置。
type Log struct {
	Level string `yaml:"level"` // debug / info / warn / error
	File  string `yaml:"file"`  // 为空则只输出到控制台（journald）
}

func defaults() Config {
	return Config{
		Listen:     ":8700",
		SessionTTL: 12 * time.Hour,
		HMI:        HMI{Database: "kayex_data", RecordStable: "hmilog_record", AlarmStable: "hmilog_alarm", MaxRange: 31 * 24 * time.Hour},
		TDengine:   TDengine{SuperTable: "opc_data", MaxConns: 8, Compress: "zstd", CompressLevel: "high"},
		Realtime:   Realtime{PollInterval: time.Second},
		History:    History{MaxRange: 2 * time.Hour, MaxTags: 10, TableMaxRange: 31 * 24 * time.Hour, HeartColumn: "HEART_DATETIME"},
		Alarm:      Alarm{SkewConfirm: 3, HistoryMaxRange: 31 * 24 * time.Hour, Record: true},
		Extract: Extract{
			Database: "kayex_data", PLCStable: "dbcenter_data", CCDStable: "hmi_ccddata", DeviceTag: "device_ip",
			TsCol: "ts", TimeCol: "capture_time", HardwareTable: "sop_hardware.station_info", HardwareIPCol: "ip", CrystalLedger: "inner_crystal_info",
			Timezone: "Asia/Shanghai", Chunk: 72 * time.Hour, MaxRows: 5_000_000, MaxConcurrent: 2,
			CacheDir: "cache", CacheTTL: time.Hour, CacheMax: 30,
		},
		Crystal: Crystal{
			CrystalColumn: "inner_crystal_no", FurnaceColumn: "inner_furnace_no", FirstColumn: "is_first",
			StepColumn: "STATE", GapLimit: time.Minute, LedgerTable: "opc_inner_crystal_info", FurnaceTable: "opc_inner_furnace_info", ListDays: []int{7, 30, 90}, MaxRange: 31 * 24 * time.Hour,
		},
		Monitor: Monitor{
			Interval:         5 * time.Second,
			HeartbeatColumn:  "HEART_UTC_TIME",
			HeartbeatZone:    "UTC",
			HeartbeatTimeout: 60 * time.Second,
			ClockSkewLimit:   30 * time.Minute,
			TagsRefresh:      time.Minute,
		},
		Clock: Clock{
			NTPServers:   []string{"ntp.aliyun.com", "ntp.tencent.com"},
			HTTPURLs:     []string{"https://www.aliyun.com", "https://www.baidu.com"},
			SyncInterval: 5 * time.Minute,
		},
		Log: Log{Level: "info", File: "logs/kx-opc-web.log"},
	}
}

// Load 读取 config.yaml。日志路径如为相对路径，转换为基于配置文件目录的绝对路径。
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件: %w", err)
	}
	cfg := defaults()
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s: %w", path, err)
	}
	if !filepath.IsAbs(cfg.Extract.CacheDir) {
		cfg.Extract.CacheDir = filepath.Join(filepath.Dir(path), cfg.Extract.CacheDir)
	}
	if cfg.Log.File != "" && !filepath.IsAbs(cfg.Log.File) {
		cfg.Log.File = filepath.Join(filepath.Dir(path), cfg.Log.File)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("配置文件 %s:\n%w", path, err)
	}
	return &cfg, nil
}

// Database 从 DSN 中解析库名。
func (t TDengine) Database() string {
	dsn, _, _ := strings.Cut(t.DSN, "?")
	if i := strings.LastIndexByte(dsn, '/'); i >= 0 {
		return dsn[i+1:]
	}
	return ""
}

// Address 从 DSN 中解析 TDengine 地址（host:port），用于日志，不含用户名密码。
func (t TDengine) Address() string {
	_, rest, ok := strings.Cut(t.DSN, "(")
	if !ok {
		return ""
	}
	address, _, _ := strings.Cut(rest, ")")
	return address
}

// AreaOf 按 IP 起止范围返回区号，没有匹配时为空。
func (c Config) AreaOf(ip string) string {
	address, err := netip.ParseAddr(ip)
	if err != nil || !address.Is4() {
		return ""
	}
	for _, a := range c.Areas {
		from, to, ok := areaBounds(a)
		if ok && address.Compare(from) >= 0 && address.Compare(to) <= 0 {
			return a.Area
		}
	}
	return ""
}

func (c Config) validate() error {
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}

	check(c.Listen != "", "listen 不能为空")
	t := c.TDengine
	check(t.DSN != "", "tdengine.dsn 不能为空")
	check(identifierPattern.MatchString(t.Database()), "tdengine.dsn 中必须包含合法的库名，如 .../kx_opc，当前: %q", t.Database())
	check(identifierPattern.MatchString(t.SuperTable), "tdengine.super_table 不合法: %q", t.SuperTable)
	check(t.MaxConns >= 2, "tdengine.max_conns 不能小于 2")
	check(slices.Contains([]string{"lz4", "zlib", "zstd", "xz", "disabled"}, t.Compress), "tdengine.compress 必须是 lz4 / zlib / zstd / xz / disabled，当前: %q", t.Compress)
	check(slices.Contains([]string{"low", "medium", "high"}, t.CompressLevel), "tdengine.compress_level 必须是 low / medium / high，当前: %q", t.CompressLevel)

	check(c.Realtime.PollInterval >= 200*time.Millisecond, "realtime.poll_interval 不能小于 200ms")
	check(c.History.MaxRange >= time.Minute && c.History.MaxRange <= 7*24*time.Hour, "history.max_range 必须在 1m~168h 之间")
	check(c.History.MaxTags >= 1 && c.History.MaxTags <= 50, "history.max_tags 必须在 1~50 之间")
	check(c.History.TableMaxRange >= time.Hour && c.History.TableMaxRange <= 366*24*time.Hour, "history.table_max_range 必须在 1h~8784h 之间")
	check(c.Alarm.SkewConfirm >= 1 && c.Alarm.SkewConfirm <= 100, "alarm.skew_confirm 必须在 1~100 之间")
	e := c.Extract
	for _, v := range []struct{ key, val string }{
		{"database", e.Database}, {"plc_stable", e.PLCStable}, {"ccd_stable", e.CCDStable},
		{"device_tag", e.DeviceTag}, {"ts_col", e.TsCol}, {"time_col", e.TimeCol}, {"hardware_ip_col", e.HardwareIPCol},
		{"crystal_ledger", e.CrystalLedger},
	} {
		check(identifierPattern.MatchString(v.val), "extract.%s 不合法: %q", v.key, v.val)
	}
	check(regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*$`).MatchString(e.HardwareTable), "extract.hardware_table 应为 库.表，当前: %q", e.HardwareTable)
	_, zerr := time.LoadLocation(e.Timezone)
	check(zerr == nil, "extract.timezone 不是合法时区: %q", e.Timezone)
	check(e.Chunk >= time.Hour, "extract.chunk 不能小于 1h")
	check(e.MaxRows >= 1000, "extract.max_rows 不能小于 1000")
	check(e.MaxConcurrent >= 1 && e.MaxConcurrent <= 16, "extract.max_concurrent 必须在 1~16 之间")
	check(e.CacheTTL >= time.Minute, "extract.cache_ttl 不能小于 1m")
	check(e.CacheMax >= 1, "extract.cache_max 不能小于 1")
	cr := c.Crystal
	for _, v := range []struct{ key, val string }{
		{"crystal_column", cr.CrystalColumn}, {"furnace_column", cr.FurnaceColumn}, {"first_column", cr.FirstColumn},
		{"step_column", cr.StepColumn}, {"ledger_table", cr.LedgerTable}, {"furnace_table", cr.FurnaceTable},
	} {
		check(identifierPattern.MatchString(v.val), "crystal.%s 不合法: %q", v.key, v.val)
	}
	check(len(cr.ListDays) > 0, "crystal.list_days 不能为空")
	check(cr.GapLimit >= 5*time.Second, "crystal.gap_limit 不能小于 5s")
	for _, s := range cr.StepNames {
		_, perr := strconv.ParseFloat(s.Code, 64)
		check(perr == nil && s.Name != "", "crystal.step_names 中 code 必须是数字（如 24.1）、name 不能为空，当前: %q %q", s.Code, s.Name)
	}
	check(cr.MaxRange >= time.Hour, "crystal.max_range 不能小于 1h")
	check(c.Alarm.HistoryMaxRange >= time.Hour && c.Alarm.HistoryMaxRange <= 366*24*time.Hour, "alarm.history_max_range 必须在 1h~8784h 之间")

	users := map[string]bool{}
	for _, a := range c.Accounts {
		check(a.User != "" && a.Password != "", "accounts 中的 user、password 都不能为空")
		check(a.Role == RoleAdmin || a.Role == RoleCustomer, "accounts 中 %q 的 role 必须是 admin 或 customer，当前: %q", a.User, a.Role)
		check(!users[a.User], "accounts 中的用户名 %q 重复", a.User)
		users[a.User] = true
	}
	check(c.SessionTTL >= time.Minute, "session_ttl 不能小于 1m")
	h := c.HMI
	for _, v := range []struct{ key, val string }{{"database", h.Database}, {"record_stable", h.RecordStable}, {"alarm_stable", h.AlarmStable}} {
		check(identifierPattern.MatchString(v.val), "hmi.%s 不合法: %q", v.key, v.val)
	}
	check(h.MaxRange >= time.Hour && h.MaxRange <= 366*24*time.Hour, "hmi.max_range 必须在 1h~8784h 之间")

	m := c.Monitor
	check(m.Interval >= time.Second, "monitor.interval 不能小于 1s")
	check(identifierPattern.MatchString(m.HeartbeatColumn), "monitor.heartbeat_column 不合法: %q", m.HeartbeatColumn)
	_, err := time.LoadLocation(m.HeartbeatZone)
	check(err == nil, "monitor.heartbeat_zone 不是合法时区: %q", m.HeartbeatZone)
	check(m.HeartbeatTimeout >= m.Interval, "monitor.heartbeat_timeout 不能小于 monitor.interval")
	check(m.ClockSkewLimit > 0, "monitor.clock_skew_limit 必须大于 0")
	check(m.TagsRefresh >= m.Interval, "monitor.tags_refresh 不能小于 monitor.interval")

	check(c.Clock.SyncInterval >= time.Minute, "clock.sync_interval 不能小于 1m")
	for i, a := range c.Areas {
		from, to, ok := areaBounds(a)
		check(ok && a.Area != "", "areas[%d] 必须填写合法的 IPv4 起止范围 from、to 和非空 area，且 from 不能大于 to", i)
		if !ok {
			continue
		}
		for j := 0; j < i; j++ {
			otherFrom, otherTo, valid := areaBounds(c.Areas[j])
			check(!valid || from.Compare(otherTo) > 0 || otherFrom.Compare(to) > 0, "areas[%d] 与 areas[%d] 的 IP 范围重叠", i, j)
		}
	}
	return errors.Join(errs...)
}
