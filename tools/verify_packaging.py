"""Build all six packages with local-only fake credentials, then inspect archives."""
import hashlib,json,shutil,sys,tarfile,tempfile
from pathlib import Path
import package

def main():
 with tempfile.TemporaryDirectory(prefix='kayex-verification-') as work:
  work=Path(work);profile=work/'site';package.init_profile(profile)
  p=profile/'settings.json';s=json.loads(p.read_text(encoding='utf-8'));s.update(td_dsn='verify:local-only@ws(127.0.0.1:6041)/kayex_data',mq_url='amqp://verify:local-only@127.0.0.1:5672/');package.write(p,json.dumps(s,indent=2)+'\n')
  p=profile/'kx-opc-web/config.yaml';package.write(p,p.read_text(encoding='utf-8').replace('CHANGE_ME','local-verification-only'))
  p=profile/'kx-opc-client/furnaces.json';package.write(p,json.dumps({'port':4840,'areas':[],'furnaces':['127.0.0.1'],'exclude':[]}))
  package.make_bundle(profile,'verification',work/'out',package.COMPONENTS)
  for component in package.COMPONENTS:
   path=work/'out'/(component+'-verification-linux-amd64.tar.gz')
   expected=path.with_suffix('.gz.sha256').read_text(encoding='utf-8').split()[0]
   with path.open('rb') as f:assert hashlib.file_digest(f,'sha256').hexdigest()==expected
   with tarfile.open(path) as archive:
    names=archive.getnames();root=names[0].split('/')[0];checks=archive.extractfile(root+'/SHA256SUMS').read().decode().splitlines()
    for line in checks:
     digest,relative=line.split('  ',1);data=archive.extractfile(root+'/'+relative).read();assert hashlib.sha256(data).hexdigest()==digest
    if component in package.APPS:
     binary=archive.extractfile(root+'/app/'+component).read();assert binary[:4]==b'\x7fELF' and binary[4]==2
     assert archive.getmember(root+'/app/'+component).mode==0o755
     assert not any('/node_modules/' in name or '/src/' in name for name in names)
     script=archive.extractfile(root+'/install.sh').read().decode();assert component in script and '\r' not in script
     if component in package.ENV_DEFAULTS:
      data=archive.extractfile(root+'/app/service.env').read().decode();assert '${' not in data and 'local-only' in data
    elif component=='tdengine':
     sql=archive.extractfile(root+'/sql/00_databases.sql').read().decode();assert 'KEEP 365d' in sql
     sql=archive.extractfile(root+'/sql/01_tables.sql').read().decode();assert 'opc_inner_' not in sql and '`opc_point_info`' not in sql and '`opc_data`' not in sql
     assert any(name.endswith('/TDengine安装流程.md') for name in names)
    else:assert any(name.endswith('/RabbitMQ安装流程.md') for name in names)
   print('ARCHIVE VERIFIED:',component,flush=True)
 print('ALL SIX COMPONENTS VERIFIED; temporary fake-credential artifacts removed.',flush=True)

if __name__=='__main__':main()
