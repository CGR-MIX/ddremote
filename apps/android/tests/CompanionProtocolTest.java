package com.limelight.ddremote;

import com.sun.net.httpserver.HttpsServer;
import com.sun.net.httpserver.HttpsConfigurator;
import java.io.*;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.*;
import java.security.cert.X509Certificate;
import java.util.concurrent.atomic.AtomicReference;
import javax.net.ssl.*;

/** Executes the production link parser and TLS client against a local HTTPS host. */
public final class CompanionProtocolTest {
    private static int assertions;
    private static void check(boolean yes, String label) {
        if (!yes) throw new AssertionError(label);
        assertions++;
        System.out.println("PASS " + label);
    }
    private static void invalid(String link) {
        try { CompanionLink.parse(link); throw new AssertionError("Unsafe link accepted"); }
        catch (IllegalArgumentException expected) { assertions++; }
    }
    public static void main(String[] args) throws Exception {
        char[] password = "local-test-only".toCharArray();
        KeyStore keys = KeyStore.getInstance("PKCS12");
        try (InputStream input = Files.newInputStream(Path.of(args[0]))) { keys.load(input, password); }
        KeyManagerFactory km = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
        km.init(keys, password);
        SSLContext tls = SSLContext.getInstance("TLS");
        tls.init(km.getKeyManagers(), null, null);
        X509Certificate certificate = (X509Certificate) keys.getCertificate("host");
        StringBuilder hex = new StringBuilder();
        for (byte b : MessageDigest.getInstance("SHA-256").digest(certificate.getEncoded())) hex.append(String.format("%02x", b));
        String token = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_test";
        AtomicReference<byte[]> file = new AtomicReference<>(new byte[0]);
        AtomicReference<String> clipboard = new AtomicReference<>("{\"text\":\"hello\"}");
        HttpsServer server = HttpsServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.setHttpsConfigurator(new HttpsConfigurator(tls));
        server.createContext("/api/", exchange -> {
            if (!("Bearer " + token).equals(exchange.getRequestHeaders().getFirst("Authorization"))) {
                exchange.sendResponseHeaders(401, -1); exchange.close(); return;
            }
            byte[] body;
            switch (exchange.getRequestURI().getPath()) {
                case "/api/files": body = "{\"files\":[]}".getBytes(StandardCharsets.UTF_8); break;
                case "/api/clipboard":
                    if (exchange.getRequestMethod().equals("POST")) clipboard.set(new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
                    body = clipboard.get().getBytes(StandardCharsets.UTF_8); break;
                case "/api/files/upload": file.set(exchange.getRequestBody().readAllBytes()); body = "{}".getBytes(); break;
                case "/api/files/download": body = file.get(); break;
                case "/api/redirect":
                    exchange.getResponseHeaders().set("Location", "https://example.invalid/steal");
                    exchange.sendResponseHeaders(302, -1); exchange.close(); return;
                default: exchange.sendResponseHeaders(404, -1); exchange.close(); return;
            }
            exchange.sendResponseHeaders(200, body.length);
            try (OutputStream output = exchange.getResponseBody()) { output.write(body); }
        });
        server.start();
        try {
            String uri = "ddremote://connect?host=127.0.0.1&port=" + server.getAddress().getPort() + "&token=" + token + "&fingerprint=" + hex;
            CompanionLink parsed = CompanionLink.parse(uri);
            check(parsed.host.equals("127.0.0.1") && parsed.port == server.getAddress().getPort(), "valid link parsed");
            invalid(uri.replace("ddremote://", "http://"));
            invalid(uri + "&host=evil.example");
            invalid(uri.replace("host=127.0.0.1", "host=evil.example%2Fpath"));
            invalid(uri.replace("host=127.0.0.1", "host=user%40evil.example"));
            invalid(uri + "#ignored");
            invalid(uri.replace("fingerprint=" + hex, "fingerprint=abc"));
            check(CompanionLink.parse(uri.replace("host=127.0.0.1", "host=%3A%3A1")).authority().startsWith("[::1]:"), "IPv6 authority is bracketed");
            CompanionClient client = new CompanionClient(parsed);
            check(client.getJson("/api/files").equals("{\"files\":[]}"), "pinned certificate and bearer authentication");
            client.postJson("/api/clipboard", "{\"text\":\"DD远程\"}");
            check(client.getJson("/api/clipboard").contains("DD远程"), "UTF-8 clipboard roundtrip");
            byte[] data = new byte[200000]; new SecureRandom().nextBytes(data);
            client.upload("测试 文件.bin", new ByteArrayInputStream(data), data.length);
            ByteArrayOutputStream output = new ByteArrayOutputStream(); client.download("测试 文件.bin", output);
            check(MessageDigest.isEqual(data, output.toByteArray()), "binary file roundtrip");
            try { client.getJson("/api/redirect"); throw new AssertionError("Redirect accepted"); }
            catch (IOException expected) { check(expected.getMessage().contains("302"), "redirect rejected"); }
            CompanionClient wrongPin = new CompanionClient(CompanionLink.parse(uri.replace(hex, "0".repeat(64))));
            try { wrongPin.getJson("/api/files"); throw new AssertionError("Wrong pin accepted"); }
            catch (SSLException expected) { check(true, "mismatched certificate rejected"); }
            CompanionClient wrongToken = new CompanionClient(CompanionLink.parse(uri.replace(token, "WRONG_WRONG_WRONG_WRONG")));
            try { wrongToken.getJson("/api/files"); throw new AssertionError("Wrong token accepted"); }
            catch (IOException expected) { check(expected.getMessage().contains("凭证"), "invalid bearer token rejected"); }
            System.out.println("PASS " + assertions + " assertions");
        } finally { server.stop(0); }
    }
}
