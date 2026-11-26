-- Website tables share kayex_data; source 171 schema; all data columns zstd/high.
CREATE TABLE IF NOT EXISTS `kayex_data`.`opc_point_info` (
  `ts` TIMESTAMP COMPRESS 'zstd' LEVEL 'high',
  `point` VARCHAR(64) COMPOSITE KEY COMPRESS 'zstd' LEVEL 'high',
  `name` NCHAR(64) COMPRESS 'zstd' LEVEL 'high',
  `db_point` VARCHAR(64) COMPRESS 'zstd' LEVEL 'high',
  `customer` BOOL COMPRESS 'zstd' LEVEL 'high'
);

CREATE TABLE IF NOT EXISTS `kayex_data`.`web_alarm` (
  `ts` TIMESTAMP COMPRESS 'zstd' LEVEL 'high',
  `device_ip` VARCHAR(64) COMPOSITE KEY COMPRESS 'zstd' LEVEL 'high',
  `device_id` VARCHAR(64) COMPRESS 'zstd' LEVEL 'high',
  `alarm_type` VARCHAR(16) COMPRESS 'zstd' LEVEL 'high',
  `active` BOOL COMPRESS 'zstd' LEVEL 'high',
  `seconds` DOUBLE COMPRESS 'zstd' LEVEL 'high'
);
