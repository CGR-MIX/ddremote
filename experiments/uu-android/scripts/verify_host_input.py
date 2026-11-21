"""Actual UU IME/gestures -> Sunshine -> synthetic Windows control receipts."""
import subprocess,os,json,time,threading,queue,argparse
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--sunshine',required=True);p.add_argument('--stage',required=True);p.add_argument('--report',required=True);a=p.parse_args()
os.environ['PYTHONIOENCODING']='utf-8';os.sys.stdout.reconfigure(encoding='utf-8',errors='replace')
root=Path(__file__).resolve().parents[3];stage=Path(a.stage).resolve();fixture=target=None;report={};messages=queue.Queue()
def read(proc):
 for line in proc.stdout:messages.put(line.strip())
 messages.put(None)
try:
 fixture=subprocess.Popen([os.sys.executable,str(Path(__file__).with_name('stream_fixture.py')),'--exe',a.sunshine,'--stage',str(stage/'sunshine-fixture'),'--seconds','135','--stop-on-stdin'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,encoding='utf-8',creationflags=0x08000000)
 threading.Thread(target=read,args=(fixture,),daemon=True).start();deadline=time.monotonic()+65
 while time.monotonic()<deadline:
  line=messages.get(timeout=60)
  if line is None:raise RuntimeError('Fixture exited before readiness')
  if line=='Fixture API ready':break
 else:raise TimeoutError('Fixture readiness')
 host_report=stage/'host-input-result.json'
 target=subprocess.Popen([str(stage/'DDInputTargetV3.exe'),str(host_report)],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,encoding='utf-8',creationflags=0x08000000)
 q=queue.Queue();threading.Thread(target=lambda:q.put(target.stdout.readline().strip()),daemon=True).start();report['targetReady']=q.get(timeout=8)
 if 'visible=True' not in report['targetReady']:raise RuntimeError('Test target hidden')
 print('Synthetic input target ready; awaiting foreground verification',flush=True)
 deadline=time.monotonic()+40
 while time.monotonic()<deadline:
  try:
   if json.loads(host_report.read_text(encoding='utf-8')).get('foreground'):break
  except (ValueError,OSError):pass
  time.sleep(.2)
 else:raise RuntimeError('Input target not foreground; no remote input dispatched')
 test=subprocess.run([str(root/'.tools/android/sdk/platform-tools/adb.exe'),'-s','emulator-5580','shell','am','instrument','-w','-e','mode','hostinput','io.ddremote.uuadapter/com.limelight.ddremote.StreamInstrumentation'],capture_output=True,text=True,encoding='utf-8',errors='replace',timeout=70,creationflags=0x08000000)
 report['instrumentation']=test.stdout;report['hostReceipts']=json.loads(host_report.read_text(encoding='utf-8'))
 r=report['hostReceipts'];report['passed']=('INSTRUMENTATION_CODE: -1' in test.stdout and all(r.get(k) for k in ('firstChineseEmoji','shortcutReplace','dragged')) and all(r.get(k,0)>0 for k in ('leftClicks','rightClicks','wheelEvents')))
except Exception as ex:report['error']=str(ex)
finally:
 for child in (target,fixture):
  if child and child.poll() is None:
   child.stdin.close()
   try:child.wait(timeout=12)
   except subprocess.TimeoutExpired:child.kill();child.wait()
 Path(a.report).write_text(json.dumps(report,indent=2,ensure_ascii=False),encoding='utf-8');print(json.dumps(report,ensure_ascii=False),flush=True)
