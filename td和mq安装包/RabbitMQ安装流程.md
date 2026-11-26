# RabbitMQ 3.8.11 / Erlang 23.2 安装流程（CentOS 7 x86_64）

## 安装文件

这些文件从 10.65.104.172 的 /home/rabbitmq_install 原样复制，已逐个 SHA256 对比。校验值见同目录 SHA256SUMS。
- rabbitmq-server-generic-unix-3.8.11.tar：RabbitMQ 通用 Unix 二进制发行包。
- otp_src_23.2.tar.gz：Erlang/OTP 23.2 源码，需要编译。
- openssl-1.1.1i.tar.gz：供 Erlang SSL 功能使用的 OpenSSL 源码。
- perl-5.36.0.tar.gz：现场留存的可选 Perl 源码；172 的系统 perl 实际是 5.16.3，不必仅因有此文件就替换系统 Perl。
- ncurses-devel-5.9-14.20130511.el7_4.x86_64.rpm：Erlang 编译所需相关头文件包。

这五个文件**不等于一台干净系统的全部离线依赖**。172 已有 gcc/gcc-c++ 4.8.5、make 3.82、ncurses-libs、glibc 2.17 和系统 Perl；全新离线机器还需要相应系统 RPM 与依赖。安装前先核对，不能用 --nodeps 强行忽略依赖。

## 目录和账号

现场程序在 `/home/rabbitmq_install/rabbitmq_server-3.8.11`，Erlang 在 `/usr/local/erlang`，OpenSSL 在 `/usr/local/ssl`。
打包外置配置 rabbitmq/install.json 可修改这些路径；生成的 systemd 服务会使用其中 RabbitMQ/Erlang 路径。
新包默认把消息数据放 `/var/lib/rabbitmq/mnesia`，日志放 `/var/log/rabbitmq`；这两个目录要属于 rabbitmq 系统账号。
`rabbitmq` Linux 用户、MQ 业务用户名、管理页面账号分别是不同概念。不要直接拷贝现场运行数据、.erlang.cookie 或用户数据库到新节点。

## 安装依赖与 Erlang

解压外层部署包后，先记录包根目录 `PACKAGE_DIR="$(pwd)"`，再进入 `installers/` 执行源码包相关命令；配置和 service 文件位于外层包根目录。

下列命令使用默认路径；如果改了 install.json，命令中路径要同步修改。以 root 安装依赖和编译，MQ 服务本身以 rabbitmq 运行。

```bash
uname -m
cat /etc/os-release
rpm -q gcc gcc-c++ make ncurses-devel ncurses-libs perl glibc
rpm -Uvh --test ncurses-devel-5.9-14.20130511.el7_4.x86_64.rpm
# 仅在依赖检查通过且确实缺少时安装该 RPM
rpm -Uvh ncurses-devel-5.9-14.20130511.el7_4.x86_64.rpm
```

安装自带 OpenSSL 到独立目录，不替换 CentOS 系统的 /usr/bin/openssl 或系统库：

```bash
tar xzf openssl-1.1.1i.tar.gz
cd openssl-1.1.1i
./config --prefix=/usr/local/ssl --openssldir=/usr/local/ssl shared
make -j2
make install_sw
cd ..
```

确保该 OpenSSL 库在 Erlang 构建与运行时可被动态链接器找到（例如按本机实际 lib 目录配置 /etc/ld.so.conf.d 的独立文件后执行 ldconfig）。不要覆盖系统原来的 libssl/libcrypto。

```bash
tar xzf otp_src_23.2.tar.gz
cd otp_src_23.2
./configure --prefix=/usr/local/erlang --with-ssl=/usr/local/ssl --without-javac
make -j2
make install
/usr/local/erlang/bin/erl -noshell -eval 'io:format("OTP ~s~n", [erlang:system_info(otp_release)]), halt().'
cd ..
```

检查 configure 的 SSL/crypto 结果，不要在 crypto/ssl 未启用时继续作为成功安装。编译会使用 CPU，-j2 可以按服务器规格修改。

## 安装 RabbitMQ 与配置

```bash
getent group rabbitmq || groupadd --system rabbitmq
id rabbitmq || useradd --system --gid rabbitmq --create-home --home-dir /var/lib/rabbitmq --shell /sbin/nologin rabbitmq
mkdir -p /home/rabbitmq_install /var/lib/rabbitmq/mnesia /var/log/rabbitmq
tar xf rabbitmq-server-generic-unix-3.8.11.tar -C /home/rabbitmq_install
chown -R rabbitmq:rabbitmq /home/rabbitmq_install/rabbitmq_server-3.8.11 /var/lib/rabbitmq /var/log/rabbitmq
```

把独立部署包的 config/rabbitmq.conf、config/rabbitmq-env.conf 放到 `$RABBITMQ_HOME/etc/rabbitmq/`，交给 rabbitmq 用户读取。
使用包中 rabbitmq-server.service 安装到 /etc/systemd/system/；服务以前台进程运行，**不要加 -detached**，由 systemd 管理。

```bash
install -m 0644 "$PACKAGE_DIR/rabbitmq-server.service" /etc/systemd/system/rabbitmq-server.service
systemctl daemon-reload
systemctl enable --now rabbitmq-server
systemctl status rabbitmq-server --no-pager
journalctl -u rabbitmq-server -n 100 --no-pager
```

管理 CLI 要使用与服务相同的 Erlang 路径和系统账号，以免 cookie/权限不一致。例如：

```bash
sudo -u rabbitmq env PATH=/usr/local/erlang/bin:/usr/bin:/bin /home/rabbitmq_install/rabbitmq_server-3.8.11/sbin/rabbitmqctl status
```

需要管理页面时，同样以 rabbitmq 账号启用 rabbitmq_management 插件；默认 AMQP 5672，管理 HTTP 15672。仅开放所需访问网段。

## MQ 业务账号与队列

为新环境创建自己的 MQ 业务账号和密码，并授予对应 vhost 的 configure/write/read 权限；不要把 Linux 用户名误当成已经存在的 MQ 用户。
管理页面账号可另外设置 administrator 标签。不要依赖 guest 的远程登录，也不要把所有消费账号都赋管理员角色。
业务账号、密码、vhost 必须与两个 consumer 外置配置的 MQ_URL 一致。密码含 @、/、: 等字符时按 URI 规则编码。
consumer 的默认队列名来自仓库；PLC/HMI 队列声明带 x-single-active-consumer。若同名队列已由其他系统创建，durable 和参数必须兼容，否则会出现 PRECONDITION_FAILED；不要直接删除正在使用的队列来绕过此错误。
本次从 172 获取安装文件，不导出生产用户、消息或队列状态。新节点的生产者连接地址也要另行调整。

官方参考：[通用 Unix 包](https://www.rabbitmq.com/docs/install-generic-unix)、[目录与环境配置](https://www.rabbitmq.com/docs/configure)。这里固定使用现场包版本，不自动升级 RabbitMQ/Erlang。
