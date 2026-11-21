"""Coordinate isolated Sunshine readiness and one bounded emulator regression."""
import argparse,subprocess,time,threading,queue,os
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--sunshine',required=True);p.add_argument('--stage',required=True);p.add_argument('--report',required=True);p.add_argument('--mode',default='stream');p.add_argument('--backdrop');a=p.parse_args()
os.environ["PYTHONIOENCODING"]="utf-8"
os.sys.stdout.reconfigure(encoding="utf-8",errors="replace")
root=Path(__file__).resolve().parents[3];lines=queue.Queue()
fixture=subprocess.Popen([os.sys.executable,str(Path(__file__).with_name('stream_fixture.py')),'--exe',a.sunshine,'--stage',a.stage,'--seconds','155','--stop-on-stdin'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,encoding='utf-8',errors='replace',creationflags=0x08000000)
def read():
 for line in fixture.stdout:lines.put(line.strip())
 lines.put(None)
threading.Thread(target=read,daemon=True).start()
backdrop=None
try:
 deadline=time.monotonic()+65
 while time.monotonic()<deadline:
  line=lines.get(timeout=65)
  if line is None:raise RuntimeError('Fixture exited before readiness')
  print(line,flush=True)
  if line=='Fixture API ready':break
 else:raise TimeoutError('Fixture readiness')
 if a.backdrop:
  backdrop=subprocess.Popen([a.backdrop,'--animate'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,creationflags=0x08000000)
  ready=backdrop.stdout.readline().decode('utf-8','replace');print(ready.strip(),flush=True)
  if 'visible=True' not in ready:raise RuntimeError('Animated test backdrop invisible')
 test=subprocess.Popen([str(root/'.tools/android/sdk/platform-tools/adb.exe'),'-s','emulator-5580','shell','am','instrument','-w','-e','mode',a.mode,'io.ddremote.uuadapter/com.limelight.ddremote.StreamInstrumentation'],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,encoding='utf-8',errors='replace',creationflags=0x08000000)
 timer=threading.Timer(85,test.kill);timer.start();output=[]
 try:
  for line in test.stdout:output.append(line);print(line.rstrip(),flush=True)
  test.wait(timeout=5)
 finally:timer.cancel()
 test_output=''.join(output);Path(a.report).write_text(test_output,encoding='utf-8')
 if 'INSTRUMENTATION_CODE: -1' not in test_output:raise RuntimeError('Stream regression failed; see report')
finally:
 if backdrop and backdrop.poll() is None:
  backdrop.stdin.close();backdrop.wait(timeout=5)
 # Let bounded fixture remove its owned sockets/process, never kill a user host.
 if fixture.stdin:fixture.stdin.close()
 try:fixture.wait(timeout=45)
 except subprocess.TimeoutExpired:raise RuntimeError('Fixture still running; inspect owned PID before cleanup')
