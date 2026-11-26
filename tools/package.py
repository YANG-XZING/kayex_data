#!/usr/bin/env python3
"""Selection-based Linux packaging; all site settings live outside source code."""
import argparse, hashlib, json, os, re, shutil, subprocess, tarfile, tempfile
from pathlib import Path, PurePosixPath
from datetime import datetime
ROOT=Path(__file__).resolve().parents[1]
APPS={'bigdata_consumer':'.','hmi_consumer':'.','kx-opc-client':'./cmd/kx-opc-client','kx-opc-web':'./cmd/kx-opc-web'}
COMPONENTS=list(APPS)+['tdengine','rabbitmq']
ENV_DEFAULTS={
'bigdata_consumer':{'MQ_URL':'${MQ_URL}','TD_DSN':'${TD_DSN}','PLC_QUEUE':'dbcenter.plc.direct','PLC_PATCH_QUEUE':'dbcenter.plc.sqlite','PLC_STABLE':'dbcenter_data','PLC_TABLE_PREFIX':'dc_','PLC_STATE_POINT':'33.0-0','PLC_PREFETCH':'2000','PLC_WORKERS':'20','PLC_BATCH_SIZE':'10','PLC_WORKER_BUFFER':'200','PLC_MAX_OPEN_CONNS':'500','PLC_MAX_IDLE_CONNS':'200','PLC_MAX_WAIT':'500ms','PLC_DB_TIMEOUT':'10s','PLC_DB_RETRY':'5s'},
'hmi_consumer':{'MQ_URL':'${MQ_URL}','TD_DSN':'${TD_DSN}','SOP_ROOT':'/home/hmi_sop_files','HMI_QUEUE_PARAM':'HMI_Parameter-30min','HMI_QUEUE_HOT_HANDLE':'HMI_BizVarsIdHotHandle-24h','HMI_QUEUE_CCD':'HMI_CCD_Data','HMI_QUEUE_RECORD':'HMI_Record','HMI_QUEUE_SOP':'HMI_SOPConfig','HMI_QUEUE_ALARM':'HMI_Alarm','HMI_QUEUE_CAM1':'HMI_BizVarsCam1','HMI_QUEUE_CAM2':'HMI_BizVarsCam2','HMI_QUEUE_CRUC_PARA':'HMI_BizVarsCrucPara','HMI_QUEUE_DUAL_CAM':'HMI_BizVarsDualCam','HMI_QUEUE_ID_NUM':'HMI_BizVarsIdNum','LOG_LEVEL':'INFO','HMI_MAX_OPEN_CONNS':'100','HMI_MAX_IDLE_CONNS':'50','HMI_BATCH_SIZE':'100','HMI_PREFETCH':'100','HMI_SOP_RETENTION':'720h','HMI_SOP_SCAN_INTERVAL':'24h','HMI_DB_RETRY':'5s'}}


def write(p,text):
 p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text,encoding='utf-8',newline='\n')


def safe_path(value):
 if not isinstance(value,str) or not re.fullmatch(r'/[A-Za-z0-9_./-]+',value) or '..' in PurePosixPath(value).parts or str(PurePosixPath(value))=='/':raise ValueError('Linux 路径须为绝对路径，且不含空格、特殊符号或 ..')
 return value.rstrip('/')


def ensure_component_templates(profile):
 for app,data in ENV_DEFAULTS.items():
  p=profile/app/'environment.json'
  if not p.exists():write(p,json.dumps(data,ensure_ascii=False,indent=2)+'\n')
 for name,data in {
 'tdengine/database.json':{'keep_days':365,'duration_days':1,'vgroups':20,'buffer_mb':256,'maxrows':40000,'minrows':1000,'wal_level':2},
 'rabbitmq/install.json':{'rabbit_home':'/home/rabbitmq_install/rabbitmq_server-3.8.11','erlang_home':'/usr/local/erlang','openssl_home':'/usr/local/ssl','service_user':'rabbitmq','data_dir':'/var/lib/rabbitmq/mnesia','log_dir':'/var/log/rabbitmq'}
 }.items():
  if not (profile/name).exists():write(profile/name,json.dumps(data,ensure_ascii=False,indent=2)+'\n')
 for name,text in {
 'tdengine/taos.cfg':'# Set a resolvable fqdn for the target machine.\n# fqdn td-host\nserverPort 6030\ndataDir /var/lib/taos\nlogDir /var/log/taos\n',
 'rabbitmq/rabbitmq.conf':'listeners.tcp.default = 5672\nmanagement.tcp.port = 15672\n',
 'rabbitmq/rabbitmq-env.conf':'# Optional: NODENAME=rabbit@your-hostname\n# Do not copy production Erlang cookies or node data.\n'
 }.items():
  if not (profile/name).exists():write(profile/name,text)


