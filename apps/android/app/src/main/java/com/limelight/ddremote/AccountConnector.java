package com.limelight.ddremote;

import android.app.Activity;
import android.content.Intent;
import android.content.SharedPreferences;
import android.os.Build;
import android.util.Base64;
import com.limelight.Game;
import com.limelight.nvstream.http.ComputerDetails;
import com.limelight.nvstream.http.NvApp;
import com.limelight.nvstream.http.NvHTTP;
import com.limelight.nvstream.http.PairingManager;
import org.json.JSONObject;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.security.SecureRandom;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;

final class AccountConnector {
    interface Progress { void show(String message); }

    static Intent connect(Activity activity, JSONObject device, long token, Progress progress) throws Exception {
        AccountSession.require(token);
        progress.show("正在建立加密连接…");
        JSONObject prepared = EmbeddedNetwork.call(token,"prepare", new JSONObject().put("ip", device.getString("ip"))
                .put("id", device.getString("id")));
        String host = prepared.getString("host");
        String advertised = prepared.optString("streamCert");
        if (advertised.isEmpty()) throw new IOException("电脑未提供串流身份证书，请更新 DD远程电脑端。");
        X509Certificate expectedCert = (X509Certificate) CertificateFactory.getInstance("X.509").generateCertificate(
                new ByteArrayInputStream(advertised.getBytes(StandardCharsets.US_ASCII)));
        String uniqueId = AccountSession.uniqueId(token);
        SharedPreferences prefs = activity.getSharedPreferences("dd_pairs", Activity.MODE_PRIVATE);
        String saved = prefs.getString(device.getString("id"), "");
        X509Certificate cert = null;
        if (!saved.isEmpty()) {
            cert = (X509Certificate) CertificateFactory.getInstance("X.509").generateCertificate(
                    new ByteArrayInputStream(Base64.decode(saved, Base64.NO_WRAP)));
        }
        // The account endpoint authenticates the current device's public cert.
        // A reinstalled host can therefore re-pair without trusting LAN data.
        if (cert == null || !cert.equals(expectedCert)) cert = expectedCert;
        NvHTTP http = new NvHTTP(new ComputerDetails.AddressTuple(host, 47989), 47984, uniqueId, cert,
                AccountSession.cryptoProvider(activity,token));
        String serverInfo = http.getServerInfo(true);
        AccountSession.require(token);
        if (cert == null || http.getPairState(serverInfo) != PairingManager.PairState.PAIRED) {
            progress.show("正在验证账号并自动配对…");
            byte[] random = new byte[16]; new SecureRandom().nextBytes(random);
            StringBuilder nonce = new StringBuilder(32);
            for (byte value : random) nonce.append(String.format(java.util.Locale.ROOT, "%02x", value & 255));
            String pin = PairingManager.generatePinString();
            http.setAccountPairingNonce(nonce.toString());
            JSONObject request = new JSONObject().put("nonce", nonce.toString()).put("pin", pin)
                    .put("name", "DD Android · " + Build.MODEL);
            ExecutorService pairingWorker = Executors.newSingleThreadExecutor();
            Future<JSONObject> approval = pairingWorker.submit(() ->
                    EmbeddedNetwork.request(token,device.getString("ip"), "/v1/pair", request));
            try {
                PairingManager pairing = http.getPairingManager();
                PairingManager.PairState result = pairing.pair(serverInfo, pin);
                approval.get(80, TimeUnit.SECONDS);
                AccountSession.require(token);
                if (result != PairingManager.PairState.PAIRED) throw new IOException("自动配对未完成，请重试 (" + result + ")");
                cert = pairing.getPairedCert();
                if (!expectedCert.equals(cert)) throw new IOException("串流证书与账号设备身份不一致，已拒绝连接。");
                AccountSession.saveCertificate(activity,token,device.getString("id"),cert);
            } finally { pairingWorker.shutdownNow(); }
        }
        progress.show("正在打开电脑桌面…");
        ComputerDetails computer = http.getComputerDetails(true);
        AccountSession.saveCertificate(activity,token,device.getString("id"),cert);
        NvApp desktop = null;
        for (NvApp app : http.getAppList()) {
            if ("Desktop".equalsIgnoreCase(app.getAppName()) || "桌面".equals(app.getAppName())) { desktop = app; break; }
        }
        if (desktop == null) throw new IOException("电脑没有发布桌面，请在 DD远程电脑端重新启动共享。");
        AccountSession.require(token);
        Intent intent = new Intent(activity, Game.class);
        intent.putExtra(Game.EXTRA_HOST, host);
        intent.putExtra(Game.EXTRA_PORT, 47989);
        intent.putExtra(Game.EXTRA_HTTPS_PORT, 47984);
        intent.putExtra(Game.EXTRA_APP_NAME, desktop.getAppName());
        intent.putExtra(Game.EXTRA_APP_ID, desktop.getAppId());
        intent.putExtra(Game.EXTRA_APP_HDR, desktop.isHdrSupported());
        intent.putExtra(Game.EXTRA_UNIQUEID, uniqueId);
        intent.putExtra(Game.EXTRA_PC_UUID, computer.uuid);
        intent.putExtra(Game.EXTRA_PC_NAME, device.getString("name"));
        intent.putExtra(Game.EXTRA_SERVER_CERT, cert.getEncoded());
        intent.putExtra("DdEmbeddedNetwork", true);
        intent.putExtra("DdAccountGeneration", token);
        return intent;
    }
}
