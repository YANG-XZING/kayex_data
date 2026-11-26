# TDengine 3.4.0.9 安装流程（CentOS 7 x86_64）

## 包与安装位置

本地原包：tdengine-tsdb-oss-3.4.0.9-linux-x64.tar.gz，与 171 运行版本一致。
SHA256：3498ebf65aaab019df2389b176c2a30c5c15037bbe80c37ae8ef842ff0cad9b5。
通常以 root 安装，发行包安装器的系统安装默认程序在 `/usr/local/taos`，配置在 `/etc/taos`，数据在 `/var/lib/taos`，日志在 `/var/log/taos`。
安装时如果选择了自定义目录，以安装器提示和实际服务文件为准；不要把数据目录放在临时解压目录。
171 的 taosd / taosadapter systemd 服务未指定 User，即使用 systemd 的默认 root 身份。应用消费服务是另一套 kayex 系统账号，两者不要混淆。

## 安装

1. 确认 `uname -m` 为 x86_64，系统为 CentOS 7，磁盘足够，并记录主机名。配置稳定可解析的 FQDN；客户端必须能解析 TD 节点返回的地址。
2. 将 TD 独立安装包放到 TD/消费服务器。用其 `.sha256` 和包内 SHA256SUMS 核对文件。
3. 解压外层部署包，再解压原始 TD 发行包：

```bash
tar xzf tdengine-tsdb-oss-3.4.0.9-linux-x64.tar.gz
cd tdengine-tsdb-oss-3.4.0.9
sudo bash install.sh
```

4. 按发行包提示完成安装，核对 `/etc/taos/taos.cfg`。外层包 `config/taos.cfg` 是可修改模板，**不会自动覆盖系统配置**；应用 dataDir、logDir、fqdn 时要确认目录和已有数据。
5. 启动服务：

```bash
sudo systemctl enable --now taosd
sudo systemctl enable --now taosadapter
systemctl status taosd taosadapter --no-pager
taos -V
```

6. 本项目 consumer/OPC/Web 使用 taosAdapter WebSocket 端口 6041，原生 CLI 使用 6030。只向需要访问的网段放行。先确认连接可用，再初始化。

## 数据库账号、初始化和压缩

Linux root/kayex 与 TD 数据库账号是不同概念。仓库原配置曾使用 TD root；新环境应自行设定数据库登录与密码，并在外置 settings.json 或各程序配置中填写。
账号需要对新建 kayex_data 的读写权限；OPC 自动建表/加列需要相应 DDL 权限，Web 也需要创建自己的普通表。
使用具有建库权限的账号登录 `taos`，不要把密码拼在可被进程列表读取的命令行里。在 CLI 中按顺序执行：

```sql
source /绝对路径/sql/00_databases.sql;
source /绝对路径/sql/01_tables.sql;
SHOW DATABASES;
SHOW kayex_data.STABLES;
```

检查每一条返回结果。默认 KEEP 365d，即 12 个月按 365 天表示；可以在打包前的 tdengine/database.json 修改。
需长期一致时，同时修改 OPC 配置里的 database_options；库已存在时不会自动改 KEEP。
每个数据列用 `COMPRESS 'zstd' LEVEL 'high'`；标签不设置列压缩。high 以更多 CPU 时间换更高压缩比。
新库默认 WAL_LEVEL 2；171 源库是 0。VGROUPS=20 和 BUFFER=256 来自现网规格，低配置机器需要按内存和负载调整。
本流程不对已有生产库执行 ALTER、COMPACT 或删库；修改压缩并不会立即重新压缩历史数据。
不要手工创建 Web 的两张表或 OPC 的超级表；应用会自动创建。OPC 晶棒台账本次不建。

官方参考：[安装文档](https://docs.tdengine.com/get-started/deploy-from-package/)、[列压缩](https://docs.tdengine.com/tdengine-sql/data-write/compress/)。包内脚本和现场实测版本优先。
