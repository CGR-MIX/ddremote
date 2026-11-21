package com.limelight.ddremote;

import android.content.Context;
import android.os.Build;
import android.content.Intent;
import org.json.JSONObject;
import java.io.File;
import java.io.IOException;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.security.KeyStore;
import java.security.cert.X509Certificate;
import javax.net.ssl.TrustManager;
import javax.net.ssl.TrustManagerFactory;
import javax.net.ssl.X509TrustManager;
import android.util.Base64;
import java.util.concurrent.Future;
import java.util.concurrent.FutureTask;

/** In-process userspace WireGuard node. Does not install a system VPN. */
public final class EmbeddedNetwork {
    private static volatile boolean started;
    private static long startedToken;
    private static final Object logoutLock = new Object();
    private static FutureTask<String> logoutTask;
    static { System.loadLibrary("ddnetwork"); }
    private static native byte[] nativeCall(byte[] method, byte[] payload);

    public static synchronized void start(Context context,long token) throws Exception {
        AccountSession.require(token);
        if (started && startedToken == token) return;
        File dir = new File(context.getNoBackupFilesDir(), "ddnetwork");
        JSONObject input = AndroidNetworkState.snapshot(context);
        input.put("dir", dir.getAbsolutePath());
        // New Android releases can move system roots into a Conscrypt APEX.
        // Export the platform's actual trusted roots for Go's standard verifier.
        File roots = new File(context.getNoBackupFilesDir(), "ddnetwork-roots.pem");
        TrustManagerFactory factory = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
        factory.init((KeyStore) null);
        try (FileOutputStream output = new FileOutputStream(roots)) {
            for (TrustManager trust : factory.getTrustManagers()) if (trust instanceof X509TrustManager) {
                for (X509Certificate certificate : ((X509TrustManager) trust).getAcceptedIssuers()) {
                    String pem = "-----BEGIN CERTIFICATE-----\n" + Base64.encodeToString(certificate.getEncoded(), Base64.NO_WRAP) +
                            "\n-----END CERTIFICATE-----\n";
                    output.write(pem.getBytes(StandardCharsets.US_ASCII));
                }
            }
        }
        if (roots.length() == 0) throw new IOException("系统没有可用的信任证书");
        input.put("caBundle", roots.getAbsolutePath());
        String host = ("dd-android-" + Build.MODEL).toLowerCase(java.util.Locale.ROOT).replaceAll("[^a-z0-9-]", "-");
        input.put("hostname", host.substring(0, Math.min(host.length(), 50)));
        call(token,"start", input);
        if (!AccountSession.runIfCurrent(token,() -> {
            started = true;
            startedToken = token;
            AndroidNetworkState.register(context);
            try {context.startService(new Intent(context,NetworkLifecycleService.class));}
            catch (IllegalStateException ignored) { }
        })) throw new IllegalStateException("账号会话已结束");
    }

    public static JSONObject call(String method, JSONObject payload) throws Exception {
        byte[] output = nativeCall(method.getBytes(StandardCharsets.UTF_8), payload.toString().getBytes(StandardCharsets.UTF_8));
        if (output == null) throw new IOException("安全网络内存不足，请重新打开应用");
        JSONObject result = new JSONObject(new String(output, StandardCharsets.UTF_8));
        if (result.has("diagnostic")) android.util.Log.e("DDNetwork", result.getString("diagnostic"));
        if (result.has("error")) throw new IOException(result.getString("error"));
        return result;
    }

    static JSONObject call(long token,String method,JSONObject payload) throws Exception {
        AccountSession.require(token);
        JSONObject result = call(method,payload.put("sessionToken",token));
        AccountSession.require(token);
        return result;
    }

    static JSONObject request(long token,String ip, String path, JSONObject body) throws Exception {
        JSONObject payload = new JSONObject().put("ip", ip).put("path", path).put("body", body == null ? "" : body.toString());
        return new JSONObject(call(token,"request", payload).getString("body"));
    }

    static Future<String> beginLogout(Context context) {
        synchronized(logoutLock) {
            if (logoutTask != null && !logoutTask.isDone()) return logoutTask;
            long invalidated = AccountSession.invalidate(); // Synchronous: queued UI callbacks are stale now.
            Context app = context.getApplicationContext();
            logoutTask = new FutureTask<>(() -> {
                try {
                    call("logout",new JSONObject().put("sessionToken",invalidated));
                    return "已退出账号，此设备已从账号列表移除。";
                } catch(Exception ex) {
                    return ex.getMessage() == null ? "已断开连接；未确认云端移除，将待离线清理" : ex.getMessage();
                } finally {
                    started=false;
                    AccountSession.finishLogout(app);
                    app.stopService(new Intent(app,NetworkLifecycleService.class));
                }
            });
            new Thread(logoutTask,"dd-account-logout").start();
            return logoutTask;
        }
    }

    static void finishSession(Context context) {
        beginLogout(context);
    }
}
