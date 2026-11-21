package com.limelight.ddremote;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.security.SecureRandom;

/** Cross-language test of production Android network code against DD Remote's Windows host. */
public final class WindowsCompanionInteropTest {
    private static void require(boolean value, String label) {
        if (!value) throw new AssertionError(label);
        System.out.println("PASS " + label);
    }

    public static void main(String[] args) throws Exception {
        if (args.length != 1) throw new IllegalArgumentException("Expected a UTF-8 local connection-link file");
        String value = Files.readString(Path.of(args[0]), StandardCharsets.UTF_8).replace("\uFEFF", "").trim();
        CompanionLink link = CompanionLink.parse(value);
        CompanionClient client = new CompanionClient(link);
        String firstList = client.getJson("/api/files");
        require(firstList.contains("\"files\""), "Windows file-list API and pinned TLS accepted");
        byte[] payload = new byte[200000];
        new SecureRandom().nextBytes(payload);
        String name = "ddremote-android-interop-测试.bin";
        client.upload(name, new ByteArrayInputStream(payload), payload.length);
        require(client.getJson("/api/files").contains(name), "UTF-8 filename present after Android-to-Windows upload");
        ByteArrayOutputStream result = new ByteArrayOutputStream();
        client.download(name, result);
        require(MessageDigest.isEqual(payload, result.toByteArray()), "Windows-to-Android 200 KB file bytes match");
        // Reading is deliberately non-mutating: do not change the user's current clipboard.
        String clipboard = client.getJson("/api/clipboard");
        require(clipboard.contains("\"text\""), "Windows clipboard read API responds (content not printed)");
        CompanionLink mismatched = CompanionLink.parse(value.replace(link.fingerprint, "0".repeat(64)));
        try {
            new CompanionClient(mismatched).getJson("/api/files");
            throw new AssertionError("Mismatched Windows certificate was accepted");
        } catch (javax.net.ssl.SSLException expected) {
            require(true, "Wrong Windows certificate pin rejected");
        }
        System.out.println("PASS 5 Windows / Android interoperability checks");
    }
}
