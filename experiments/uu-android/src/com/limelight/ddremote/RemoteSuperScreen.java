package com.limelight.ddremote;

import android.os.Handler;
import android.os.Looper;
import org.json.JSONObject;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

/** The lease spans preparation and the stream; a failed teardown never starts a second screen. */
final class RemoteSuperScreen {
 interface Ready { void complete(String error); }
 private final Runnable revoked;
 private final long token;private final String ip;private final Handler ui=new Handler(Looper.getMainLooper());
 private final ScheduledExecutorService worker=Executors.newSingleThreadScheduledExecutor();
 private final AtomicBoolean closed=new AtomicBoolean();private String lease="";private Future<?> cleanup;
 RemoteSuperScreen(long token,String ip,Runnable revoked){this.token=token;this.ip=ip;this.revoked=revoked;}
 private JSONObject request(JSONObject body)throws Exception{return EmbeddedNetwork.request(token,ip,"/v1/super-screen",body);}
 synchronized void prepare(int width,int height,int fps,Ready ready){
  if(closed.get())return;
  worker.execute(()->{try{
   if(ip==null||ip.isEmpty())throw new IllegalStateException("此连接没有账号设备地址");
   JSONObject state=request(null);
   if(!state.optBoolean("available"))throw new IllegalStateException(state.optString("reason","电脑端未准备好超级屏"));
   JSONObject response=request(new JSONObject().put("action","enable").put("width",width).put("height",height).put("fps",fps));
   lease=response.getString("lease");
   synchronized(this){if(!closed.get())worker.scheduleWithFixedDelay(this::renew,4,4,TimeUnit.SECONDS);}
   ui.post(()->{if(!closed.get())ready.complete(null);});
  }catch(Exception ex){ui.post(()->{if(!closed.get())ready.complete(UuAdapterHome.friendly(ex));});}});
 }
 private void renew(){if(closed.get()||lease.isEmpty())return;try{request(new JSONObject().put("action","renew").put("lease",lease));}catch(Exception ex){close();ui.post(revoked);}}
 synchronized Future<?> close(){
  if(closed.compareAndSet(false,true)){
   cleanup=worker.submit(()->{String current=lease;lease="";if(!current.isEmpty())try{request(new JSONObject().put("action","disable").put("lease",current));}catch(Exception ex){throw new IllegalStateException("未确认电脑显示布局恢复，请检查电脑或按 Ctrl+Alt+Shift+F11",ex);}});
   worker.shutdown();
  }
  return cleanup;
 }
}
