"""Run bounded process-lifecycle checks against the actual privacy mask helper."""
import argparse,subprocess,os,time,json,concurrent.futures
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--exe',required=True);p.add_argument('--report',required=True);a=p.parse_args()
children=[];results={}
def start(parent):
 child=subprocess.Popen([a.exe,'--privacy-helper',str(parent)],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,creationflags=0x08000000);children.append(child)
 with concurrent.futures.ThreadPoolExecutor(1) as pool:
  pending=pool.submit(child.stdout.readline)
  try:line=pending.result(timeout=6).decode(errors='replace').strip()
  except concurrent.futures.TimeoutError:child.kill();raise
 if line!='READY':raise RuntimeError(line or 'helper exited')
 return child
try:
 child=start(os.getpid());began=time.monotonic();child.stdin.close();child.wait(timeout=3);results['stdinEOFSeconds']=round(time.monotonic()-began,3)
 child=start(os.getpid());began=time.monotonic();child.wait(timeout=23);results['missingHeartbeatSeconds']=round(time.monotonic()-began,3)
 parent=subprocess.Popen(['cmd.exe','/c','pause'],stdin=subprocess.PIPE,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,creationflags=0x08000000);children.append(parent)
 child=start(parent.pid);began=time.monotonic();parent.terminate();parent.wait(timeout=3);child.wait(timeout=3);results['parentExitSeconds']=round(time.monotonic()-began,3)
 results['helperLifecycle']='passed';results['visualPrivacyProven']=False
except Exception as ex:results['error']=str(ex)
finally:
 for child in children:
  if child.poll() is None:
   if child.stdin and not child.stdin.closed:child.stdin.close()
   try:child.wait(timeout=3)
   except subprocess.TimeoutExpired:child.kill();child.wait()
 Path(a.report).write_text(json.dumps(results,indent=2),encoding='utf-8');print(json.dumps(results))
