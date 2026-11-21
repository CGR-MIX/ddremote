package com.limelight.ddremote;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import com.limelight.BuildConfig;
import com.limelight.PcView;
import com.limelight.preferences.StreamSettings;
import org.json.JSONArray;
import org.json.JSONObject;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;

/** Account-first client; only DD machines owned by the signed-in identity appear. */
public final class DdHomeActivity extends Activity {
    private final ExecutorService worker = Executors.newSingleThreadExecutor();
    private final Handler ui = new Handler(Looper.getMainLooper());
    private TextView status;
    private LinearLayout deviceList;
    private Button login;
    private Button logoutButton;
    private boolean visible, busy, connecting, closed, pendingLogin, loggingOut;
    private long sessionToken;
    private String openedAuthUrl = "";
    private final Runnable poll = () -> refresh();

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        sessionToken = AccountSession.currentToken();
        LinearLayout col = DdUi.page(this, "DD远程", "登录同一账号，点开自己的电脑。\n桌面、游戏和文件，随时接续。");
        status = DdUi.text(this, "登录后即可连接自己的电脑。账号服务由 Tailscale 提供。", 15, DdUi.MUTED);
        DdUi.margin(this, col, status, 0, 14);
        login = DdUi.button(this, col, "登录账号", true, v -> login());
        logoutButton = DdUi.button(this,col,"退出并移除此设备",false,v -> logout());
        logoutButton.setVisibility(android.view.View.GONE);
        DdUi.margin(this,col,DdUi.text(this,"退出账号会移除此设备；彻底关闭程序后需重新登录。普通切到后台可继续保持连接。",13,DdUi.MUTED),0,12);
        DdUi.button(this, col, "刷新我的电脑", false, v -> refresh());
        DdUi.margin(this, col, DdUi.text(this, "我的电脑", 23, DdUi.INK), 22, 8);
        deviceList = new LinearLayout(this);
        deviceList.setOrientation(LinearLayout.VERTICAL);
        DdUi.margin(this, col, deviceList, 0, 12);
        deviceList.addView(DdUi.text(this, "登录后，自动显示同账号下已打开 DD远程的电脑。", 15, DdUi.MUTED));
        DdUi.button(this, col, "画质、触控与手柄", false, v -> startActivity(new Intent(this, StreamSettings.class)));
        DdUi.button(this, col, "高级连接", false, v -> new AlertDialog.Builder(this).setTitle("高级连接")
                .setItems(new String[]{"局域网电脑与游戏列表", "使用文件连接链接", "退出并移除此设备"}, (d, index) -> {
                    if (index == 0) startActivity(new Intent(this, PcView.class));
                    else if (index == 1) startActivity(new Intent(this, CompanionActivity.class));
                    else logout();
                }).show());
        DdUi.button(this, col, "关于与开源许可", false, v -> showAbout());
        DdUi.margin(this, col, DdUi.text(this, "DD远程 " + BuildConfig.VERSION_NAME + " · Android", 12, DdUi.MUTED), 12, 0);
    }

    @Override protected void onResume() { super.onResume(); visible = true; refresh(); }
    @Override protected void onPause() { visible = false; ui.removeCallbacks(poll); super.onPause(); }
    @Override protected void onDestroy() {
        closed = true; ui.removeCallbacks(poll); worker.shutdownNow();
        if (isFinishing() && isTaskRoot()) EmbeddedNetwork.finishSession(getApplicationContext());
        super.onDestroy();
    }

    private void refresh() {
        if (busy || closed || connecting || loggingOut || !visible || !AccountSession.isCurrent(sessionToken)) return;
        final long token = sessionToken;
        busy = true;
        ui.removeCallbacks(poll);
        worker.execute(() -> {
            try {
                EmbeddedNetwork.start(getApplicationContext(),token);
                JSONObject data = EmbeddedNetwork.call(token,"discover", new JSONObject());
                post(token,() -> render(data));
            } catch (Exception | LinkageError ex) { message(token,"连接失败：" + friendly(ex)); }
            finally { post(token,() -> { busy = false; if (visible && !connecting) ui.postDelayed(poll, 6000); }); }
        });
    }

    private void render(JSONObject data) {
        final long token = sessionToken;
        boolean running = data.optBoolean("running");
        login.setText(running ? "账号已连接" : "登录账号");
        login.setEnabled(!running);
        logoutButton.setVisibility(running ? android.view.View.VISIBLE : android.view.View.GONE);
        String account = data.optString("account");
        String state = data.optString("state");
        status.setText(running ? "已登录 " + account :
                ("NeedsMachineAuth".equals(state) ? "请在账号管理页面批准此设备，然后刷新。" : "登录后即可连接自己的电脑。账号服务由 Tailscale 提供。"));
        deviceList.removeAllViews();
        JSONArray devices = data.optJSONArray("devices");
        if (devices == null || devices.length() == 0) {
            deviceList.addView(DdUi.text(this, running ? "暂未找到在线电脑。请在电脑打开 DD远程，登录相同账号并开始桌面共享。" : "登录后自动发现电脑，无需输入 IP。", 15, DdUi.MUTED));
        } else for (int i=0;i<devices.length();i++) {
            JSONObject device = devices.optJSONObject(i);
            if (device == null) continue;
            Button connect = DdUi.button(this, deviceList, device.optString("name") + "\n" +
                    (device.optBoolean("ready") ? "点击连接桌面" : "电脑尚未开启桌面共享"), true, v -> connect(device,token));
            connect.setEnabled(device.optBoolean("ready"));
            if (device.optBoolean("ready")) DdUi.button(this, deviceList, "文件与剪贴板 · " + device.optString("name"), false, v -> companion(device,token));
        }
        String auth = data.optString("authUrl");
        if (pendingLogin && !auth.isEmpty() && !auth.equals(openedAuthUrl)) {
            Uri uri = Uri.parse(auth);
            String host = uri.getHost();
            if ("https".equals(uri.getScheme()) && host != null && (host.equals("tailscale.com") || host.endsWith(".tailscale.com"))) {
                openedAuthUrl = auth;
                pendingLogin = false;
                try { startActivity(new Intent(Intent.ACTION_VIEW, uri)); }
                catch (android.content.ActivityNotFoundException ex) { status.setText("此设备没有可用浏览器，无法打开登录页面。"); }
            } else status.setText("登录服务返回了无效地址。");
        }
    }

    private void login() {
        if (connecting || closed || loggingOut) return;
        try {sessionToken = AccountSession.open(getApplicationContext());}
        catch (IllegalStateException ex) {status.setText(friendly(ex));return;}
        final long token = sessionToken;
        pendingLogin = true;
        openedAuthUrl = "";
        login.setEnabled(false);
        status.setText("正在打开账号登录…");
        worker.execute(() -> {
            try {
                EmbeddedNetwork.start(getApplicationContext(),token);
                EmbeddedNetwork.call(token,"login", new JSONObject());
            } catch (Exception | LinkageError ex) { post(token,() -> {pendingLogin=false;status.setText("登录失败：" + friendly(ex));}); }
            finally { post(token,() -> {login.setEnabled(true);refresh();}); }
        });
    }

    private void logout() {
        if (loggingOut || closed) return;
        loggingOut = true;
        Future<String> result = EmbeddedNetwork.beginLogout(getApplicationContext());
        sessionToken = 0;
        connecting = false;
        busy = false;
        pendingLogin = false;
        openedAuthUrl = "";
        ui.removeCallbacks(poll);
        login.setEnabled(false);
        status.setText("正在退出并移除此设备…");
        logoutButton.setEnabled(false);
        deviceList.removeAllViews();
        // Never queue logout behind pairing/discovery on the single I/O worker.
        new Thread(() -> {
            String message;
            try {message=result.get();} catch(Exception ex) {message=friendly(ex);}
            final String finalMessage=message;
            ui.post(() -> {
                loggingOut=false;
                if (!closed) {status.setText(finalMessage);login.setEnabled(true);login.setText("登录账号");logoutButton.setEnabled(true);logoutButton.setVisibility(android.view.View.GONE);}
            });
        },"dd-logout-result").start();
    }

    private void connect(JSONObject device,long token) {
        if (connecting || loggingOut || !AccountSession.isCurrent(token)) return;
        connecting = true;
        ui.removeCallbacks(poll);
        worker.execute(() -> {
            try {
                Intent intent = AccountConnector.connect(this, device, token, value -> message(token,value));
                post(token,() -> startActivity(intent));
            } catch (Exception ex) { message(token,"连接失败：" + friendly(ex)); }
            finally { post(token,() -> {connecting=false;if (visible) ui.postDelayed(poll,6000);}); }
        });
    }

    private void companion(JSONObject device,long token) {
        if (connecting || loggingOut || !AccountSession.isCurrent(token)) return;
        connecting = true;
        message(token,"正在打开文件连接…");
        worker.execute(() -> {
            try {
                EmbeddedNetwork.call(token,"prepare", new JSONObject().put("ip", device.getString("ip")).put("id", device.getString("id")));
                JSONObject response = EmbeddedNetwork.request(token,device.getString("ip"), "/v1/companion", null);
                Uri original = Uri.parse(response.getString("link"));
                Uri.Builder local = original.buildUpon().clearQuery();
                for (String key : original.getQueryParameterNames()) local.appendQueryParameter(key, "host".equals(key) ? "127.0.0.2" : original.getQueryParameter(key));
                Intent intent = new Intent(this, CompanionActivity.class).setData(local.build()).putExtra("DdAutoConnect", true).putExtra("DdAccountGeneration",token);
                post(token,() -> startActivity(intent));
            } catch (Exception ex) { message(token,"文件连接失败：" + friendly(ex)); }
            finally { post(token,() -> {connecting=false;}); }
        });
    }

    private void post(long token,Runnable action) {ui.post(() -> {if (!closed) AccountSession.runIfCurrent(token,action);});}
    private void message(long token,String message) {post(token,() -> status.setText(message));}
    private String friendly(Throwable ex) {
        Throwable root = ex.getCause() == null ? ex : ex.getCause();
        return root.getMessage() == null ? "请检查网络后重试。" : root.getMessage();
    }

    private void showAbout() {
        String license = "DD远程 " + BuildConfig.VERSION_NAME + "\n\n" +
                "Android 串流基于 Moonlight Android，协议兼容 Sunshine。\n" +
                "账号连接内嵌 Tailscale tsnet 1.104.7，无需安装外部 VPN。\n" +
                "Moonlight Android 上游提交：b48494cb96bff23d8886c4775cc4f39a1075495d\n" +
                "原作者：Cameron Gutman、Diego Waxemberg、Aaron Neyer、Andrew Hennessy 及社区贡献者。\n" +
                "DD远程界面、账号发现、文件传输与剪贴板为本项目新增功能。\n" +
                "项目采用 GPLv3；随交付源码提供上游版权与依赖许可。\n\n";
        license += readAsset("third-party-notices.txt") + "\n\n" + readAsset("moonlight-license.txt");
        TextView text = DdUi.text(this, license, 13, DdUi.INK);
        text.setPadding(DdUi.dp(this,20),DdUi.dp(this,12),DdUi.dp(this,20),DdUi.dp(this,12));
        text.setTextIsSelectable(true);
        ScrollView scroll = new ScrollView(this); scroll.addView(text);
        new AlertDialog.Builder(this).setTitle("关于 DD远程").setView(scroll).setPositiveButton("关闭",null).show();
    }

    private String readAsset(String name) {
        try (InputStream input = getAssets().open(name)) {
            ByteArrayOutputStream out = new ByteArrayOutputStream(); byte[] buffer = new byte[8192]; int n;
            while ((n=input.read(buffer))!=-1) out.write(buffer,0,n);
            return new String(out.toByteArray(), StandardCharsets.UTF_8);
        } catch (Exception ignored) { return "请查看交付源码中的许可声明。"; }
    }
}
