# 可视化打包与部署

双击根目录的 `启动打包工具.cmd`。开发机需要 Python 3.11+（含 tkinter）、PyYAML、Go 1.25.6+；勾选 Web 时还需要 Node.js/npm。
首次缺少 Python 依赖时运行 `python -m pip install -r tools/requirements.txt`。

1. 配置默认放在 `profiles/site`，也可以在窗口中新建、选择另一套现场配置目录。
2. “外置配置”页选择对应文件修改，切换文件、校验和打包时保存当前编辑内容。也可直接用文本编辑器修改文件。
3. 勾选需要的组件，先校验，再打包。每个组件生成独立 `.tar.gz` 和 `.sha256`，输出默认 `dist`。
4. 编译读取当前源码，包括本次用户修改的前端；Web 在临时副本构建，不覆盖源码里的前端产物。
5. 打包不会启动采集、连接 MQ 消费消息或执行远端 SQL。安装到 Linux 后按下文单独初始化、启动。

## 外置配置文件

| 文件 | 配置内容 |
|---|---|
| settings.json | 共用 TD 连接、MQ 连接、应用安装父目录、Linux 服务账号 |
| bigdata_consumer/environment.json | PLC 队列、表名前缀、点位、批量、并发、连接池和超时 |
| hmi_consumer/environment.json | 11 个 HMI 队列、SOP 目录与保留时间、日志、连接池、批量和重试 |
| kx-opc-client/config.yaml | TD 连接、压缩、自动建库参数、暂存、采集和日志 |
| kx-opc-client/furnaces.json | 炉台 IP 范围、排除清单、OPC 端口、区域 |
| kx-opc-web/config.yaml | 网站账号/角色、端口、TD、查询限制、告警、时钟、区域、导出 |
| tdengine/database.json | 保留时间、VGROUPS、BUFFER、数据块行数、WAL |
| tdengine/taos.cfg | TD 服务数据/日志目录、主机名等；安装后需要显式应用 |
| rabbitmq/install.json | MQ、Erlang、OpenSSL 路径和 MQ 系统账号、数据/日志目录 |
| rabbitmq/rabbitmq.conf / rabbitmq-env.conf | MQ 监听端口、节点名等服务配置 |

`CHANGE_ME` 不会放行打包。两个 consumer 的 TD_DSN、MQ_URL 可以引用 `${TD_DSN}`、`${MQ_URL}`，也可各自填写完整值。OPC YAML 的 `"${TD_DSN}"` 使用共用 TD 连接。
TD 格式：`用户名:密码@ws(主机:6041)/kayex_data`。MQ 格式：`amqp://用户名:密码@主机:5672/虚拟主机`；MQ 的特殊字符按 URI 规则编码。连接串不要写进日志。
配置目录和产物默认已加入 `.gitignore`；安装包包含实际连接配置，应按内部部署文件管理。
本次不打包 kx-inner-processor。前端界面不变；OPC 晶棒/炉次台账不初始化，相关页面可能暂时无数据。OPC 配置指向保留的 opc_inner_* 名称，防止误读 PLC 同名台账。

## 应用安装到 TD/消费服务器

解压选中的应用包，进入包目录，执行 `sudo bash install.sh`。
默认安装到 `/opt/组件名`，用 `kayex` 系统账号（可以在打包前修改），普通用户无需 Linux 登录权限。
安装器保留已有配置，新配置写入 `.new.时间`；原二进制另存 `.bak.时间`。升级会停止相应服务，安装完不自动启动。
确认现场配置后运行 `sudo systemctl start 组件名`，用 `journalctl -u 组件名 -f` 查看日志。
consumer 使用 `service.env`，OPC 使用 `config.yaml`，采集端另有 `furnaces.json`。修改后重启相应服务。
SOP 目录默认 `/home/hmi_sop_files`，暂存默认 `/home/kx-opc-client/data`；安装器按配置给服务账号创建写权限。
应用端固定使用 kayex_data；保持 `zstd/high`。默认保留 365 天（12 个月按固定天数处理），打包前可修改。

## 数据库初始化

只需 `td初始化/00_databases.sql` 和 `01_tables.sql`。先建库，再建 consumer 必需的超级表等未自动创建的表。
SQL 类型、长度、列顺序、标签和复合主键来自 171 的 3.4.0.9 元数据；所有数据列统一 zstd/high。
Web 的 opc_point_info、web_alarm 和 OPC 的 opc_data、opc_heartbeat_jump 由程序自动创建，不手工重复创建。
OPC 新点位由采集端自动加列。IF NOT EXISTS 不会补齐已有表，也不会改变已有库保留时间；脚本面向新环境。
没有 DROP DATABASE，没有历史数据迁移。本次对 171 和 172 仅做只读检查和安装包下载。
VGROUPS=20、BUFFER=256 沿用现场参数；新库 WAL_LEVEL=2，现场源库为 0。WAL=0 虽省盘但失去相应故障恢复保障，外置参数可明确选择。
两张 PLC 台账只提供源结构，当前项目没有它们的独立维护程序；硬件表同样需要对应数据来源。

TDengine 和 RabbitMQ 的详细安装步骤分别见安装包文件夹里的 `TDengine安装流程.md` 和 `RabbitMQ安装流程.md`；生成的对应安装包也包含这两份说明。
