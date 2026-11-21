package com.limelight.ddremote;
import android.app.Instrumentation;
import android.app.Activity;
import android.content.Intent;
import android.os.Bundle;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputConnection;
import com.limelight.nvstream.http.*;
import java.lang.reflect.Field;
import java.security.cert.X509Certificate;

/** Local emulator fixture only; never included in the delivered APK. */
public final class StreamInstrumentation extends Instrumentation {
    private Bundle args;
    public void onCreate(Bundle b){args=b;start();}
    private static Object field(Object owner,String name)throws Exception{Field f=owner.getClass().getDeclaredField(name);f.setAccessible(true);return f.get(owner);}
    private long awaitFrames(UuStreamActivity screen) throws Exception {
        long deadline=System.currentTimeMillis()+45000;
        while(System.currentTimeMillis()<deadline){
            Thread.sleep(250);SunshineVideoRenderer video=(SunshineVideoRenderer)field(screen,"video");
            if(video!=null&&video.rendered.get()>=90)return video.rendered.get();
        }
        throw new AssertionError("No rendered stream: "+((android.widget.TextView)field(screen,"status")).getText());
    }
    private void assertReleased(Activity screen) throws Exception {
        long stopBy=System.currentTimeMillis()+8000;
        Object conn=field(screen,"conn");if(conn==null)return;Field owned=conn.getClass().getDeclaredField("bridgeOwned");owned.setAccessible(true);
        while(owned.getBoolean(conn)&&System.currentTimeMillis()<stopBy)Thread.sleep(100);
        if(owned.getBoolean(conn))throw new AssertionError("Native connection not released");
        Field sem=conn.getClass().getDeclaredField("connectionAllowed");sem.setAccessible(true);
        if(((java.util.concurrent.Semaphore)sem.get(null)).availablePermits()!=1)throw new AssertionError("Protocol semaphore leaked");
    }
    private String sampleSurface(UuStreamActivity screen)throws Exception{
        android.graphics.Bitmap bitmap=android.graphics.Bitmap.createBitmap(8,8,android.graphics.Bitmap.Config.ARGB_8888);
        java.util.concurrent.CountDownLatch ready=new java.util.concurrent.CountDownLatch(1);int[] status={-1};
        android.view.SurfaceView surface=(android.view.SurfaceView)field(screen,"surface");
        android.view.PixelCopy.request(surface,bitmap,code->{status[0]=code;ready.countDown();},new android.os.Handler(android.os.Looper.getMainLooper()));
        if(!ready.await(3,java.util.concurrent.TimeUnit.SECONDS)||status[0]!=0)throw new AssertionError("PixelCopy failed "+status[0]);
        StringBuilder colors=new StringBuilder();for(int[] xy:new int[][]{{1,1},{6,1},{1,6},{6,6}}){int c=bitmap.getPixel(xy[0],xy[1]);if(colors.length()>0)colors.append(";");colors.append(android.graphics.Color.red(c)).append(",").append(android.graphics.Color.green(c)).append(",").append(android.graphics.Color.blue(c));}bitmap.recycle();return colors.toString();
    }
    private void touch(UuStreamActivity screen,int action,float x,float y)throws Exception {
        final Throwable[] failure={null};runOnMainSync(()->{try{
            android.view.View view=(android.view.View)field(screen,"surface");
            long now=android.os.SystemClock.uptimeMillis();android.view.MotionEvent e=android.view.MotionEvent.obtain(now,now,action,x*view.getWidth(),y*view.getHeight(),0);
            ((TouchRouter)field(screen,"touch")).onTouch(view,e);e.recycle();
        }catch(Throwable ex){failure[0]=ex;}});if(failure[0]!=null)throw new AssertionError(failure[0]);
    }
    private void twoFinger(UuStreamActivity screen,int action,float y)throws Exception {
        final Throwable[] failure={null};runOnMainSync(()->{try{
            android.view.View view=(android.view.View)field(screen,"surface");
            android.view.MotionEvent.PointerProperties[] props=new android.view.MotionEvent.PointerProperties[2];
            android.view.MotionEvent.PointerCoords[] coords=new android.view.MotionEvent.PointerCoords[2];
            for(int i=0;i<2;i++){props[i]=new android.view.MotionEvent.PointerProperties();props[i].id=i;props[i].toolType=1;coords[i]=new android.view.MotionEvent.PointerCoords();coords[i].x=(.45f+i*.1f)*view.getWidth();coords[i].y=y*view.getHeight();coords[i].pressure=1;coords[i].size=1;}
            long now=android.os.SystemClock.uptimeMillis();android.view.MotionEvent e=android.view.MotionEvent.obtain(now,now,action,2,props,coords,0,0,1,1,0,0,android.view.InputDevice.SOURCE_TOUCHSCREEN,0);
            ((TouchRouter)field(screen,"touch")).onTouch(view,e);e.recycle();
        }catch(Throwable ex){failure[0]=ex;}});if(failure[0]!=null)throw new AssertionError(failure[0]);
    }
    private void verifyMobileUI(UuStreamActivity screen,Bundle result)throws Exception {
        android.view.View view=(android.view.View)field(screen,"surface");int originalHeight=view.getHeight();
        runOnMainSync(()->{try{java.lang.reflect.Method menu=screen.getClass().getDeclaredMethod("showMenu");menu.setAccessible(true);menu.invoke(screen);java.lang.reflect.Method keys=screen.getClass().getDeclaredMethod("showKeys",String.class);keys.setAccessible(true);keys.invoke(screen,"win_full_keyboard_main.json");}catch(Exception ex){throw new RuntimeException(ex);}});
        Thread.sleep(500);
        android.view.View panel=(android.view.View)field(screen,"panel");android.view.View root=(android.view.View)field(screen,"root");
        if(panel.getHeight()>root.getHeight()*.56||view.getHeight()<originalHeight*.40)throw new AssertionError("Full keyboard obscures remote view");
        result.putString("fullKeyboardVisibleViewport",String.valueOf(view.getHeight()));
        runOnMainSync(()->{try{java.lang.reflect.Method hide=screen.getClass().getDeclaredMethod("hidePanel");hide.setAccessible(true);hide.invoke(screen);}catch(Exception ex){throw new RuntimeException(ex);}});

        runOnMainSync(screen::showPhoneKeyboard);
        Bundle debug=new Bundle();debug.putString("keyboardProbe","requested windowFocus="+screen.hasWindowFocus()+" editorFocus="+((UuTextInputAdapter)field(screen,"ime")).view().hasFocus());sendStatus(1,debug);
        long deadline=System.currentTimeMillis()+6000;while(System.currentTimeMillis()<deadline&&(int)field(screen,"imeBottom")==0)Thread.sleep(100);
        Thread.sleep(350);android.graphics.Rect visible=new android.graphics.Rect();screen.getWindow().getDecorView().getWindowVisibleDisplayFrame(visible);result.putString("imeGeometry","decor="+screen.getWindow().getDecorView().getHeight()+" visible="+visible+" rootInsets="+screen.getWindow().getDecorView().getRootWindowInsets());if((int)field(screen,"imeBottom")<=0||view.getHeight()>=originalHeight){Bundle debug2=new Bundle();debug2.putString("keyboardProbe","missing windowFocus="+screen.hasWindowFocus()+" editorFocus="+((UuTextInputAdapter)field(screen,"ime")).view().hasFocus());sendStatus(1,debug2);Thread.sleep(15000);}if((int)field(screen,"imeBottom")<=0||view.getHeight()>=originalHeight)throw new AssertionError("Phone IME covers stream or inset missing: height="+view.getHeight()+" original="+originalHeight+" inset="+field(screen,"imeBottom"));
        result.putString("keyboardViewport",originalHeight+" -> "+view.getHeight()+" pixels; IME="+field(screen,"imeBottom"));
        runOnMainSync(()->{try{java.lang.reflect.Method hide=screen.getClass().getDeclaredMethod("hidePhoneKeyboard");hide.setAccessible(true);hide.invoke(screen);}catch(Exception ex){throw new RuntimeException(ex);}});
        deadline=System.currentTimeMillis()+6000;while(System.currentTimeMillis()<deadline&&(int)field(screen,"imeBottom")>0)Thread.sleep(100);Thread.sleep(350);
        if(view.getHeight()!=originalHeight)throw new AssertionError("Stream size not restored after IME");
        if((boolean)field(screen,"stopRequested"))throw new AssertionError("Keyboard resizing disconnected stream");
        result.putString("keyboardLifecycle","IME show/hide preserved connection and restored viewport");
    }
    private void verifyHostInput(UuStreamActivity screen)throws Exception {
        runOnMainSync(()->{try{((TouchRouter)field(screen,"touch")).mode(false);}catch(Exception ex){throw new RuntimeException(ex);}});
        touch(screen,0,.3f,.3f);Thread.sleep(60);touch(screen,1,.3f,.3f);Thread.sleep(400);
        final Throwable[] failure={null};runOnMainSync(()->{try{
            UuTextInputAdapter ime=(UuTextInputAdapter)field(screen,"ime");InputConnection ic=ime.view().onCreateInputConnection(new EditorInfo());
            ic.setComposingText("ni",1);ic.commitText("你好🙂 DD",1);
        }catch(Throwable ex){failure[0]=ex;}});if(failure[0]!=null)throw new AssertionError(failure[0]);Thread.sleep(800);
        runOnMainSync(()->{try{SunshineInput keys=(SunshineInput)field(screen,"input");keys.chord(android.view.KeyEvent.KEYCODE_CTRL_LEFT,android.view.KeyEvent.KEYCODE_A);keys.text("DD 输入验证🙂");}catch(Exception ex){throw new RuntimeException(ex);}});Thread.sleep(700);
        touch(screen,0,.3f,.4f);Thread.sleep(650);touch(screen,2,.55f,.4f);Thread.sleep(100);touch(screen,1,.55f,.4f);Thread.sleep(250);
        runOnMainSync(()->{try{((TouchRouter)field(screen,"touch")).mode(true);}catch(Exception ex){throw new RuntimeException(ex);}});
        touch(screen,0,.4f,.4f);twoFinger(screen,5|(1<<8),.4f);Thread.sleep(60);twoFinger(screen,6|(1<<8),.4f);touch(screen,1,.4f,.4f);Thread.sleep(250);
        runOnMainSync(()->{try{((SunshineInput)field(screen,"input")).stroke(android.view.KeyEvent.KEYCODE_ESCAPE);}catch(Exception ex){throw new RuntimeException(ex);}});Thread.sleep(200);
        touch(screen,0,.4f,.4f);twoFinger(screen,5|(1<<8),.4f);twoFinger(screen,2,.6f);twoFinger(screen,6|(1<<8),.6f);touch(screen,1,.4f,.6f);Thread.sleep(800);
    }
    @Override public void onStart(){
        if("inputlogic".equals(args.getString("mode"))){InputVerification.run(this);return;}
        Bundle result=new Bundle();Activity activity=null;
        try{
            final long token=AccountSession.open(getTargetContext());
            String host="10.0.2.2";int port=48989;
            NvHTTP http=new NvHTTP(new ComputerDetails.AddressTuple(host,port),48984,AccountSession.uniqueId(token),null,AccountSession.cryptoProvider(getTargetContext(),token));
            String info=http.getServerInfo(true);
            PairingManager pairing=http.getPairingManager();
            PairingManager.PairState paired=pairing.pair(info,"4186");
            if(paired!=PairingManager.PairState.PAIRED)throw new AssertionError("Pair state "+paired);
            X509Certificate cert=pairing.getPairedCert();
            NvApp desktop=null;for(NvApp app:http.getAppList())if("Desktop".equals(app.getAppName()))desktop=app;
            if(desktop==null)throw new AssertionError("Desktop not published");
            Intent intent=new Intent(getTargetContext(),UuStreamActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            intent.putExtra("DdAccountGeneration",token);intent.putExtra("host",host);intent.putExtra("port",port);intent.putExtra("httpsPort",48984);
            intent.putExtra("uniqueId",AccountSession.uniqueId(token));intent.putExtra("cert",cert.getEncoded());intent.putExtra("appName","Desktop");intent.putExtra("appId",desktop.getAppId());
            intent.putExtra("fps",30);intent.putExtra("bitrate",5000);
            activity=startActivitySync(intent);
            UuStreamActivity screen=(UuStreamActivity)activity;
            result.putString("renderedFrames720",String.valueOf(awaitFrames(screen)));
            result.putString("surface",field(screen,"surface").getClass().getName());
            result.putString("ime",((UuTextInputAdapter)field(screen,"ime")).view().getClass().getName());
            if("mobileui".equals(args.getString("mode")))verifyMobileUI(screen,result);
            if("audio".equals(args.getString("mode"))){
                StreamAudio audio=(StreamAudio)field(screen,"audio");audio.muted=false;
                Bundle signal=new Bundle();signal.putString("audioStage","toneReady");sendStatus(1,signal);
                long until=System.currentTimeMillis()+10000;while(System.currentTimeMillis()<until&&audio.nonSilentPackets.get()<30)Thread.sleep(100);
                result.putString("totalAudioPackets",String.valueOf(audio.packets.get()));
                result.putString("nonSilentAudioPackets",String.valueOf(audio.nonSilentPackets.get()));
                if(audio.nonSilentPackets.get()<30)throw new AssertionError("No non-silent host audio received");
                audio.muted=true;until=System.currentTimeMillis()+4000;while(System.currentTimeMillis()<until&&audio.mutedPackets.get()<30)Thread.sleep(100);
                result.putString("mutedAudioPackets",String.valueOf(audio.mutedPackets.get()));
                if(audio.mutedPackets.get()<30)throw new AssertionError("No packets delivered through local mute");
                audio.muted=false;Activity last=activity;runOnMainSync(last::finish);assertReleased(last);finish(Activity.RESULT_OK,result);return;
            }
            if("hostinput".equals(args.getString("mode"))){
                verifyHostInput(screen);result.putString("hostInputDispatched","original UU IME + production direct/trackpad router; host report is authoritative");
                Activity last=activity;runOnMainSync(last::finish);assertReleased(last);finish(Activity.RESULT_OK,result);return;
            }
            if("privacy".equals(args.getString("mode"))){
                for(String stage:new String[]{"baseline","masked","restored"}){
                    Bundle sample=new Bundle();sample.putString("sampleStage",stage);sample.putString("colors",sampleSurface(screen));sendStatus(1,sample);
                    if(!"restored".equals(stage))Thread.sleep(6000);
                }
                Activity last=activity;runOnMainSync(last::finish);assertReleased(last);result.putString("privacySamples","recorded (host coordinator must check)");finish(Activity.RESULT_OK,result);return;
            }
            if("true".equals(args.getString("input"))){
                Thread.sleep(2000);
                final Throwable[] failure={null};
                runOnMainSync(()->{try{
                    UuTextInputAdapter ime=(UuTextInputAdapter)field(screen,"ime");
                    InputConnection ic=ime.view().onCreateInputConnection(new EditorInfo());
                    ic.setComposingText("ni",1);ic.commitText("你好🙂 DD",1);
                }catch(Throwable ex){failure[0]=ex;}});
                if(failure[0]!=null)throw new AssertionError(failure[0]);
                Thread.sleep(2500);
                runOnMainSync(()->{try{
                    SunshineInput keys=(SunshineInput)field(screen,"input");keys.chord(android.view.KeyEvent.KEYCODE_CTRL_LEFT,android.view.KeyEvent.KEYCODE_A);keys.text("DD 输入验证🙂");
                }catch(Throwable ex){failure[0]=ex;}});
                if(failure[0]!=null)throw new AssertionError(failure[0]);
                result.putString("inputDispatched","original UU IME + Ctrl+A synthetic test");
            }
            ActivityMonitor monitor=addMonitor(UuStreamActivity.class.getName(),null,false);
            runOnMainSync(()->screen.changeQuality(2));
            Activity replacement=waitForMonitorWithTimeout(monitor,15000);removeMonitor(monitor);
            if(replacement==null)throw new AssertionError("Quality did not reconnect");
            activity=replacement;
            result.putString("renderedFrames1080",String.valueOf(awaitFrames((UuStreamActivity)replacement)));
            if(replacement.getIntent().getIntExtra("width",0)!=1920)throw new AssertionError("Quality dimensions missing");
            result.putString("qualityReconnect","720p30 to 1080p60 passed");
            Thread.sleep(4000);
            Activity last=activity;runOnMainSync(last::finish);assertReleased(last);
            // A cancelled start must not leave a connection or over-release the semaphore.
            Activity cancelled=startActivitySync(intent);activity=cancelled;
            runOnMainSync(cancelled::finish);Thread.sleep(2000);assertReleased(cancelled);
            result.putString("startupCancellation","passed");result.putString("teardown","passed");
            finish(Activity.RESULT_OK,result);
        }catch(Throwable ex){android.util.Log.e("DDStreamTest","Test failed",ex);result.putString("failure",ex.toString());if(activity!=null){Activity last=activity;runOnMainSync(last::finish);}finish(Activity.RESULT_CANCELED,result);}
    }
}
