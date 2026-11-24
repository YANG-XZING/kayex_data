package extract

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"kx-opc-web/internal/td"
)

// Device 是 kayex_data 里有 PLC 数据的一台设备。
type Device struct {
	ID string `json:"id"` // device_id 标签，如 BB012
	IP string `json:"ip"`
}

const devicesTTL = 10 * time.Minute

// devicesCache 缓存设备清单：子表很少增减，10 分钟读一次。
type devicesCache struct {
	mu   sync.Mutex
	at   time.Time
	list []Device
}

// Devices 返回 PLC 超级表下的全部设备（按 device_id 排序）。
// 从 information_schema.ins_tags 读标签：只读元数据，几百毫秒；
// 直接 SELECT DISTINCT 超级表的标签在 kayex_data 上要扫全部 vgroup，实测超过 1 分钟。
func (s *Service) Devices(ctx context.Context) ([]Device, error) {
	c := &s.devices
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.list != nil && time.Since(c.at) < devicesTTL {
		return c.list, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	q := fmt.Sprintf("SELECT table_name, tag_name, tag_value FROM information_schema.ins_tags WHERE db_name = %s AND stable_name = %s AND tag_name IN ('device_id', %s)",
		td.String(s.cfg.Database), td.String(s.cfg.PLCStable), td.String(s.cfg.DeviceTag))
	res, err := s.db.Query(ctx, q)
	if err != nil {
		if c.list != nil {
			return c.list, nil // 读不到时先用旧的
		}
		return nil, err
	}
	byTable := map[string]*Device{}
	for _, row := range res.Rows {
		t := td.AsString(row[0])
		d := byTable[t]
		if d == nil {
			d = &Device{}
			byTable[t] = d
		}
		v := td.AsString(row[2])
		if td.AsString(row[1]) == "device_id" {
			d.ID = v
		} else {
			d.IP = v
		}
	}
	seen := map[string]bool{}
	list := make([]Device, 0, len(byTable))
	for _, d := range byTable {
		if d.IP != "" && !seen[d.IP] {
			seen[d.IP] = true
			list = append(list, *d)
		}
	}
	slices.SortFunc(list, func(a, b Device) int { return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(a.IP, b.IP)) })
	c.list, c.at = list, time.Now()
	return list, nil
}

// Crystal 是 kayex_data 晶棒台账里的一根晶（kx-inner-processor 维护，编号与 kx_opc 的不是同一套）。
type Crystal struct {
	CrystalNo    string  `json:"crystal_no"`
	FurnaceNo    string  `json:"furnace_no"`
	IsFirst      bool    `json:"is_first"`
	Start        int64   `json:"start"` // ts 毫秒，按晶棒取数用 [Start, End] 查
	End          *int64  `json:"end"`   // 进行中为 null
	StartCapture string  `json:"start_capture"`
	EndCapture   string  `json:"end_capture"`
	Seconds      float64 `json:"seconds"` // 按采集时间（PLC 时钟）算的时长；进行中为 0，页面按当前时间算
}

// Crystals 列出一台设备 since 以来开始的晶棒，按开始时间倒序。
func (s *Service) Crystals(ctx context.Context, device string, since time.Time) ([]Crystal, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	q := fmt.Sprintf("SELECT inner_crystal_no, inner_furnace_no, is_first, start_ts, end_ts, start_capture_time, end_capture_time "+
		"FROM %s.%s WHERE device_ip = %s AND start_ts >= %d ORDER BY start_ts DESC",
		td.Quote(s.cfg.Database), td.Quote(s.cfg.CrystalLedger), td.String(device), since.UnixMilli())
	res, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	list := make([]Crystal, 0, len(res.Rows))
	for _, row := range res.Rows {
		c := Crystal{CrystalNo: td.AsString(row[0]), FurnaceNo: td.AsString(row[1])}
		if f, ok := row[2].(int8); ok {
			c.IsFirst = f != 0
		}
		if t, ok := row[3].(time.Time); ok {
			c.Start = t.UnixMilli()
		}
		if t, ok := row[4].(time.Time); ok {
			e := t.UnixMilli()
			c.End = &e
		}
		if row[5] != nil {
			c.StartCapture = td.AsString(row[5])
		}
		if row[6] != nil {
			c.EndCapture = td.AsString(row[6])
		}
		t0, err0 := time.ParseInLocation(captureLayout, c.StartCapture, s.zone)
		t1, err1 := time.ParseInLocation(captureLayout, c.EndCapture, s.zone)
		if err0 == nil && err1 == nil {
			c.Seconds = t1.Sub(t0).Seconds()
		}
		list = append(list, c)
	}
	return list, nil
}

// captureLayout 是 capture_time 的格式（PLC 时钟，毫秒）
const captureLayout = "2006-01-02 15:04:05.000"
