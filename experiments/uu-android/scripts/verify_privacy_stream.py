"""Synthetic privacy capture test; no user desktop images/text are saved."""
import subprocess,os,json,time,threading,queue,argparse,concurrent.futures
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--exe',required=True);p.add_argument('--sunshine',required=True);p.add_argument('--stage',required=True);p.add_argument('--report',required=True);a=p.parse_args()
os.environ['PYTHONIOENCODING']='utf-8';os.sys.stdout.reconfigure(encoding='utf-8',errors='replace')
root=Path(__file__).resolve().parents[3];stage=Path(a.stage).resolve()
back=mask=test=fixture=None;report={'samples':{},'physicalPanelBlackVerified':False};messages=queue.Queue()
def consume(proc,q):
 for line in proc.stdout:q.put(line.strip())
 q.put(None)
def first_line(proc,seconds=8):
 q=queue.Queue();threading.Thread(target=lambda:q.put(proc.stdout.readline().strip()),daemon=True).start()
 return q.get(timeout=seconds)
def colors(value):return [[int(c) for c in point.split(',')]for point in value.split(';')]
expected=[[255,0,0],[0,255,0],[0,0,255],[255,255,255]]
def matches(value):return all(sum(abs(a-b)for a,b in zip(x,y))<100 for x,y in zip(colors(value),expected))
try:
 fixture=subprocess.Popen([os.sys.executable,str(Path(__file__).with_name('stream_fixture.py')),'--exe',a.sunshine,'--stage',str(stage/'sunshine-fixture'),'--seconds','125','--stop-on-stdin'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,encoding='utf-8',creationflags=0x08000000)
 fixture_lines=queue.Queue();threading.Thread(target=consume,args=(fixture,fixture_lines),daemon=True).start()
 deadline=time.monotonic()+60
 while time.monotonic()<deadline:
  line=fixture_lines.get(timeout=60)
  if line is None:raise RuntimeError('Sunshine exited before readiness')
  if line=='Fixture API ready':break
 else:raise TimeoutError('Sunshine readiness')
 back=subprocess.Popen([str(stage/'PrivacyBackdropV2.exe')],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,encoding='utf-8',creationflags=0x08000000)
 report['backdropReady']=first_line(back);assert 'visible=True' in report['backdropReady'],report['backdropReady']
 test=subprocess.Popen([str(root/'.tools/android/sdk/platform-tools/adb.exe'),'-s','emulator-5580','shell','am','instrument','-w','-e','mode','privacy','io.ddremote.uuadapter/com.limelight.ddremote.StreamInstrumentation'],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,encoding='utf-8',errors='replace',creationflags=0x08000000)
 threading.Thread(target=consume,args=(test,messages),daemon=True).start();deadline=time.monotonic()+65;sample={};raw=[]
 while time.monotonic()<deadline:
  line=messages.get(timeout=50)
  if line is None:break
  raw.append(line)
  if line.startswith('INSTRUMENTATION_STATUS: '):
   key,sep,value=line[len('INSTRUMENTATION_STATUS: '):].partition('=');sample[key]=value
  if line.startswith('INSTRUMENTATION_STATUS_CODE:') and 'sampleStage' in sample:
   label=sample['sampleStage'];report['samples'][label]=sample.get('colors');sample={};print('Sample '+label+' '+report['samples'][label],flush=True)
   if label=='baseline':
    if not matches(report['samples'][label]):raise RuntimeError('Synthetic quadrants not visible; no privacy claim')
    mask=subprocess.Popen([a.exe,'--privacy-helper',str(os.getpid())],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,encoding='utf-8',creationflags=0x08000000)
    report['maskReady']=first_line(mask);assert report['maskReady']=='READY',report['maskReady'];print('Mask ready pid='+str(mask.pid),flush=True)
   elif label=='masked':mask.stdin.close();mask.wait(timeout=3);report['maskClosed']=True
 report['testOutput']=raw
 if len(report['samples'])==3:report['streamCaptureExclusion']=all(matches(value)for value in report['samples'].values())
 if not report.get('streamCaptureExclusion'):raise RuntimeError('Remote synthetic image did not survive mask')
except Exception as e:report['error']=str(e)
finally:
 for child in (mask,back,test,fixture):
  if child is not None and child.poll() is None:
   if child.stdin and not child.stdin.closed:child.stdin.close()
   try:child.wait(timeout=12 if child is fixture else 3)
   except subprocess.TimeoutExpired:child.kill();child.wait()
 Path(a.report).write_text(json.dumps(report,indent=2,ensure_ascii=False),encoding='utf-8');print(json.dumps(report,ensure_ascii=False),flush=True)
