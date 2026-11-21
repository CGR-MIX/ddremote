"""Play a generated quiet tone into the isolated host and verify decoded audio counters."""
import subprocess,os,json,time,threading,queue,argparse,wave,math,struct,winsound,ctypes
from ctypes import wintypes
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--sunshine',required=True);p.add_argument('--stage',required=True);p.add_argument('--report',required=True);a=p.parse_args()
os.environ['PYTHONIOENCODING']='utf-8';os.sys.stdout.reconfigure(encoding='utf-8',errors='replace')
root=Path(__file__).resolve().parents[3];stage=Path(a.stage).resolve();fixture=test=None;report={};messages=queue.Queue();wave_handle=None;wave_header=None;wave_data=None
def read(proc,q):
 for line in proc.stdout:q.put(line.strip())
 q.put(None)
class WaveCaps(ctypes.Structure):
 _fields_=[('mid',wintypes.WORD),('pid',wintypes.WORD),('version',wintypes.DWORD),('name',wintypes.WCHAR*32),('formats',wintypes.DWORD),('channels',wintypes.WORD),('reserved',wintypes.WORD),('support',wintypes.DWORD)]
class WaveFormat(ctypes.Structure):
 _fields_=[('tag',wintypes.WORD),('channels',wintypes.WORD),('samplesPerSec',wintypes.DWORD),('bytesPerSec',wintypes.DWORD),('align',wintypes.WORD),('bits',wintypes.WORD),('extra',wintypes.WORD)]
class WaveHeader(ctypes.Structure):
 _fields_=[('data',ctypes.c_void_p),('length',wintypes.DWORD),('recorded',wintypes.DWORD),('user',ctypes.c_size_t),('flags',wintypes.DWORD),('loops',wintypes.DWORD),('next',ctypes.c_void_p),('reserved',ctypes.c_size_t)]
winmm=ctypes.WinDLL('winmm')
def start_tone():
 global wave_handle,wave_header,wave_data
 target=None
 for i in range(winmm.waveOutGetNumDevs()):
  caps=WaveCaps();err=winmm.waveOutGetDevCapsW(i,ctypes.byref(caps),ctypes.sizeof(caps))
  if err==0 and 'Steam Streaming Speakers' in caps.name:target=i
 if target is None:raise RuntimeError('Sunshine virtual audio output absent')
 fmt=WaveFormat(1,2,48000,192000,4,16,0);wave_handle=ctypes.c_void_p()
 err=winmm.waveOutOpen(ctypes.byref(wave_handle),target,ctypes.byref(fmt),0,0,0)
 if err:raise RuntimeError('waveOutOpen '+str(err))
 pcm=b''.join(struct.pack('<hh',v,v)for v in (int(900*math.sin(2*math.pi*440*n/48000))for n in range(48000*2)))
 wave_data=ctypes.create_string_buffer(pcm);wave_header=WaveHeader(ctypes.cast(wave_data,ctypes.c_void_p),len(pcm),0,0,4|8,12,None,0)
 err=winmm.waveOutPrepareHeader(wave_handle,ctypes.byref(wave_header),ctypes.sizeof(wave_header))
 if err:raise RuntimeError('waveOutPrepareHeader '+str(err))
 err=winmm.waveOutWrite(wave_handle,ctypes.byref(wave_header),ctypes.sizeof(wave_header))
 if err:raise RuntimeError('waveOutWrite '+str(err))
 report['toneDestination']='Steam Streaming Speakers (existing Sunshine capture sink)'
try:
 tone=stage/'synthetic-440hz.wav'
 with wave.open(str(tone),'wb') as w:
  w.setnchannels(2);w.setsampwidth(2);w.setframerate(48000)
  w.writeframes(b''.join(struct.pack('<hh',v,v)for v in (int(900*math.sin(2*math.pi*440*n/48000))for n in range(48000*2))))
 fixture=subprocess.Popen([os.sys.executable,str(Path(__file__).with_name('stream_fixture.py')),'--exe',a.sunshine,'--stage',str(stage/'sunshine-fixture'),'--seconds','130','--stop-on-stdin'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,encoding='utf-8',creationflags=0x08000000)
 threading.Thread(target=read,args=(fixture,messages),daemon=True).start();deadline=time.monotonic()+65
 while time.monotonic()<deadline:
  line=messages.get(timeout=60)
  if line is None:raise RuntimeError('Fixture exited before readiness')
  if line=='Fixture API ready':break
 else:raise TimeoutError('Fixture readiness')
 q=queue.Queue();test=subprocess.Popen([str(root/'.tools/android/sdk/platform-tools/adb.exe'),'-s','emulator-5580','shell','am','instrument','-w','-e','mode','audio','io.ddremote.uuadapter/com.limelight.ddremote.StreamInstrumentation'],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,encoding='utf-8',errors='replace',creationflags=0x08000000)
 threading.Thread(target=read,args=(test,q),daemon=True).start();deadline=time.monotonic()+65;raw=[]
 while time.monotonic()<deadline:
  line=q.get(timeout=50)
  if line is None:break
  raw.append(line)
  if 'audioStage=toneReady' in line:
   start_tone();report['syntheticToneStarted']=True
 report['instrumentation']=raw;report['decodedAudioAndMutePassed']='INSTRUMENTATION_CODE: -1' in raw;report['physicalPhoneSpeakerVerified']=False
except Exception as ex:report['error']=str(ex)
finally:
 if wave_handle:
  winmm.waveOutReset(wave_handle)
  if wave_header:winmm.waveOutUnprepareHeader(wave_handle,ctypes.byref(wave_header),ctypes.sizeof(wave_header))
  winmm.waveOutClose(wave_handle)
 for child in (test,fixture):
  if child and child.poll() is None:
   if child.stdin:child.stdin.close()
   try:child.wait(timeout=12)
   except subprocess.TimeoutExpired:child.kill();child.wait()
 stats=stage/'sunshine-fixture'/'udp-stats.json'
 if stats.exists():report['udpRelay']=json.loads(stats.read_text(encoding='utf-8'))
 Path(a.report).write_text(json.dumps(report,indent=2,ensure_ascii=False),encoding='utf-8');print(json.dumps(report,ensure_ascii=False),flush=True)
