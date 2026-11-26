# 本地离线安装文件（不上传 Git）

Git 中只保留安装说明和 SHA256SUMS；TDengine、RabbitMQ、Erlang、OpenSSL、Perl、ncurses 等安装文件保留在本地。

在新电脑上打包中间件前，需要自行补齐以下相对路径：

- tdengine-tsdb-oss-3.4.0.9-linux-x64.tar.gz
- rabbitmq-centos7-x86_64/ncurses-devel-5.9-14.20130511.el7_4.x86_64.rpm
- rabbitmq-centos7-x86_64/openssl-1.1.1i.tar.gz
- rabbitmq-centos7-x86_64/otp_src_23.2.tar.gz
- rabbitmq-centos7-x86_64/perl-5.36.0.tar.gz
- rabbitmq-centos7-x86_64/rabbitmq-server-generic-unix-3.8.11.tar

RabbitMQ 相关文件来自 172 的 /home/rabbitmq_install。补齐后与 rabbitmq-centos7-x86_64/SHA256SUMS 核对。
只打包业务程序时不需要这些中间件安装文件。
具体安装步骤见 TDengine安装流程.md 和 RabbitMQ安装流程.md。
