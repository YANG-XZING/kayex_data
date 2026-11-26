# TDengine 新环境初始化

1. 先执行 `00_databases.sql`，创建 kayex_data 和 sop_hardware；固定保留 365 天。
2. 再执行 `01_tables.sql`，创建不会由当前程序自动创建的表。
3. 启动 consumer、OPC 采集与 Web。所有数据列使用 zstd/high，标签不设列压缩。

手工建表保留 12 张 PLC/HMI 超级表、PLC 两张原有查询台账以及硬件信息超级表。
Web 的 opc_point_info、web_alarm，以及 OPC 的 opc_data、opc_heartbeat_jump 由程序自动创建，不在这里重复维护。
OPC 晶棒/炉次台账不初始化；界面保留，相应数据暂不可用。PLC 台账保留结构，当前打包范围不包含其独立维护程序。

SQL 根据 10.65.104.171 的 3.4.0.9 结构导出，字段类型/长度/顺序/标签/复合主键与源库一致。
KEEP 改为 365 天，列压缩统一 zstd/high。
