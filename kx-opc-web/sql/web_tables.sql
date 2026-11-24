-- 网站自己的表，和采集数据（opc_data）放在同一个库 kx_opc 里集中管理。
-- 都是普通表，主键为 (ts, 第二列) 复合主键（TDengine 3.3 起支持）。
-- 程序启动时自动执行以下建表（已存在则跳过），本文件仅供 DBA 手工建表或核对。
-- 约定：每一列（含 ts 和主键列）都用 zstd 高压缩。

-- 点位信息：每个点位（point，opc_data 的列名，就是 OPC 的 tag）对应的中文名、DB 点位（PLC 地址，如 33.24-0），全站显示和导出表头都用它；
-- customer：客户可见（客户账户的“上位机数据”只看这些点位，在“配置信息 → 客户可见点位”里勾选）。
-- 每次修改追加一行，每个 point 的最新一行即当前值；可以直接 INSERT，也可以在网站“点位信息”页上传 CSV。
-- 注意：库的 KEEP 是 1095 天，超过的行会被删除；程序会定期把长期没改过的点位重写一行，避免过期。
CREATE TABLE IF NOT EXISTS `kx_opc`.`opc_point_info` (
  `ts`       TIMESTAMP   COMPRESS 'zstd' LEVEL 'high',
  `point`    VARCHAR(64) PRIMARY KEY COMPRESS 'zstd' LEVEL 'high',
  `name`     NCHAR(64)   COMPRESS 'zstd' LEVEL 'high',
  `db_point` VARCHAR(64) COMPRESS 'zstd' LEVEL 'high',
  `customer` BOOL        COMPRESS 'zstd' LEVEL 'high'
);
-- 早期建的表没有 customer 列，程序启动时会自动补上，等同于：
--   ALTER TABLE kx_opc.opc_point_info ADD COLUMN customer BOOL COMPRESS 'zstd' LEVEL 'high';

-- 告警事件：只在状态变化时写，触发一行 active = true，恢复一行 active = false。
-- alarm_type：heartbeat_stop 心跳停止 / time_skew 时间不一致；seconds：心跳停了多少秒 / 时间差多少秒（工控机快为正）。
-- 主键 (ts, device_ip)：同一台炉台同一时刻的多条事件，程序会错开 1 毫秒写入。
CREATE TABLE IF NOT EXISTS `kx_opc`.`web_alarm` (
  `ts`         TIMESTAMP   COMPRESS 'zstd' LEVEL 'high',
  `device_ip`  VARCHAR(64) PRIMARY KEY COMPRESS 'zstd' LEVEL 'high',
  `device_id`  VARCHAR(64) COMPRESS 'zstd' LEVEL 'high',
  `alarm_type` VARCHAR(16) COMPRESS 'zstd' LEVEL 'high',
  `active`     BOOL        COMPRESS 'zstd' LEVEL 'high',
  `seconds`    DOUBLE      COMPRESS 'zstd' LEVEL 'high'
);

-- 旧版本的显示配置表，已不再使用（里面没有数据），确认后可以删除：
--   DROP TABLE IF EXISTS kx_opc.web_config;

-- 示例
-- 写一个点位的信息（改的时候再插一行即可，以最新一行为准，网站一分钟内读到）：
--   INSERT INTO kx_opc.opc_point_info (ts, point, name, db_point) VALUES (NOW, 'MELT_TEMP_PV', '熔体温度', '33.124-0');
-- 当前的点位信息：
--   SELECT point, LAST_ROW(name), LAST_ROW(db_point) FROM kx_opc.opc_point_info PARTITION BY point;
-- 每台炉台每种告警的当前状态（active = true 的就是还没恢复的告警）：
--   SELECT device_ip, alarm_type, LAST_ROW(device_id), LAST_ROW(ts), LAST_ROW(active), LAST_ROW(seconds) FROM kx_opc.web_alarm PARTITION BY device_ip, alarm_type;