def init_profile(profile):
 profile.mkdir(parents=True,exist_ok=False)
 write(profile/'settings.json',json.dumps({'td_dsn':'CHANGE_ME@ws(10.65.104.171:6041)/kayex_data','mq_url':'amqp://CHANGE_ME@10.65.104.172:5672/','install_base':'/opt','service_user':'kayex'},ensure_ascii=False,indent=2)+'\n')
 for app in ('kx-opc-client','kx-opc-web'):
  text=(ROOT/app/'configs/config.yaml').read_text(encoding='utf-8')
  text=re.sub(r'(?m)^  dsn:.*$','  dsn: "${TD_DSN}"',text)
  text=re.sub(r'(?m)^  # dsn:.*\n?','',text)
  if app=='kx-opc-web':text=re.sub(r'password: "[^"]*"','password: "CHANGE_ME"',text)
  write(profile/app/'config.yaml',text)
 shutil.copyfile(ROOT/'kx-opc-client/configs/furnaces.json',profile/'kx-opc-client/furnaces.json')
 ensure_component_templates(profile)
 print('配置模板已创建：'+str(profile))


def selection(raw):
 values=list(dict.fromkeys(raw.split(',')))
 if not values or any(x not in COMPONENTS for x in values):raise ValueError('请勾选有效组件')
 return values


