package com.limelight.ddremote;

import java.net.URI;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.Map;

/** A copied host capability. Deliberately kept in memory only. */
public final class CompanionLink {
    public final String host;
    public final int port;
    public final String token;
    public final String fingerprint;

    private CompanionLink(String host, int port, String token, String fingerprint) {
        this.host = host;
        this.port = port;
        this.token = token;
        this.fingerprint = fingerprint;
    }

    public static CompanionLink parse(String value) {
        try {
            URI uri = new URI(value.trim());
            if (!"ddremote".equals(uri.getScheme()) || !"connect".equals(uri.getHost()) ||
                    uri.getRawUserInfo() != null || uri.getPort() != -1 || uri.getFragment() != null ||
                    (uri.getPath() != null && !uri.getPath().isEmpty() && !"/".equals(uri.getPath()))) {
                throw new IllegalArgumentException();
            }
            Map<String, String> params = new HashMap<>();
            if (uri.getRawQuery() == null) throw new IllegalArgumentException();
            for (String part : uri.getRawQuery().split("&")) {
                String[] pair = part.split("=", 2);
                if (pair.length != 2) throw new IllegalArgumentException();
                String key = URLDecoder.decode(pair[0], "UTF-8");
                String item = URLDecoder.decode(pair[1], "UTF-8");
                if (params.put(key, item) != null) throw new IllegalArgumentException();
            }
            String host = params.get("host");
            String token = params.get("token");
            String fingerprint = params.get("fingerprint");
            int port = Integer.parseInt(params.getOrDefault("port", "48200"));
            if (host == null || host.length() > 253 || !host.matches("[A-Za-z0-9.\\-:\\[\\]]+") ||
                    token == null || !token.matches("[A-Za-z0-9_-]{20,256}") ||
                    fingerprint == null || !fingerprint.matches("[A-Fa-f0-9]{64}") ||
                    port < 1 || port > 65535) throw new IllegalArgumentException();
            URI endpoint = new URI("https", null, host, port, "/", null, null);
            if (endpoint.getHost() == null) throw new IllegalArgumentException();
            return new CompanionLink(host, port, token, fingerprint.toLowerCase(java.util.Locale.ROOT));
        } catch (Exception ex) {
            throw new IllegalArgumentException("连接链接无效。请从电脑端重新复制完整的 DD远程连接链接。");
        }
    }

    public String authority() { return (host.contains(":") && !host.startsWith("[") ? "[" + host + "]" : host) + ":" + port; }
}
