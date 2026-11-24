package common

import (
	"database/sql"
	"fmt"
	"sync"

	"hmi1.1_consumer/config"
	"hmi1.1_consumer/logx"

	_ "github.com/taosdata/driver-go/v3/taosWS"
)

// 将所有全局变量集中定义，避免重复声明
var (
	DB                  *sql.DB
	TableExistenceCache sync.Map // 缓存表是否存在
	tableLocks          sync.Map // 建表锁，防止高并发时重复建表

	// TableTagCache 用于缓存子表当前的炉台号(Tag)，防止重复执行 ALTER TABLE
	TableTagCache sync.Map

	// GlobalLastTsMap 用于存储每个表最后一次写入的时间戳，保证 TS 平滑递增
	GlobalLastTsMap sync.Map
)

func InitDB() {
	var err error
	DB, err = sql.Open("taosWS", config.TaosDSN)
	if err != nil {
		logx.Fatalf("DB", "数据库连接失败: %v", err)
	}
	// 根据需要设置连接池
	DB.SetMaxOpenConns(100)
	DB.SetMaxIdleConns(50)
}

func EnsureTableExists(tableName, stableName, deviceID, ip string) {
	if _, ok := TableExistenceCache.Load(tableName); ok {
		return
	}

	// 使用锁确保同一个 tableName 只有一个协程在执行建表逻辑
	lockIntf, _ := tableLocks.LoadOrStore(tableName, &sync.Mutex{})
	lock := lockIntf.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	// 二次检查，防止在等待锁的过程中表已经被创建了
	if _, ok := TableExistenceCache.Load(tableName); ok {
		return
	}

	createSql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS kayex_data.%s USING kayex_data.%s TAGS ('%s', '%s')",
		tableName, stableName, deviceID, ip)

	if _, err := DB.Exec(createSql); err == nil {
		TableExistenceCache.Store(tableName, true)
		// 建表成功时，顺便初始化一下 Tag 缓存，避免后续立即执行 ALTER TABLE
		TableTagCache.Store(tableName, deviceID)
	} else {
		logx.Errorf("DB", "建表失败 %s: %v", tableName, err)
	}
}