def load_profile(profile,selected):
 settings=json.loads((profile/'settings.json').read_text(encoding='utf-8-sig'));configs={}
 def expand(value):
  value=str(value).replace('${TD_DSN}',settings.get('td_dsn','')).replace('${MQ_URL}',settings.get('mq_url',''))
  if any(c in value for c in '\r\n\x00') or 'CHANGE_ME' in value or '${' in value:raise ValueError('所选组件还有未填写参数，请检查外置配置')
  return value
 def dsn(value):
  if not re.fullmatch(r'[^\r\n:]+:[^\r\n]+@ws\([^\r\n]+\)/kayex_data(?:\?[^\r\n]*)?',value):raise ValueError('TD 连接须为 用户:密码@ws(地址:6041)/kayex_data')
 def account(value):
  if not re.fullmatch(r'[a-z_][a-z0-9_-]{0,30}',value) or value=='root':raise ValueError('请填写独立的合法 Linux 服务账号')
 if any(x in APPS for x in selected):safe_path(settings['install_base']);account(settings['service_user'])
 for app in selected:
  if app in ENV_DEFAULTS:
   values=json.loads((profile/app/'environment.json').read_text(encoding='utf-8-sig'))
   if not set(ENV_DEFAULTS[app]).issubset(values):raise ValueError(app+' 环境配置缺少必要项')
   values={k:expand(v) for k,v in values.items()}
   if any(not re.fullmatch(r'[A-Z][A-Z0-9_]+',k) for k in values):raise ValueError('环境变量名无效')
   dsn(values['TD_DSN'])
   if not values['MQ_URL'].startswith(('amqp://','amqps://')):raise ValueError('MQ 连接格式无效')
   for key,val in values.items():
    if key.endswith(('_CONNS','_SIZE','_WORKERS','_BUFFER','_PREFETCH')) and (not val.isdigit() or int(val)<1):raise ValueError(app+' 容量参数必须为正整数')
    if key.endswith(('_WAIT','_TIMEOUT','_RETRY','_RETENTION','_INTERVAL')) and not re.fullmatch(r'(?:\d+(?:\.\d+)?(?:h|m|s|ms|us|ns))+',val):raise ValueError(app+' 时长格式应为 500ms、5s、24h 等')
   prefix='PLC_' if app=='bigdata_consumer' else 'HMI_'
   if int(values[prefix+'MAX_IDLE_CONNS'])>int(values[prefix+'MAX_OPEN_CONNS']):raise ValueError('空闲连接数不能大于最大连接数')
   if app=='hmi_consumer':safe_path(values['SOP_ROOT'])
   for key in ['PLC_STABLE','PLC_TABLE_PREFIX']:
    if key in values and not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*',values[key]):raise ValueError('PLC 表名或前缀不合法')
   configs[app]=values
  elif app in APPS:
   try:
    import yaml
    text=(profile/app/'config.yaml').read_text(encoding='utf-8-sig').replace('"${TD_DSN}"',json.dumps(expand('${TD_DSN}'),ensure_ascii=False))
    if 'CHANGE_ME' in text or '${' in text:raise ValueError('所选 OPC 配置还有未填写参数')
    data=yaml.safe_load(text)
   except ImportError:raise ValueError('请先安装 tools/requirements.txt 中的 PyYAML')
   except yaml.YAMLError:raise ValueError(app+' YAML 格式错误（配置内容不写入日志）')
   dsn(data['tdengine']['dsn'])
   if (data['tdengine']['compress'],data['tdengine']['compress_level'])!=('zstd','high'):raise ValueError('列压缩应保持 zstd/high')
   if app=='kx-opc-web':
    accounts=data.get('accounts',[])
    if not accounts or any(not a.get('user') or not a.get('password') or a.get('role') not in ('admin','customer') for a in accounts):raise ValueError('请填写网站账号、密码和角色')
    if data.get('crystal',{}).get('ledger_table')=='inner_crystal_info':raise ValueError('OPC 台账不能指向 PLC 台账')
   else:
    safe_path(data['tdengine']['spool_dir'])
    if not json.loads((profile/app/'furnaces.json').read_text(encoding='utf-8-sig')).get('furnaces'):raise ValueError('炉台清单不能为空')
   configs[app]=text
  elif app=='tdengine':
   data=json.loads((profile/'tdengine/database.json').read_text(encoding='utf-8-sig'))
   if any(type(data[k]) is not int or data[k]<1 for k in ['keep_days','duration_days','vgroups','buffer_mb','minrows','maxrows']):raise ValueError('TD 容量和保留参数必须为正整数')
   if data['duration_days']>data['keep_days'] or data['minrows']>data['maxrows'] or data['wal_level'] not in (0,1,2):raise ValueError('TD 参数组合无效')
   configs[app]=data
  else:
   data=json.loads((profile/'rabbitmq/install.json').read_text(encoding='utf-8-sig'))
   for key in ['rabbit_home','erlang_home','openssl_home','data_dir','log_dir']:safe_path(data[key])
   account(data['service_user']);configs[app]=data
 return settings,configs


def service(app,settings):
 base=settings['install_base']+'/'+app;user=settings['service_user']
 suffix=' -config '+base+'/config.yaml' if app.startswith('kx-opc-') else ''
 environment='EnvironmentFile='+base+'/service.env\n' if not suffix else ''
 return f"""[Unit]
Description=Kayex {app}
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User={user}
Group={user}
WorkingDirectory={base}
ExecStart={base}/{app}{suffix}
{environment}Environment=TZ=Asia/Shanghai
Restart=on-failure
RestartSec=10
TimeoutStopSec=30
LimitNOFILE=65535
UMask=0027
[Install]
WantedBy=multi-user.target
"""


def installer(app,settings,dirs):
 user=settings['service_user'];base=settings['install_base'];directories=' '.join("'"+safe_path(x)+"'" for x in dirs+[base+'/'+app+'/logs'])
 return f"""#!/usr/bin/env bash
set -euo pipefail
[[ $EUID -eq 0 ]] || {{ echo '请使用 sudo bash install.sh'; exit 1; }}
SRC="$(cd "$(dirname "$0")" && pwd)"
APP='{app}'
DEST='{base}/{app}'
USER_NAME='{user}'
getent group "$USER_NAME" >/dev/null || groupadd --system "$USER_NAME"
id "$USER_NAME" >/dev/null 2>&1 || useradd --system --gid "$USER_NAME" --no-create-home --shell /sbin/nologin "$USER_NAME"
install -d -m 0750 -o "$USER_NAME" -g "$USER_NAME" "$DEST" "$DEST/logs" "$DEST/cache"
STAMP="$(date +%Y%m%d%H%M%S)-$$"
if systemctl is-active --quiet "$APP"; then systemctl stop "$APP"; fi
if [[ -f "$DEST/$APP" ]]; then cp -p "$DEST/$APP" "$DEST/$APP.bak.$STAMP"; fi
install -m 0755 "$SRC/app/$APP" "$DEST/$APP"
for name in config.yaml furnaces.json service.env; do
 [[ -f "$SRC/app/$name" ]] || continue
 target="$DEST/$name"
 [[ ! -f "$target" ]] || target="$target.new.$STAMP"
 install -m 0640 -o root -g "$USER_NAME" "$SRC/app/$name" "$target"
done
for dir in {directories or "'$DEST/logs'"}; do
 install -d -m 0750 -o "$USER_NAME" -g "$USER_NAME" "$dir"
done
install -m 0644 "$SRC/$APP.service" "/etc/systemd/system/$APP.service"
systemctl daemon-reload
systemctl enable "$APP"
echo '安装完成，尚未启动。旧配置保留，新配置另存为 .new.时间；核对后显式替换并启动服务。'
echo '本脚本不执行 SQL、不迁移或删除数据库。'
"""


