"""Restore a recent stored encrypted artifact; production is never contacted."""
import datetime,hashlib,json,os,pathlib,re,shutil,subprocess,tempfile,time,urllib.request,urllib.parse,uuid,zipfile,io
REPO='BipoXDXD/stillon';API='https://api.github.com/'
IMAGE='postgres@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873'
TOKEN=os.environ.pop('GH_TOKEN');IDENTITY=os.environ.pop('AGE_IDENTITY')
class Redirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,req,fp,code,msg,headers,newurl):
  if urllib.parse.urlparse(newurl).scheme!='https':raise RuntimeError('insecure_redirect')
  r=super().redirect_request(req,fp,code,msg,headers,newurl)
  if urllib.parse.urlparse(newurl).hostname!='api.github.com':r.remove_header('Authorization')
  return r
opener=urllib.request.build_opener(Redirect())
def get(path,raw=False):
 req=urllib.request.Request(API+path,headers={'User-Agent':'StillOn-stored-restore-audit','Accept':'application/vnd.github+json','Authorization':'Bearer '+TOKEN})
 with opener.open(req,timeout=40) as r:data=r.read(16*1024*1024+1)
 if len(data)>16*1024*1024:raise RuntimeError('response_too_large')
 return data if raw else json.loads(data)
def run(args,data=None,timeout=90):
 p=subprocess.run(args,input=data,capture_output=True,timeout=timeout)
 if p.returncode:raise RuntimeError('isolated_step_failed')
 return p.stdout
name='stillon-audit-'+uuid.uuid4().hex[:12];created=False
work=pathlib.Path(tempfile.mkdtemp(prefix='stillon-audit-',dir=os.environ['RUNNER_TEMP']));os.chmod(work,0o700)
try:
 artifacts=get(f'repos/{REPO}/actions/artifacts?per_page=100')['artifacts']
 candidates=[a for a in artifacts if not a['expired'] and a['name'].startswith('stillon-backup-') and a.get('workflow_run',{}).get('head_branch')=='main']
 a=max(candidates,key=lambda a:a['created_at'])
 age=(datetime.datetime.now(datetime.timezone.utc)-datetime.datetime.fromisoformat(a['created_at'].replace('Z','+00:00'))).total_seconds()
 if not 0<=age<=26*3600:raise RuntimeError('backup_age_outside_policy')
 rid=a['workflow_run']['id'];parent=get(f'repos/{REPO}/actions/runs/{rid}')
 if parent['conclusion']!='success' or parent['head_branch']!='main' or parent['path']!='.github/workflows/backup.yml':raise RuntimeError('untrusted_backup_run')
 archive=get(f"repos/{REPO}/actions/artifacts/{a['id']}/zip",True)
 if a.get('digest')!='sha256:'+hashlib.sha256(archive).hexdigest():raise RuntimeError('stored_digest_mismatch')
 with zipfile.ZipFile(io.BytesIO(archive)) as z:
  entries=[e for e in z.infolist() if not e.is_dir()]
  if len(entries)!=1 or entries[0].file_size>16*1024*1024:raise RuntimeError('unexpected_archive')
  cipher=z.read(entries[0])
 if not cipher.startswith(b'age-encryption.org/v1\n'):raise RuntimeError('not_age')
 jobs=get(f'repos/{REPO}/actions/runs/{rid}/jobs')['jobs'];job=next(j for j in jobs if j['name']=='backup' and j['conclusion']=='success')
 log=get(f"repos/{REPO}/actions/jobs/{job['id']}/logs",True).decode(errors='replace')
 m=re.search(r'schema (\d+), (\d+) usuário\(s\), (\d+) alvo\(s\)',log)
 if not m:raise RuntimeError('snapshot_metadata_missing')
 cipherpath=work/'stored.age';cipherpath.write_bytes(cipher);cipherpath.chmod(0o600)
 identity=work/'identity';identity.write_text(IDENTITY+'\n');identity.chmod(0o600);IDENTITY=''
 plain=run(['age','-d','-i',str(identity),str(cipherpath)]);identity.unlink()
 if not plain.startswith(b'PGDMP'):raise RuntimeError('invalid_dump')
 run(['docker','pull',IMAGE],timeout=180)
 run(['docker','run','-d','--name',name,'--label','io.stillon.audit=github-stored-restore','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--user','70:70','--memory','768m','--cpus','1','--pids-limit','128','--tmpfs','/var/lib/postgresql:rw,size=256m,uid=70,gid=70','--tmpfs','/var/run/postgresql:rw,size=16m,uid=70,gid=70','--tmpfs','/tmp:rw,size=16m,uid=70,gid=70','-e','POSTGRES_HOST_AUTH_METHOD=trust',IMAGE,'-c','log_error_verbosity=terse','-c','log_min_error_statement=panic']);created=True
 for _ in range(60):
  p=subprocess.run(['docker','exec',name,'pg_isready','-h','127.0.0.1','-U','postgres'],capture_output=True)
  if p.returncode==0:break
  time.sleep(1)
 else:raise RuntimeError('isolated_database_not_ready')
 run(['docker','exec','-i',name,'psql','-X','-U','postgres','-d','postgres','-v','ON_ERROR_STOP=1'],b'CREATE ROLE stillon_app LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE; CREATE DATABASE stillon OWNER stillon_app;')
 run(['docker','exec','-i',name,'pg_restore','--exit-on-error','-U','postgres','-d','stillon'],plain);plain=b''
 sql=pathlib.Path('deploy/backup/verificar.sql').read_bytes()
 run(['docker','exec','-i',name,'psql','-X','-U','postgres','-d','stillon','-v','ON_ERROR_STOP=1','-v','versao='+m[1],'-v','usuarios='+m[2],'-v','alvos='+m[3]],sql)
 print(json.dumps({'stored_restore_verified':True,'artifact_id':a['id'],'backup_run':rid,'backup_age_hours':round(age/3600,2),'snapshot_schema':int(m[1]),'minimum_users':int(m[2]),'minimum_targets':int(m[3]),'isolation':'GitHub-hosted runner, PostgreSQL network none, no production mounts','checked_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat()}))
finally:
 if created:
  label=run(['docker','inspect',name,'--format','{{index .Config.Labels "io.stillon.audit"}}']).decode().strip()
  if label!='github-stored-restore':raise RuntimeError('cleanup_label_mismatch')
  run(['docker','rm','-f','-v',name])
 shutil.rmtree(work)
