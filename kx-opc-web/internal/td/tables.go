package td

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// 网站自己的表，和采集数据放在同一个库里集中管理。都是普通表，主键为 (ts, 第二列) 复合主键；启动时自动创建（已存在则跳过）。
const (
	// PointTable 点位信息：点位（point，opc_data 的列名，就是 OPC 的 tag）对应的中文名、DB 点位（PLC 地址）、是否客户可见。
	// 每次修改追加一行，point 的最新一行即当前值；可以直接 INSERT，也可以在网站上传 CSV
	PointTable = "opc_point_info"
	// AlarmTable 告警事件：触发（active = true）、恢复（active = false）各写一行。
	// 主键是 (ts, device_ip)，同一台炉台同一时刻的多条事件要错开 1 毫秒写入
	AlarmTable = "web_alarm"
)

const ensureRetry = time.Minute

func (d *DB) tableDDL() []string {
	c := fmt.Sprintf(" COMPRESS '%s' LEVEL '%s'", d.compress, d.compressLevel)
	return []string{
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (`ts` TIMESTAMP%s, `point` VARCHAR(64) PRIMARY KEY%s, `name` NCHAR(64)%s, `db_point` VARCHAR(64)%s, `customer` BOOL%s)",
			Quote(d.database), Quote(PointTable), c, c, c, c, c),
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (`ts` TIMESTAMP%s, `device_ip` VARCHAR(64) PRIMARY KEY%s, `device_id` VARCHAR(64)%s, "+
			"`alarm_type` VARCHAR(16)%s, `active` BOOL%s, `seconds` DOUBLE%s)",
			Quote(d.database), Quote(AlarmTable), c, c, c, c, c, c),
	}
}

// EnsureTables 创建网站自己的表；失败时每分钟重试，直到成功或 ctx 结束。
func (d *DB) EnsureTables(ctx context.Context, log *slog.Logger) {
	for {
		err := d.ensureTables(ctx)
		if err == nil {
			log.Debug("网站数据表已就绪", "tables", []string{PointTable, AlarmTable})
			return
		}
		log.Warn("创建网站数据表失败，稍后重试", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(ensureRetry):
		}
	}
}

func (d *DB) ensureTables(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, ddl := range d.tableDDL() {
		if _, err := d.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("%s: %w", ddl, err)
		}
	}
	return d.addColumns(ctx)
}

// laterColumns 是建表之后才加的列：已有的表启动时自动补上
var laterColumns = []struct{ table, column, typ string }{
	{PointTable, "customer", "BOOL"},
}

func (d *DB) addColumns(ctx context.Context) error {
	for _, lc := range laterColumns {
		res, err := d.Query(ctx, fmt.Sprintf("DESCRIBE %s.%s", Quote(d.database), Quote(lc.table)))
		if err != nil {
			return err
		}
		found := false
		for _, row := range res.Rows {
			found = found || AsString(row[0]) == lc.column
		}
		if found {
			continue
		}
		ddl := fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s %s COMPRESS '%s' LEVEL '%s'",
			Quote(d.database), Quote(lc.table), Quote(lc.column), lc.typ, d.compress, d.compressLevel)
		if _, err := d.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("%s: %w", ddl, err)
		}
	}
	return nil
}