def rabbit_service(config):
 return f"""[Unit]
Description=RabbitMQ Message Queue
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User={config['service_user']}
Group={config['service_user']}
Environment="PATH={config['erlang_home']}/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin"
Environment="RABBITMQ_MNESIA_BASE={config['data_dir']}"
Environment="RABBITMQ_LOG_BASE={config['log_dir']}"
ExecStart={config['rabbit_home']}/sbin/rabbitmq-server
ExecStop={config['rabbit_home']}/sbin/rabbitmqctl stop
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
[Install]
WantedBy=multi-user.target
"""


def run(args,cwd,env=None):
 print('执行：'+' '.join(str(x) for x in args),flush=True);subprocess.run(args,cwd=cwd,env=env,check=True)


def archive_stage(stage,output,manifest):
 write(stage/'manifest.json',json.dumps(manifest,ensure_ascii=False,indent=2)+'\n');checks=[]
 for p in sorted(stage.rglob('*')):
  if p.is_file():
   with p.open('rb') as f:checks.append(hashlib.file_digest(f,'sha256').hexdigest()+'  '+p.relative_to(stage).as_posix())
 write(stage/'SHA256SUMS','\n'.join(checks)+'\n')
 target=output/(stage.name+'.tar.gz');partial=output/(stage.name+'.partial')
 try:
  with tarfile.open(partial,'w:gz') as archive:
   for p in sorted(stage.rglob('*')):
    info=archive.gettarinfo(str(p),arcname=stage.name+'/'+p.relative_to(stage).as_posix())
    info.uid=info.gid=0;info.uname=info.gname='root'
    info.mode=0o755 if p.is_dir() or p.name in APPS or p.suffix=='.sh' else 0o600 if p.suffix in ('.env','.yaml','.cfg','.conf') else 0o644
    if p.is_file():
     with p.open('rb') as f:archive.addfile(info,f)
    else:archive.addfile(info)
  partial.replace(target)
 finally:
  if partial.exists():partial.unlink()
 with target.open('rb') as f:digest=hashlib.file_digest(f,'sha256').hexdigest()
 write(target.with_suffix(target.suffix+'.sha256'),digest+'  '+target.name+'\n')
 print('已生成：'+str(target),flush=True)


