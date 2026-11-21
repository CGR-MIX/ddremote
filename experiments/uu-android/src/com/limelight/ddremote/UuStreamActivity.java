package com.limelight.ddremote;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.graphics.Color;
import android.os.Bundle;
import android.os.Build;
import android.view.WindowInsets;
import android.view.inputmethod.EditorInfo;
import android.media.AudioManager;
import android.graphics.Rect;
import android.widget.SeekBar;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.SurfaceHolder;
import android.view.SurfaceView;
import android.view.View;
import android.view.WindowManager;
import android.view.inputmethod.InputMethodManager;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.TextView;
import com.limelight.binding.audio.AndroidAudioRenderer;
import com.limelight.nvstream.NvConnection;
import com.limelight.nvstream.NvConnectionListener;
import com.limelight.nvstream.StreamConfiguration;
import com.limelight.nvstream.http.ComputerDetails;
import com.limelight.nvstream.http.NvApp;
import com.limelight.nvstream.jni.MoonBridge;
import org.json.JSONArray;
import org.json.JSONObject;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;

/** DD-owned stream lifecycle hosted in the UU APK, using UU surface and text input. */
public final class UuStreamActivity extends Activity implements SurfaceHolder.Callback {
    public static final String EXTRA_HOST="host", EXTRA_PORT="port",EXTRA_HTTPS_PORT="httpsPort",EXTRA_APP_NAME="appName",EXTRA_APP_ID="appId",EXTRA_APP_HDR="hdr",EXTRA_UNIQUEID="uniqueId",EXTRA_PC_UUID="pcUuid",EXTRA_PC_NAME="pcName",EXTRA_SERVER_CERT="cert";
    private final Handler ui=new Handler(Looper.getMainLooper());
    private FrameLayout root,viewport;
    private StreamPreferences preferences;
    private StreamAudio audio;
    private int imeBottom;
    private boolean keyboardRequested;
    private SurfaceView surface;
    private TextView status;
    private LinearLayout panel,keyboard;
    private UuTextInputAdapter ime;
    private SunshineInput input;
    private RemotePrivacy privacy;
 private RemoteSuperScreen superScreen;
 private boolean superMode;
    private TouchRouter touch;
    private NvConnection conn;
    private SunshineVideoRenderer video;
    private boolean started,closed,connected;
    private long messageUntil;
    private long accountToken;
    private int width=1280,height=720,fps=60,bitrate=10000;
    private volatile boolean stopRequested;
    private final Object stopLock=new Object();
    private boolean stopIssued;
    private Intent reconnect;
    private final Runnable stats=new Runnable(){public void run(){if(closed)return;if(video!=null&&connected&&android.os.SystemClock.uptimeMillis()>messageUntil)status.setText(width+"×"+height+" · "+fps+"帧 · "+(bitrate/1000)+" Mbps · 已显示 "+video.rendered.get()+" 帧");ui.postDelayed(this,2000);}};
    @Override public void onCreate(Bundle bundle){
        super.onCreate(bundle);
        accountToken=getIntent().getLongExtra("DdAccountGeneration",0);
        if(!AccountSession.isCurrent(accountToken)){finish();return;}
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        if(Build.VERSION.SDK_INT>=30)getWindow().setDecorFitsSystemWindows(false);
        setVolumeControlStream(AudioManager.STREAM_MUSIC);preferences=new StreamPreferences(this);
        width=getIntent().getIntExtra("width",1280);height=getIntent().getIntExtra("height",720);fps=getIntent().getIntExtra("fps",60);bitrate=getIntent().getIntExtra("bitrate",10000);
        superMode=getIntent().getBooleanExtra("DdSuperScreen",false);
 if(superMode)superScreen=new RemoteSuperScreen(accountToken,getIntent().getStringExtra("DdPeerIp"),()->{message("超级屏连接已中断或已在电脑恢复，请重新连接");reconnect=null;requestStop();});
 root=new FrameLayout(this);root.setBackgroundColor(Color.BLACK);setContentView(root);
        if(Build.VERSION.SDK_INT>=30){getWindow().getInsetsController().hide(WindowInsets.Type.systemBars());getWindow().getInsetsController().setSystemBarsBehavior(android.view.WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE);}else{getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_FULLSCREEN|View.SYSTEM_UI_FLAG_HIDE_NAVIGATION|View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY); }
        try{
            surface=(SurfaceView)Class.forName("com.remote.streamer.StreamerSurfaceView").getConstructor(android.content.Context.class).newInstance(this);
            viewport=new FrameLayout(this);root.addView(viewport,new FrameLayout.LayoutParams(-1,-1));
            viewport.addView(surface,new FrameLayout.LayoutParams(-1,-1,Gravity.CENTER));
            viewport.addOnLayoutChangeListener((v,l,t,r,b,ol,ot,or,ob)->fitSurface());
            root.setFocusableInTouchMode(true);
            root.setOnApplyWindowInsetsListener((v,insets)->{if(Build.VERSION.SDK_INT>=30){imeBottom=insets.getInsets(WindowInsets.Type.ime()).bottom;updateViewport();}return insets;});
            root.getViewTreeObserver().addOnGlobalLayoutListener(()->{if(Build.VERSION.SDK_INT>=30){WindowInsets insets=root.getRootWindowInsets();if(insets!=null)imeBottom=insets.getInsets(WindowInsets.Type.ime()).bottom;}else{Rect visible=new Rect();root.getWindowVisibleDisplayFrame(visible);int hidden=root.getRootView().getHeight()-visible.bottom;imeBottom=hidden>dp(120)?hidden:0;}updateViewport();});
            surface.getHolder().addCallback(this);
            status=new TextView(this);status.setTextColor(Color.WHITE);status.setBackgroundColor(0xb0152c56);status.setPadding(dp(12),dp(5),dp(12),dp(5));status.setText("正在连接电脑…");
            root.addView(status,new FrameLayout.LayoutParams(-2,-2,Gravity.TOP|Gravity.CENTER_HORIZONTAL));
            Button menu=new Button(this);menu.setText("DD 控制");FrameLayout.LayoutParams mp=new FrameLayout.LayoutParams(dp(105),dp(46),Gravity.TOP|Gravity.LEFT);mp.topMargin=dp(30);root.addView(menu,mp);menu.setOnClickListener(v->showMenu());
            panel=new LinearLayout(this){@Override protected void onMeasure(int w,int h){
                int available=root.getHeight();
                if(available>0)h=View.MeasureSpec.makeMeasureSpec((int)(available*.55f),View.MeasureSpec.AT_MOST);
                super.onMeasure(w,h);
            }};panel.setOrientation(LinearLayout.VERTICAL);panel.setBackgroundColor(0xee14223b);panel.setVisibility(View.GONE);
            root.addView(panel,new FrameLayout.LayoutParams(-1,-2,Gravity.BOTTOM));
            panel.addOnLayoutChangeListener((v,l,t,r,b,ol,ot,or,ob)->updateViewport());
            ime=UuTextInputAdapter.create(this,new UuTextInputAdapter.Sink(){public void committedText(String value){if(input!=null)input.text(value);}public void keyStroke(int code){if(input!=null)input.stroke(code);}});
            EditText editor=ime.view();editor.setImeOptions(editor.getImeOptions()|EditorInfo.IME_FLAG_NO_EXTRACT_UI|EditorInfo.IME_FLAG_NO_FULLSCREEN);root.addView(editor,new FrameLayout.LayoutParams(1,1));
            root.requestApplyInsets();
            ui.post(stats);
        }catch(Exception|LinkageError ex){new AlertDialog.Builder(this).setMessage("远控组件加载失败："+UuAdapterHome.friendly(ex)).setPositiveButton("返回",(d,w)->finish()).show();}
    }
    private int dp(int n){return Math.round(n*getResources().getDisplayMetrics().density);}
    private void fitSurface(){if(surface==null)return;int w=viewport.getWidth(),h=viewport.getHeight();if(w==0||h==0)return;float ratio=(float)width/height;int sw=w,sh=(int)(w/ratio);if(sh>h){sh=h;sw=(int)(h*ratio);}FrameLayout.LayoutParams p=(FrameLayout.LayoutParams)surface.getLayoutParams();if(p.width!=sw||p.height!=sh){p.width=sw;p.height=sh;p.gravity=Gravity.CENTER;surface.setLayoutParams(p);}}
    private void updateViewport(){
        if(viewport==null)return;int bottom=Math.max(imeBottom,panel!=null&&panel.getVisibility()==View.VISIBLE?panel.getHeight():0);
        FrameLayout.LayoutParams p=(FrameLayout.LayoutParams)viewport.getLayoutParams();if(p.bottomMargin!=bottom){p.bottomMargin=bottom;viewport.setLayoutParams(p);}
    }
    private void hidePanel(){panel.setVisibility(View.GONE);updateViewport();}
    void showPhoneKeyboard(){hidePanel();keyboardRequested=true;ime.view().requestFocus();ime.view().postDelayed(this::requestKeyboardWhenFocused,100);}
    private void requestKeyboardWhenFocused(){if(!keyboardRequested||closed||stopRequested||!hasWindowFocus())return;EditText editor=ime.view();editor.requestFocus();InputMethodManager manager=(InputMethodManager)getSystemService(INPUT_METHOD_SERVICE);manager.showSoftInput(editor,InputMethodManager.SHOW_IMPLICIT);if(Build.VERSION.SDK_INT>=30)getWindow().getInsetsController().show(WindowInsets.Type.ime());}
    @Override public void onWindowFocusChanged(boolean focused){super.onWindowFocusChanged(focused);if(focused&&keyboardRequested&&ime!=null)ime.view().postDelayed(this::requestKeyboardWhenFocused,100);}
    private void hidePhoneKeyboard(){keyboardRequested=false;((InputMethodManager)getSystemService(INPUT_METHOD_SERVICE)).hideSoftInputFromWindow(root.getWindowToken(),0);root.requestFocus();}
    @Override public void surfaceCreated(SurfaceHolder holder){if(!started&&!closed&&!stopRequested){started=true;if(superScreen==null){startStream();}else{message("正在准备超级屏，电脑屏幕将暂时关闭输出…");superScreen.prepare(width,height,fps,error->{if(stopRequested||closed)return;if(error==null){startStream();}else{message("超级屏未开启："+error);requestStop();new AlertDialog.Builder(this).setTitle("超级屏未开启").setMessage(error).setPositiveButton("返回",(d,w)->finish()).show();}});}}}
    @Override public void surfaceChanged(SurfaceHolder h,int f,int w,int height){}
    @Override public void surfaceDestroyed(SurfaceHolder h){requestStop();}
    private void startStream(){
        try{
            AccountSession.require(accountToken);
            X509Certificate cert=(X509Certificate)CertificateFactory.getInstance("X.509").generateCertificate(new ByteArrayInputStream(getIntent().getByteArrayExtra(EXTRA_SERVER_CERT)));
            StreamConfiguration config=new StreamConfiguration.Builder().setApp(new NvApp(getIntent().getStringExtra(EXTRA_APP_NAME),getIntent().getIntExtra(EXTRA_APP_ID,0),false))
                    .setResolution(width,height).setRefreshRate(fps).setLaunchRefreshRate(fps).setBitrate(bitrate).setMaxPacketSize(1024)
                    .setRemoteConfiguration(StreamConfiguration.STREAM_CFG_REMOTE).setSupportedVideoFormats(MoonBridge.VIDEO_FORMAT_H264)
                    .setAudioConfiguration(MoonBridge.AUDIO_CONFIGURATION_STEREO).setClientRefreshRateX100(fps*100).setEnableSops(false).build();
            conn=new NvConnection(this,new ComputerDetails.AddressTuple(getIntent().getStringExtra(EXTRA_HOST),getIntent().getIntExtra(EXTRA_PORT,47989)),getIntent().getIntExtra(EXTRA_HTTPS_PORT,47984),getIntent().getStringExtra(EXTRA_UNIQUEID),config,AccountSession.cryptoProvider(this,accountToken),cert);
            video=new SunshineVideoRenderer(surface.getHolder().getSurface());
            audio=new StreamAudio(this,preferences.muted());conn.start(audio,video,new NvConnectionListener(){
                public void stageStarting(String stage){message("正在连接："+stage);}
                public void stageComplete(String stage){}
                public void stageFailed(String stage,int ports,int code){message("连接失败："+stage+" ("+code+")");ui.post(()->requestStop());}
                public void connectionStarted(){ui.post(()->{connected=true;if(stopRequested||!AccountSession.isCurrent(accountToken)){requestStop();return;}input=new SunshineInput(conn);privacy=new RemotePrivacy(UuStreamActivity.this,accountToken,getIntent().getStringExtra("DdPeerIp"),UuStreamActivity.this::message);touch=new TouchRouter(conn,surface);touch.mode(preferences.trackpad());touch.sensitivity(preferences.sensitivity());surface.setOnTouchListener(touch);message("已连接，点击 DD 控制打开工具栏");});}
                public void connectionTerminated(int code){ui.post(()->{message("连接已结束 ("+code+")");requestStop();});}
                public void connectionStatusUpdate(int value){if(value==MoonBridge.CONN_STATUS_POOR)message("网络波动，可在画质中切换到流畅模式");}
                public void displayMessage(String text){message(text);}
                public void displayTransientMessage(String text){message(text);}
                public void rumble(short a,short b,short c){}public void rumbleTriggers(short a,short b,short c){}
                public void setHdrMode(boolean enabled,byte[] data){}public void setMotionEventState(short a,byte b,short c){}public void setControllerLED(short a,byte b,byte c,byte d){}
            });
        }catch(Exception|LinkageError ex){message("连接失败："+UuAdapterHome.friendly(ex));requestStop();}
    }
    private void message(String text){ui.post(()->{if(!closed&&status!=null){status.setText(text);messageUntil=android.os.SystemClock.uptimeMillis()+5000;}});}
    private void showMenu(){
        if(panel.getVisibility()==View.VISIBLE){hidePanel();return;}hidePhoneKeyboard();
        panel.removeAllViews();panel.setVisibility(View.VISIBLE);
        android.widget.ScrollView vertical=new android.widget.ScrollView(this);
        LinearLayout content=new LinearLayout(this);content.setOrientation(LinearLayout.VERTICAL);
        vertical.addView(content);panel.addView(vertical,new LinearLayout.LayoutParams(-1,-2));
        HorizontalScrollView scroll=new HorizontalScrollView(this);LinearLayout row=new LinearLayout(this);scroll.addView(row);content.addView(scroll);
        action(row,"手机输入法",this::showPhoneKeyboard);
        action(row,"完整键盘",()->showKeys("win_full_keyboard_main.json"));
        action(row,"数字键",()->showKeys("full_keyboard_num.json"));action(row,"功能键",()->showKeys("full_keyboard_func.json"));
        action(row,touch==null||touch.isTrackpad()?"触控板模式":"直接触控模式",this::touchOptions);
        action(row,"画质",this::quality);action(row,superMode?"退出超级屏":"超级屏",this::superScreenDialog);action(row,privacy!=null&&privacy.active()?"关闭隐私遮罩":"隐私遮罩",()->{if(privacy!=null)privacy.toggle();else message("连接完成后可使用隐私遮罩");});action(row,audio!=null&&audio.muted?"开启声音":"静音",()->{if(audio!=null){audio.muted=!audio.muted;preferences.muted(audio.muted);message(audio.muted?"远程声音已静音":"远程声音已开启");hidePanel();}});action(row,"断开",this::finish);
        LinearLayout shortcuts=new LinearLayout(this);HorizontalScrollView quick=new HorizontalScrollView(this);quick.addView(shortcuts);content.addView(quick);
        action(shortcuts,"Esc",()->stroke(KeyEvent.KEYCODE_ESCAPE));action(shortcuts,"Tab",()->stroke(KeyEvent.KEYCODE_TAB));
        action(shortcuts,"复制",()->chord(KeyEvent.KEYCODE_CTRL_LEFT,KeyEvent.KEYCODE_C));action(shortcuts,"粘贴",()->chord(KeyEvent.KEYCODE_CTRL_LEFT,KeyEvent.KEYCODE_V));
        action(shortcuts,"全选",()->chord(KeyEvent.KEYCODE_CTRL_LEFT,KeyEvent.KEYCODE_A));action(shortcuts,"切换窗口",()->chord(KeyEvent.KEYCODE_ALT_LEFT,KeyEvent.KEYCODE_TAB));
        keyboard=new LinearLayout(this);keyboard.setOrientation(LinearLayout.VERTICAL);content.addView(keyboard);
    }
    private void action(LinearLayout row,String text,Runnable action){Button b=new Button(this);b.setText(text);b.setAllCaps(false);b.setTextSize(12);row.addView(b,new LinearLayout.LayoutParams(-2,dp(45)));b.setOnClickListener(v->action.run());}
    private void stroke(int code){if(input!=null)input.stroke(code);}
    private void chord(int modifier,int code){if(input!=null)input.chord(modifier,code);}
    private void showKeys(String name){
        if(keyboard==null)return;keyboard.removeAllViews();
        try(java.io.InputStream in=getAssets().open("fullkeyboard/"+name);java.io.ByteArrayOutputStream out=new java.io.ByteArrayOutputStream()){
            byte[] buf=new byte[4096];int n;while((n=in.read(buf))>0)out.write(buf,0,n);
            JSONArray rows=new JSONObject(new String(out.toByteArray(),StandardCharsets.UTF_8)).getJSONArray("keyRowList");
            HorizontalScrollView scroll=new HorizontalScrollView(this);LinearLayout board=new LinearLayout(this);board.setOrientation(LinearLayout.VERTICAL);scroll.addView(board);keyboard.addView(scroll);
            for(int r=0;r<rows.length();r++){LinearLayout row=new LinearLayout(this);board.addView(row);JSONArray entries=rows.getJSONObject(r).getJSONArray("keyItemList");for(int i=0;i<entries.length();i++){
                JSONObject item=entries.getJSONObject(i);int code=item.optInt("keyCode");Button key=new Button(this);key.setText(item.optString("displayName"));key.setAllCaps(false);key.setTextSize(11);key.setMinWidth(0);key.setMinimumWidth(0);key.setPadding(0,0,0,0);row.addView(key,new LinearLayout.LayoutParams(dp((int)(Math.max(1,item.optDouble("width",1))*44)),dp(42)));
                key.setOnTouchListener((v,e)->{if(input==null)return true;if(e.getActionMasked()==MotionEvent.ACTION_DOWN)input.key(code,true);else if(e.getActionMasked()==MotionEvent.ACTION_UP||e.getActionMasked()==MotionEvent.ACTION_CANCEL)input.key(code,false);return true;});
            }}
        }catch(Exception ex){message("键盘布局读取失败");}
    }
    private void superScreenDialog(){
        if(stopRequested||!connected){message("连接完成后可切换超级屏");return;}
        if(privacy!=null&&privacy.active()){message("请先关闭隐私遮罩再切换超级屏");return;}
        new AlertDialog.Builder(this).setTitle(superMode?"退出超级屏":"超级屏 · 试验功能")
          .setMessage(superMode?"退出后恢复电脑原有显示布局，并重新连接。":"电脑将创建 DD 专属虚拟显示器，关闭物理屏的桌面输出。\n\n需要电脑端已安装驱动并以管理员身份运行。尚未完成真机验收。\n\n断开连接后恢复原布局；电脑快捷键 Ctrl+Alt+Shift+F11 可恢复。")
          .setPositiveButton(superMode?"退出并重连":"开启并重连",(d,w)->{reconnect=new Intent(getIntent()).setFlags(0);reconnect.putExtra("DdSuperScreen",!superMode);requestStop();})
          .setNegativeButton("取消",null).show();
    }
    private void touchOptions(){
        int current=touch!=null&&!touch.isTrackpad()?1:0;
        new AlertDialog.Builder(this).setTitle("触控方式").setSingleChoiceItems(new String[]{"触控板：单指移动，双指滚动／右键","直接触控：点击定位，长按拖动"},current,(d,which)->{boolean trackpad=which==0;preferences.trackpad(trackpad);if(touch!=null)touch.mode(trackpad);d.dismiss();hidePanel();message(trackpad?"已切换触控板，可在触控设置中调整速度":"已切换直接触控，点击画面即可定位");})
        .setNeutralButton("指针速度",(d,w)->pointerSpeed()).setNegativeButton("关闭",null).show();
    }
    private void pointerSpeed(){
        LinearLayout content=new LinearLayout(this);content.setOrientation(LinearLayout.VERTICAL);content.setPadding(dp(24),dp(12),dp(24),dp(12));TextView label=new TextView(this);content.addView(label);SeekBar speed=new SeekBar(this);speed.setMax(15);speed.setProgress(Math.round((preferences.sensitivity()-.5f)*10));label.setText("速度 "+String.format(java.util.Locale.ROOT,"%.1f",preferences.sensitivity())+"×");content.addView(speed);
        speed.setOnSeekBarChangeListener(new SeekBar.OnSeekBarChangeListener(){public void onProgressChanged(SeekBar b,int n,boolean user){label.setText("速度 "+String.format(java.util.Locale.ROOT,"%.1f",.5f+n/10f)+"×");}public void onStartTrackingTouch(SeekBar b){}public void onStopTrackingTouch(SeekBar b){}});
        new AlertDialog.Builder(this).setTitle("触控板指针速度").setView(content).setPositiveButton("应用",(d,w)->{float value=.5f+speed.getProgress()/10f;preferences.sensitivity(value);if(touch!=null)touch.sensitivity(value);}).setNegativeButton("取消",null).show();
    }
    private void quality(){int current=width==1920?2:fps==30?0:1;new AlertDialog.Builder(this).setTitle("画质 · 应用后短暂重连").setSingleChoiceItems(StreamPreferences.QUALITY_LABELS,current,(d,index)->{d.dismiss();changeQuality(index);}).setNegativeButton("取消",null).show();}
    void changeQuality(int index){
        if(stopRequested||conn==null||!connected){message("连接完成后可调整画质");return;}
        message("正在切换画质…");
        if(index<0||index>2)return;preferences.quality(index);reconnect=new Intent(getIntent()).setFlags(0);StreamPreferences.applyQuality(reconnect,index);requestStop();
    }
    private void requestStop(){
        stopRequested=true;connected=false;if(privacy!=null)privacy.close();if(touch!=null)touch.close();if(input!=null)input.close();
        synchronized(stopLock){if(stopIssued)return;stopIssued=true;}
        java.util.concurrent.Future<?> restore=superScreen==null?null:superScreen.close();
        new Thread(()->{try{if(conn!=null)conn.stop();if(restore!=null)restore.get(25,java.util.concurrent.TimeUnit.SECONDS);}
            catch(Exception ex){ui.post(()->{reconnect=null;message("未确认原显示布局恢复，请检查电脑后再连接");});}
            finally{afterStopped();}},"dd-uu-stop").start();
    }
    private void afterStopped(){ui.post(()->{connected=false;if(reconnect!=null&&AccountSession.isCurrent(accountToken)){Intent next=reconnect;reconnect=null;startActivity(next);finish();}});}
    static boolean localKey(int code){return code==KeyEvent.KEYCODE_VOLUME_UP||code==KeyEvent.KEYCODE_VOLUME_DOWN||code==KeyEvent.KEYCODE_VOLUME_MUTE||code==KeyEvent.KEYCODE_POWER||code==KeyEvent.KEYCODE_HOME;}
    @Override public boolean onKeyDown(int code,KeyEvent event){if(code==KeyEvent.KEYCODE_BACK){showMenu();return true;}if(input!=null&&event.getDeviceId()>=0&&!localKey(code)&&input.supports(code)){input.key(code,true);return true;}return super.onKeyDown(code,event);}
    @Override public boolean onKeyUp(int code,KeyEvent event){if(code==KeyEvent.KEYCODE_BACK)return true;if(input!=null&&event.getDeviceId()>=0&&!localKey(code)&&input.supports(code)){input.key(code,false);return true;}return super.onKeyUp(code,event);}
    @Override protected void onStop(){super.onStop();reconnect=null;requestStop();finish();}
    @Override protected void onDestroy(){closed=true;ui.removeCallbacks(stats);if(ime!=null)ime.close();requestStop();super.onDestroy();}
}
