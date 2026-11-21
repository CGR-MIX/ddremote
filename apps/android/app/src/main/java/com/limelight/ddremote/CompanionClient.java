package com.limelight.ddremote;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.URL;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.cert.CertificateException;
import java.security.cert.X509Certificate;

import javax.net.ssl.HttpsURLConnection;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLSession;
import javax.net.ssl.TrustManager;
import javax.net.ssl.X509TrustManager;

/** HTTPS capability client. Every request and redirect is bound to the copied certificate pin. */
final class CompanionClient {
    private final CompanionLink link;
    private final SSLContext tls;

    CompanionClient(CompanionLink link) throws Exception {
        this.link = link;
        tls = SSLContext.getInstance("TLS");
        tls.init(null, new TrustManager[] { new X509TrustManager() {
            @Override public void checkClientTrusted(X509Certificate[] chain, String auth) throws CertificateException {
                throw new CertificateException("Client certificates are not used");
            }
            @Override public void checkServerTrusted(X509Certificate[] chain, String auth) throws CertificateException {
                if (chain == null || chain.length == 0 || !matches(chain[0])) throw new CertificateException("电脑证书指纹不匹配");
                chain[0].checkValidity();
            }
            @Override public X509Certificate[] getAcceptedIssuers() { return new X509Certificate[0]; }
        } }, null);
    }

    private boolean matches(X509Certificate cert) {
        try {
            byte[] actual = MessageDigest.getInstance("SHA-256").digest(cert.getEncoded());
            byte[] expected = new byte[32];
            for (int i = 0; i < expected.length; i++) expected[i] = (byte) Integer.parseInt(link.fingerprint.substring(i * 2, i * 2 + 2), 16);
            return MessageDigest.isEqual(actual, expected);
        } catch (Exception ex) { return false; }
    }

    private boolean verifyPinnedHost(String host, SSLSession session) {
        try {
            String expected = link.host.replace("[", "").replace("]", "");
            return host.replace("[", "").replace("]", "").equalsIgnoreCase(expected) &&
                    matches((X509Certificate) session.getPeerCertificates()[0]);
        } catch (Exception ex) { return false; }
    }

    private HttpsURLConnection open(String path, String method) throws IOException {
        if (!path.startsWith("/api/") || path.startsWith("//")) throw new IOException("无效请求路径");
        HttpsURLConnection conn = (HttpsURLConnection) new URL("https://" + link.authority() + path).openConnection();
        conn.setSSLSocketFactory(tls.getSocketFactory());
        // The explicit certificate pin authenticates the host, including a self-signed LAN IP.
        conn.setHostnameVerifier(this::verifyPinnedHost);
        conn.setInstanceFollowRedirects(false);
        conn.setConnectTimeout(10000);
        conn.setReadTimeout(60000);
        conn.setRequestMethod(method);
        conn.setRequestProperty("Authorization", "Bearer " + link.token);
        conn.setRequestProperty("Accept", "application/json");
        conn.setUseCaches(false);
        return conn;
    }

    private static void requireSuccess(HttpsURLConnection conn) throws IOException {
        int code = conn.getResponseCode();
        if (code == 401 || code == 403) throw new IOException("连接凭证已失效，请在电脑端重新复制连接链接。");
        if (code < 200 || code > 299) throw new IOException("电脑返回错误：HTTP " + code);
    }

    String getJson(String path) throws IOException {
        HttpsURLConnection conn = open(path, "GET");
        try {
            requireSuccess(conn);
            try (InputStream in = conn.getInputStream(); ByteArrayOutputStream out = new ByteArrayOutputStream()) {
                byte[] buffer = new byte[8192];
                int count;
                while ((count = in.read(buffer)) != -1) {
                    if (out.size() + count > 2 * 1024 * 1024) throw new IOException("响应过大");
                    out.write(buffer, 0, count);
                }
                return new String(out.toByteArray(), StandardCharsets.UTF_8);
            }
        } finally { conn.disconnect(); }
    }

    void postJson(String path, String json) throws IOException {
        HttpsURLConnection conn = open(path, "POST");
        byte[] bytes = json.getBytes(StandardCharsets.UTF_8);
        conn.setDoOutput(true);
        conn.setRequestProperty("Content-Type", "application/json; charset=utf-8");
        conn.setFixedLengthStreamingMode(bytes.length);
        try {
            try (OutputStream out = conn.getOutputStream()) { out.write(bytes); }
            requireSuccess(conn);
        } finally { conn.disconnect(); }
    }

    void upload(String name, InputStream input, long length) throws IOException {
        HttpsURLConnection conn = open("/api/files/upload?name=" + URLEncoder.encode(name, "UTF-8"), "POST");
        conn.setDoOutput(true);
        conn.setRequestProperty("Content-Type", "application/octet-stream");
        if (length >= 0) conn.setFixedLengthStreamingMode(length); else conn.setChunkedStreamingMode(65536);
        try {
            try (OutputStream out = conn.getOutputStream()) { copy(input, out); }
            requireSuccess(conn);
        } finally { conn.disconnect(); }
    }

    void download(String name, OutputStream output) throws IOException {
        HttpsURLConnection conn = open("/api/files/download?name=" + URLEncoder.encode(name, "UTF-8"), "GET");
        try {
            requireSuccess(conn);
            try (InputStream in = conn.getInputStream()) { copy(in, output); }
        } finally { conn.disconnect(); }
    }

    private static void copy(InputStream input, OutputStream output) throws IOException {
        byte[] buffer = new byte[65536];
        int n;
        while ((n = input.read(buffer)) != -1) {
            if (Thread.currentThread().isInterrupted()) throw new IOException("传输已取消");
            output.write(buffer, 0, n);
        }
    }
}