def make_bundle(profile,version,output,selected):
 settings,configs=load_profile(profile,selected)
 if not re.fullmatch(r'[A-Za-z0-9_.-]+',version):raise ValueError('版本号只支持字母、数字、点、下划线、连字符')
 output.mkdir(parents=True,exist_ok=True)
 for name in selected:
  if (output/(name+'-'+version+'-linux-amd64.tar.gz')).exists():raise ValueError('已有同版本安装包，请修改版本号')
 env=os.environ.copy();env.update(GOOS='linux',GOARCH='amd64',CGO_ENABLED='0')
 with tempfile.TemporaryDirectory(prefix='kayex-build-') as temp:
  work=Path(temp)
  for component in selected:
   print('正在打包：'+component,flush=True);stage=work/(component+'-'+version+'-linux-amd64');stage.mkdir()
   if component in APPS:
    src=ROOT/component
    if component=='kx-opc-web':
     src=work/'web-source';shutil.copytree(ROOT/component,src,ignore=shutil.ignore_patterns('node_modules','dist','.git'))
     npm=shutil.which('npm.cmd') or shutil.which('npm')
     if not npm:raise ValueError('Web 打包需要 Node.js/npm')
     run([npm,'ci','--no-audit','--no-fund'],src/'web');run([npm,'run','build'],src/'web')
    appdir=stage/'app';appdir.mkdir()
    flags='-s -w'+(' -X main.version='+version if component.startswith('kx-opc-') else '')
    run(['go','build','-trimpath','-ldflags',flags,'-o',str(appdir/component),APPS[component]],src,env)
    dirs=[]
    if component in ENV_DEFAULTS:
     write(appdir/'service.env',''.join(k+'='+json.dumps(v,ensure_ascii=False)+'\n' for k,v in configs[component].items()))
     if component=='hmi_consumer':dirs=[configs[component]['SOP_ROOT']]
    else:
     write(appdir/'config.yaml',configs[component])
     if component=='kx-opc-client':
      import yaml
      dirs=[yaml.safe_load(configs[component])['tdengine']['spool_dir']]
      shutil.copyfile(profile/component/'furnaces.json',appdir/'furnaces.json')
    write(stage/(component+'.service'),service(component,settings));write(stage/'install.sh',installer(component,settings,dirs))
   elif component=='tdengine':
    shutil.copyfile(ROOT/'td和mq安装包/tdengine-tsdb-oss-3.4.0.9-linux-x64.tar.gz',stage/'tdengine-tsdb-oss-3.4.0.9-linux-x64.tar.gz')
    shutil.copytree(ROOT/'td初始化',stage/'sql');db=configs[component]
    sql=(stage/'sql/00_databases.sql').read_text(encoding='utf-8')
    for pattern,value in {r'KEEP 365d':'KEEP '+str(db['keep_days'])+'d',r'DURATION 1d':'DURATION '+str(db['duration_days'])+'d',r'VGROUPS 20':'VGROUPS '+str(db['vgroups']),r'BUFFER 256':'BUFFER '+str(db['buffer_mb']),r'MAXROWS 40000':'MAXROWS '+str(db['maxrows']),r'MINROWS 1000':'MINROWS '+str(db['minrows']),r'WAL_LEVEL 2':'WAL_LEVEL '+str(db['wal_level'])}.items():sql=re.sub(pattern,value,sql)
    write(stage/'sql/00_databases.sql',sql);shutil.copytree(profile/'tdengine',stage/'config')
   else:
    shutil.copytree(ROOT/'td和mq安装包/rabbitmq-centos7-x86_64',stage/'installers');shutil.copytree(profile/'rabbitmq',stage/'config');write(stage/'rabbitmq-server.service',rabbit_service(configs[component]))
   shutil.copyfile(ROOT/'DEPLOYMENT.md',stage/'DEPLOYMENT.md')
   if component in ('tdengine','rabbitmq'):
    guide='TDengine安装流程.md' if component=='tdengine' else 'RabbitMQ安装流程.md'
    shutil.copyfile(ROOT/'td和mq安装包'/guide,stage/guide)
   manifest={'component':component,'version':version,'platform':'linux/amd64','built_at':datetime.now().astimezone().isoformat(),'schema_source':'171 / 3.4.0.9','manual_sql_excludes_opc_tables':True}
   archive_stage(stage,output,manifest)


def main():
 parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('action',choices=['init','check','build'])
 parser.add_argument('--profile',type=Path,default=ROOT/'profiles/site');parser.add_argument('--version',default=datetime.now().strftime('%Y%m%d%H%M%S'));parser.add_argument('--output',type=Path,default=ROOT/'dist');parser.add_argument('--components',default=','.join(COMPONENTS));args=parser.parse_args()
 try:
  selected=selection(args.components)
  if args.action=='init':init_profile(args.profile)
  elif args.action=='check':load_profile(args.profile,selected);print('所选组件基础配置校验通过。')
  else:make_bundle(args.profile,args.version,args.output,selected)
 except (ValueError,OSError,subprocess.CalledProcessError,KeyError,TypeError) as exc:
  print('失败：'+str(exc) if type(exc) is ValueError else '失败：配置格式、文件或构建命令出错，请检查上方输出。');return 1
 return 0
if __name__=='__main__':raise SystemExit(main())
