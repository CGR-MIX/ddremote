"""Isolated Sunshine instance, local-only, separate ports; closes only its own child."""
import atexit,argparse,base64,hashlib,json,os,secrets,ssl,subprocess,time,urllib.request,socket,socketserver,threading
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--exe',required=True);p.add_argument('--stage',required=True);p.add_argument('--seconds',type=int,default=240);p.add_argument('--stop-on-stdin',action='store_true');a=p.parse_args()
stage=Path(a.stage).resolve();stage.mkdir(parents=True,exist_ok=True)
user='dd-fixture';password=secrets.token_hex(24);salt=secrets.token_hex(8)
(stage/'credentials.json').write_text(json.dumps({'username':user,'salt':salt,'password':hashlib.sha256((password+salt).encode()).digest()[::-1].hex().upper()}))
(stage/'apps.json').write_text(json.dumps({'env':{},'apps':[{'name':'Desktop','image-path':'desktop.png'}]}))
config={'sunshine_name':'DD UU isolated stream fixture','port':48989,'file_state':stage/'state.json','credentials_file':stage/'credentials.json','file_apps':stage/'apps.json','pkey':stage/'key.pem','cert':stage/'cert.pem','log_path':stage/'sunshine.log','origin_web_ui_allowed':'pc','upnp':'disabled','address_family':'ipv4','bind_address':'127.0.0.2','min_log_level':int(os.environ.get('DD_FIXTURE_LOG_LEVEL','2')),'system_tray':'disabled','ping_timeout':10000}
(stage/'sunshine.conf').write_text(''.join(f'{k} = {str(v).replace(chr(92),"/")}\n' for k,v in config.items()))
log=(stage/'process.log').open('w')
child=subprocess.Popen([a.exe,str(stage/'sunshine.conf')],cwd=str(Path(a.exe).parent),stdout=log,stderr=log,creationflags=0x08000000)
atexit.register(lambda: child.terminate() if child.poll() is None else None)
(stage/'process.json').write_text(json.dumps({'pid':child.pid,'exe':a.exe,'port':48989}))
class Proxy(socketserver.BaseRequestHandler):
    def handle(self):
        try:
            upstream=socket.create_connection(('127.0.0.2',self.server.server_address[1]),10)
            def upload():
                try:
                    while True:
                        data=self.request.recv(65536)
                        if not data:break
                        upstream.sendall(data)
                except OSError:pass
            upload_thread=threading.Thread(target=upload,daemon=True);upload_thread.start()
            response=bytearray()
            while True:
                data=upstream.recv(65536)
                if not data:break
                response.extend(data)
                if self.server.server_address[1]!=49010:
                    self.request.setsockopt(socket.IPPROTO_TCP,socket.TCP_NODELAY,1)
                    for offset in range(0,len(data),1024):self.request.sendall(data[offset:offset+1024]);time.sleep(.001)
            if self.server.server_address[1]==49010:
                # One RTSP transaction per connection. Forward the complete sealed
                # message then half-close, avoiding emulator FIN truncation.
                if len(response)>=24:
                    declared=int.from_bytes(response[:4],'big') & 0x7fffffff
                    print(f'RTSP fixture sealed bytes={len(response)} declared={declared+24}',flush=True)
                self.request.setsockopt(socket.IPPROTO_TCP,socket.TCP_NODELAY,1)
                for offset in range(0,len(response),1024):
                    self.request.sendall(response[offset:offset+1024]);time.sleep(.002)
                self.request.shutdown(socket.SHUT_WR)
                upload_thread.join(timeout=3)
            else:
                self.request.shutdown(socket.SHUT_WR);upload_thread.join(timeout=3)
            if self.server.server_address[1]==48989 and b'\r\n\r\n' in response:
                head,body=response.split(b'\r\n\r\n',1)
                lengths=[line.decode('ascii','replace') for line in head.split(b'\r\n') if line.lower().startswith(b'content-length:')]
                print('HTTP fixture response '+str(lengths)+' actual='+str(len(body)),flush=True)
            upstream.close()
        except OSError:pass
