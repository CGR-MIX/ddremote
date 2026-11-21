package com.limelight.ddremote;

import android.app.Activity;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import org.json.JSONArray;
import org.json.JSONObject;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;

/** Replacement account entry in the rebuilt UU APK. No original account SDK runs. */
public final class UuAdapterHome extends Activity {
    private final Handler ui = new Handler(Looper.getMainLooper());
    private final ExecutorService worker = Executors.newSingleThreadExecutor();
    private TextView status;
    private LinearLayout devices;
    private Button login, logout;
    private boolean visible, busy, closed, closing, pendingLogin;
    private long token;
    private boolean connecting;
    private final Runnable poll = () -> refresh();

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        token = AccountSession.open(getApplicationContext());
        LinearLayout page = page(this);
        heading(page,"DD远程 · 试验版");
        text(page,"登录账号，选择你的电脑即可开始远程控制。",15);
        status=text(page,"点击登录账号，同账号电脑会自动显示。",16);
        login=button(page,"登录账号",v -> login());
        logout=button(page,"退出并移除此设备",v -> logout());
        logout.setVisibility(View.GONE);
        button(page,"刷新我的电脑",v -> refresh());
        text(page,"我的电脑",22);
        devices=new LinearLayout(this); devices.setOrientation(LinearLayout.VERTICAL); page.addView(devices);
        button(page,"验证 UU 原生键盘组件",v -> startActivity(new Intent(this,UuInputLab.class)));
        text(page,"超级屏需搭配0.3.3电脑端、虚拟屏驱动和管理员权限。超级屏与隐私遮罩仍待实机验证。",14);
    }
    @Override protected void onResume() {super.onResume();visible=true;refresh();}
    @Override protected void onPause() {visible=false;ui.removeCallbacks(poll);super.onPause();}
    @Override protected void onDestroy() {
        closed=true;ui.removeCallbacks(poll);worker.shutdownNow();
        if(isFinishing()&&isTaskRoot()) EmbeddedNetwork.finishSession(getApplicationContext());
        super.onDestroy();
    }
    private void post(long generation,Runnable action) {
        ui.post(() -> { if(!closed && AccountSession.isCurrent(generation)) action.run(); });
    }
    private void refresh() {
        if(!visible||closed||busy||closing||connecting||!AccountSession.isCurrent(token))return;
        final long generation=token; busy=true; ui.removeCallbacks(poll);
        worker.execute(() -> {
            try {EmbeddedNetwork.start(getApplicationContext(),generation);
                JSONObject data=EmbeddedNetwork.call(generation,"discover",new JSONObject());
                post(generation,() -> render(data));
            } catch(Exception|LinkageError ex) {post(generation,() -> status.setText("网络不可用："+friendly(ex)));}
            finally {post(generation,() -> {busy=false;if(visible)ui.postDelayed(poll,5000);});}
        });
    }
    private void render(JSONObject data) {
        boolean running=data.optBoolean("running");
        status.setText(running?"已登录 "+data.optString("account"):"请完成账号登录后等待设备发现。");
        login.setEnabled(!running);login.setText(running?"账号已连接":"登录账号");
        logout.setVisibility(running?View.VISIBLE:View.GONE);
        devices.removeAllViews();JSONArray list=data.optJSONArray("devices");
        if(list==null||list.length()==0)text(devices,"尚未发现电脑；电脑需打开 DD、登录相同账号并开始接收。",15);
        else for(int i=0;i<list.length();i++) {JSONObject d=list.optJSONObject(i);if(d==null)continue;
            Button deviceButton=button(devices,d.optString("name")+(d.optBoolean("ready")?" · 远程控制":" · 未准备好"),v -> connect(d));deviceButton.setEnabled(d.optBoolean("ready"));
        }
        String auth=data.optString("authUrl");
        if(pendingLogin&&!auth.isEmpty()) {
            Uri uri=Uri.parse(auth);String host=uri.getHost();
            if("https".equals(uri.getScheme())&&host!=null&&(host.equals("tailscale.com")||host.endsWith(".tailscale.com"))) {
                pendingLogin=false;
                try {startActivity(new Intent(Intent.ACTION_VIEW,uri));}
                catch(android.content.ActivityNotFoundException ex) {status.setText("没有可用浏览器，请安装后重试。");}
            } else {pendingLogin=false;status.setText("账号服务返回了无效地址。");}
        }
    }
    private void login() {
        if(closed||closing)return;
        token=AccountSession.open(getApplicationContext());final long generation=token;
        pendingLogin=true;login.setEnabled(false);status.setText("正在打开账号登录…");
        worker.execute(() -> {
            try {EmbeddedNetwork.start(getApplicationContext(),generation);EmbeddedNetwork.call(generation,"login",new JSONObject());}
            catch(Exception|LinkageError ex) {post(generation,() -> {pendingLogin=false;status.setText("登录失败："+friendly(ex));});}
            finally {post(generation,() -> {login.setEnabled(true);refresh();});}
        });
    }
    private void connect(JSONObject device) {
        if(connecting||closing||!AccountSession.isCurrent(token))return;
        final long generation=token;connecting=true;ui.removeCallbacks(poll);
        worker.execute(() -> {
            try{Intent intent=AccountConnector.connect(this,device,generation,value->post(generation,()->status.setText(value)));post(generation,()->startActivity(intent));}
            catch(Exception|LinkageError ex){post(generation,()->status.setText("连接失败："+friendly(ex)));}
            finally{post(generation,()->{connecting=false;if(visible)ui.postDelayed(poll,5000);});}
        });
    }
    private void logout() {
        if(closing||closed)return;closing=true;pendingLogin=false;ui.removeCallbacks(poll);
        Future<String> result=EmbeddedNetwork.beginLogout(getApplicationContext());token=0;
        devices.removeAllViews();login.setEnabled(false);logout.setEnabled(false);status.setText("正在退出并移除此设备…");
        new Thread(() -> {
            String message;try {message=result.get();}catch(Exception ex){message=friendly(ex);}
            final String value=message;ui.post(() -> {closing=false;busy=false;if(closed)return;
                status.setText(value);login.setText("登录账号");login.setEnabled(true);logout.setEnabled(true);logout.setVisibility(View.GONE);
            });
        },"dd-uu-logout").start();
    }
    static String friendly(Throwable error) {return error.getMessage()==null?error.getClass().getSimpleName():error.getMessage();}
    static LinearLayout page(Activity a) {
        ScrollView scroll=new ScrollView(a);LinearLayout col=new LinearLayout(a);col.setOrientation(LinearLayout.VERTICAL);
        int p=(int)(24*a.getResources().getDisplayMetrics().density);col.setPadding(p,p,p,p);scroll.addView(col);a.setContentView(scroll);
        scroll.setOnApplyWindowInsetsListener((v,insets) -> {col.setPadding(p+insets.getSystemWindowInsetLeft(),p+insets.getSystemWindowInsetTop(),p+insets.getSystemWindowInsetRight(),p+insets.getSystemWindowInsetBottom());return insets;});
        return col;
    }
    static TextView text(LinearLayout parent,String value,int size) {TextView v=new TextView(parent.getContext());v.setText(value);v.setTextSize(size);v.setPadding(0,12,0,12);parent.addView(v);return v;}
    static void heading(LinearLayout parent,String value){text(parent,value,27);}
    static Button button(LinearLayout parent,String title,View.OnClickListener action) {Button b=new Button(parent.getContext());b.setText(title);b.setAllCaps(false);parent.addView(b,new LinearLayout.LayoutParams(-1,-2));b.setOnClickListener(action);return b;}
}
