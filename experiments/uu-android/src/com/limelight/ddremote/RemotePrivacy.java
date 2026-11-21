package com.limelight.ddremote;

import android.app.Activity;
import android.app.AlertDialog;
import android.os.Handler;
import android.os.Looper;
import org.json.JSONObject;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

/** A short-lived, peer-owned Windows mask; heartbeats never recreate a revoked mask. */
final class RemotePrivacy implements AutoCloseable {
 interface Notice{void show(String text);}
 private final Activity activity;private final long token;private final String ip;private final Notice notice;
 private final Handler ui=new Handler(Looper.getMainLooper());
 private final ScheduledExecutorService worker=Executors.newSingleThreadScheduledExecutor();
 private final AtomicBoolean closed=new AtomicBoolean();private volatile String lease="";private volatile boolean busy;
 RemotePrivacy(Activity activity,long token,String ip,Notice notice){this.activity=activity;this.token=token;this.ip=ip;this.notice=notice;worker.scheduleWithFixedDelay(this::renew,4,4,TimeUnit.SECONDS);}
 boolean active(){return !lease.isEmpty();}
 private JSONObject request(String action,String value)throws Exception{return EmbeddedNetwork.request(token,ip,"/v1/privacy",new JSONObject().put("action",action).put("lease",value));}
 private void message(String text){ui.post(()->{if(!closed.get())notice.show(text);});}
 void toggle(){
  if(closed.get()||busy)return;
  if(ip==null||ip.isEmpty()){message("该连接没有账号设备地址，无法启用隐私遮罩");return;}
  if(active()){disable();return;}
  new AlertDialog.Builder(activity).setTitle("隐私遮罩 · 试验功能").setMessage("电脑所有屏幕将显示黑色遮罩，远程画面应保持可见。若远程画面也变黑，请立即关闭。\n\n电脑可按 Ctrl+Alt+Shift+F12 解除；断线后最多20秒自动恢复。此功能不等于关闭物理显示器，也不能遮挡系统安全桌面。").setPositiveButton("开启",(d,w)->enable()).setNegativeButton("取消",null).show();
 }
 private synchronized void enable(){if(closed.get()||busy)return;busy=true;worker.execute(()->{try{
  JSONObject result=request("enable","");String granted=result.getString("lease");
  if(closed.get()){try{request("disable",granted);}catch(Exception ignored){}return;}
  lease=granted;message("隐私遮罩已开启；电脑快捷键 Ctrl+Alt+Shift+F12 可解除");
 }catch(Exception ex){message("隐私遮罩开启失败，请确认电脑端已更新且未锁屏");}finally{busy=false;}});}
 private synchronized void disable(){if(closed.get()||busy)return;busy=true;worker.execute(()->{String previous=lease;try{request("disable",previous);lease="";message("隐私遮罩已关闭");}catch(Exception ex){lease="";message("未收到关闭确认，已停止续期；电脑将在20秒内恢复");}finally{busy=false;}});}
 private void renew(){String current=lease;if(current.isEmpty()||closed.get())return;try{request("renew",current);}catch(Exception ex){lease="";message("隐私遮罩连接中断或已在电脑解除；停止续期");}}
 @Override public synchronized void close(){if(!closed.compareAndSet(false,true))return;worker.execute(()->{String previous=lease;lease="";if(!previous.isEmpty())try{request("disable",previous);}catch(Exception ignored){}});worker.shutdown();}
}
