#!/usr/bin/env python3
"""Read-only TD metadata export and local deployment SQL generation."""
import argparse,base64,json,re,urllib.request
from pathlib import Path
from datetime import datetime
ROOT=Path(__file__).resolve().parents[1]
MAP={'hmilog_alarm':'init_alarm.txt','hmi_bizvars_cam1':'init_bizvars_cam1.txt','hmi_bizvars_cam2':'init_bizvars_cam2.txt','hmi_bizvars_crucpara':'init_bizvars_crucpara.txt','hmi_bizvars_dualcam':'init_bizvars_dualcam.txt','hmi_bizvars_idnum':'init_bizvars_num.txt','hmi_ccddata':'init_ccd.txt','dbcenter_data':'init_dbcenter.txt','hmi_bizvars_idhothandle':'init_hmi_BizVarsIdHotHandle .txt','hmi_parameter':'init_hmi_parameter.txt','hmilog_record':'init_record.txt','hmilog_sop':'init_sop.txt'}


def write(p,text):
 p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text,encoding='utf-8',newline='\n')


def quote(name):
 if not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*',name):raise ValueError('Invalid metadata identifier')
 return '`'+name+'`'


def capture():
 config=(ROOT/'kx-opc-client/configs/config.yaml').read_text(encoding='utf-8')
 dsn=re.search(r'^  dsn:\s*"([^"\n]+)"',config,re.M)[1]
 auth,host=dsn.split('@ws(',1)
 host=host.split(')',1)[0]
 headers={'Authorization':'Basic '+base64.b64encode(auth.encode()).decode()}
 def query(sql):
  if not sql.startswith(('SHOW ','DESCRIBE ','SELECT ')):raise ValueError('Read-only queries only')
  req=urllib.request.Request('http://'+host+'/rest/sql',data=sql.encode(),headers=headers)
  with urllib.request.urlopen(req,timeout=30) as r:res=json.load(r)
  if res.get('code')!=0:raise RuntimeError('Metadata query failed: '+res.get('desc',''))
  return res
 snapshot={'server':host,'captured_at':datetime.now().astimezone().isoformat(),'version':query('SELECT SERVER_VERSION();')['data'][0][0],'databases':{},'tables':[]}
 for db in ['kayex_data','kx_opc','sop_hardware']:
  snapshot['databases'][db]=query('SHOW CREATE DATABASE '+quote(db)+';')['data'][0][1]
  stables=[r[0] for r in query('SHOW '+quote(db)+'.STABLES;')['data']]
  normal=[r[0] for r in query("SELECT table_name FROM information_schema.ins_tables WHERE db_name='"+db+"' AND type='NORMAL_TABLE';")['data']]
  for name in sorted(stables+normal):
   kind='STABLE' if name in stables else 'TABLE';full=quote(db)+'.'+quote(name)
   desc=query('DESCRIBE '+full+';')
   snapshot['tables'].append({'database':db,'name':name,'kind':kind,'ddl':query('SHOW CREATE '+kind+' '+full+';')['data'][0][1],'columns':desc['data'],'column_meta':desc['column_meta']})
 return snapshot


def column_section(ddl):
 start=ddl.index('(');depth=0;quoted=False
 for i in range(start,len(ddl)):
  c=ddl[i]
  if c=='`':quoted=not quoted
  if quoted:continue
  if c=='(':depth+=1
  if c==')':
   depth-=1
   if depth==0:return ddl[start+1:i],ddl[i+1:]
 raise ValueError('Unbalanced DDL')


def split_columns(text):
 parts=[];start=0;depth=0
 for i,c in enumerate(text):
  if c=='(':depth+=1
  elif c==')':depth-=1
  elif c==',' and depth==0:parts.append(text[start:i].strip());start=i+1
 parts.append(text[start:].strip());return parts


def render(table):
 cols,tail=column_section(table['ddl'])
 specs=split_columns(cols)
 output=[]
 for spec in specs:
  spec=re.sub(r"\s+(?:ENCODE|COMPRESS|LEVEL)\s+'[^']*'",'',spec,flags=re.I)
  output.append('  '+spec+" COMPRESS 'zstd' LEVEL 'high'")
 db=table['database'];name=table['name']
 if db=='kx_opc':
  db='kayex_data'
  if name in ('inner_crystal_info','inner_furnace_info'):name='opc_'+name
 # preserve tag definitions, composite keys and table options reported by the source server
 return 'CREATE '+table['kind']+' IF NOT EXISTS '+quote(db)+'.'+quote(name)+' (\n'+',\n'.join(output)+'\n)'+tail+';\n'


def generate(snapshot):
 folder=ROOT/'td初始化'
 dbsql="""-- New deployment only. 12 months is represented as a fixed 365 days.
-- IF NOT EXISTS does not change retention or schema of an existing database.
-- Do not run DROP DATABASE. Source 171 has WAL_LEVEL 0; new installs use WAL_LEVEL 2.
CREATE DATABASE IF NOT EXISTS `kayex_data`
  KEEP 365d DURATION 1d COMP 2
  VGROUPS 20 BUFFER 256 PAGES 256 PAGESIZE 4
  MAXROWS 40000 MINROWS 1000 WAL_LEVEL 2 STT_TRIGGER 1
  CACHEMODEL 'last_row' PRECISION 'ms';
CREATE DATABASE IF NOT EXISTS `sop_hardware`
  KEEP 365d DURATION 1d COMP 2 MAXROWS 40000 MINROWS 1000
  WAL_LEVEL 2 PRECISION 'ms';
"""
 write(folder/'00_databases.sql',dbsql)
 statements=[]
 for table in snapshot['tables']:
  if table['database']=='kx_opc':continue
  statements.append('-- Source: '+snapshot['server']+' / '+table['database']+'.'+table['name']+'\n-- Snapshot: '+snapshot['captured_at']+'\n'+render(table))
 write(folder/'01_tables.sql','\n'.join(statements))
 print('Generated',len(statements),'manual table definitions; automatic OPC/Web tables excluded')


def main():
 parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--refresh',action='store_true');args=parser.parse_args()
 path=ROOT/'tools/schema/schema_snapshot_171.json'
 if args.refresh:
  data=capture();write(path,json.dumps(data,ensure_ascii=False,indent=2)+'\n')
 else:data=json.loads(path.read_text(encoding='utf-8'))
 generate(data)

if __name__=='__main__':main()