class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address=True
    daemon_threads=True
proxies=[]
for port in (48989,48984,49010):
    proxy=Server(('127.0.0.1',port),Proxy);proxies.append(proxy)
    threading.Thread(target=proxy.serve_forever,daemon=True).start()
udps=[]
udp_stats={}
for port in (48998,48999,49000,49002):
    listener=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);listener.setsockopt(socket.SOL_SOCKET,socket.SO_RCVBUF,4*1024*1024);listener.setsockopt(socket.SOL_SOCKET,socket.SO_SNDBUF,4*1024*1024);listener.bind(('127.0.0.1',port));udps.append(listener)
    forward=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);forward.setsockopt(socket.SOL_SOCKET,socket.SO_RCVBUF,4*1024*1024);forward.setsockopt(socket.SOL_SOCKET,socket.SO_SNDBUF,4*1024*1024);forward.connect(('127.0.0.2',port));udps.append(forward)
    peer=[None];counts={'toHost':0,'toClient':0,'errors':[]};udp_stats[port]=counts
    def receive(listener=listener,forward=forward,peer=peer,counts=counts):
        try:
            while True:
                data,addr=listener.recvfrom(65536);peer[0]=addr;forward.send(data);counts['toHost']+=1
        except OSError as ex:
            if ex.winerror!=10038:counts['errors'].append('toHost: '+str(ex))
    def reply(listener=listener,forward=forward,peer=peer,counts=counts):
        while True:
            try:
                data=forward.recv(65536)
                if peer[0]:listener.sendto(data,peer[0]);counts['toClient']+=1
            except ConnectionResetError:
                # Early audio pings precede Sunshine binding its audio socket.
                # Winsock reports the ICMP response on recv; keep the relay alive.
                counts['earlyResetRetried']=counts.get('earlyResetRetried',0)+1
                continue
            except OSError as ex:
                if ex.winerror!=10038:counts['errors'].append('toClient: '+str(ex))
                break
    threading.Thread(target=receive,daemon=True).start();threading.Thread(target=reply,daemon=True).start()
print('Started isolated Sunshine fixture pid='+str(child.pid),flush=True)
stop=threading.Event()
if a.stop_on_stdin:threading.Thread(target=lambda:(os.sys.stdin.readline(),stop.set()),daemon=True).start()
deadline=time.monotonic()+a.seconds;approved=set();ready=False;ctx=None
try:
    while time.monotonic()<deadline and child.poll() is None and not stop.is_set():
        try:
            if ctx is None:
                ctx=ssl.create_default_context(cafile=str(stage/'cert.pem'));ctx.check_hostname=False
            def request(method,body=None):
                req=urllib.request.Request('https://127.0.0.2:48990/api/pin',data=json.dumps(body).encode() if body is not None else None,method=method,headers={'Authorization':'Basic '+base64.b64encode((user+':'+password).encode()).decode(),'Content-Type':'application/json'})
                with urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ctx)).open(req,timeout=35) as response:return json.load(response)
            result=request('GET')
            if not ready:print('Fixture API ready',flush=True);ready=True
            for item in result.get('pairings',[]):
                if item['id'] not in approved:
                    approved.add(item['id'])
                    answer=request('POST',{'pairing_id':item['id'],'pin':'4186','name':'DD UU emulator fixture'})
                    print('Fixture pair approved='+str(answer.get('status')),flush=True)
        except Exception as ex:
            if ready:print('Fixture request '+type(ex).__name__,flush=True)
        time.sleep(.4)
finally:
    for proxy in proxies:proxy.shutdown();proxy.server_close()
    for udp in udps:udp.close()
    child.terminate()
    try:child.wait(timeout=8)
    except subprocess.TimeoutExpired:child.kill();child.wait()
    log.close();(stage/'udp-stats.json').write_text(json.dumps(udp_stats,indent=2));print('Isolated Sunshine fixture stopped',flush=True)
