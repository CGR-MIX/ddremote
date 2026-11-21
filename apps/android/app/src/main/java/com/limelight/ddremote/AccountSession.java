package com.limelight.ddremote;

import android.content.Context;
import android.content.ContextWrapper;
import com.limelight.binding.PlatformBinding;
import com.limelight.nvstream.http.LimelightCryptoProvider;
import java.io.File;
import java.security.SecureRandom;
import java.security.PrivateKey;
import java.security.cert.X509Certificate;
import android.util.Base64;

/** DD account credentials are isolated from manually added LAN computers. */
public final class AccountSession {
    private static final SessionGate gate = new SessionGate();
    private static String uniqueId;

    static long open(Context context) { synchronized (gate) {
        long token = gate.open();
        if (uniqueId != null) return token;
        clearFiles(context);
        byte[] value = new byte[8]; new SecureRandom().nextBytes(value);
        StringBuilder id = new StringBuilder();
        for (byte b : value) id.append(String.format(java.util.Locale.ROOT,"%02x",b & 255));
        uniqueId = id.toString();
        return token;
    } }

    static String uniqueId(long token) { synchronized(gate) {gate.require(token); return uniqueId;} }
    public static boolean isCurrent(long token) {return gate.isCurrent(token);}
    static long currentToken() {return gate.currentToken();}
    static void require(long token) {gate.require(token);}
    static boolean runIfCurrent(long token,Runnable action) {return gate.runIfCurrent(token,action);}
    static long invalidate() {return gate.invalidate();}

    public static LimelightCryptoProvider cryptoProvider(Context context,long token) { synchronized(gate) {
        gate.require(token);
        File directory = new File(context.getNoBackupFilesDir(),"dd-account-pairing");
        if (!directory.isDirectory() && !directory.mkdirs()) throw new IllegalStateException("无法创建账号配对目录");
        LimelightCryptoProvider delegate = PlatformBinding.getCryptoProvider(new ContextWrapper(context.getApplicationContext()) {
            @Override public File getFilesDir() {return directory;}
        });
        // Lazy upstream key generation also executes under the same guard, so
        // an old request can never recreate credentials after logout clears them.
        return new LimelightCryptoProvider() {
            public X509Certificate getClientCertificate() {synchronized(gate){gate.require(token);return delegate.getClientCertificate();}}
            public PrivateKey getClientPrivateKey() {synchronized(gate){gate.require(token);return delegate.getClientPrivateKey();}}
            public byte[] getPemEncodedClientCertificate() {synchronized(gate){gate.require(token);return delegate.getPemEncodedClientCertificate();}}
            public String encodeBase64String(byte[] data) {synchronized(gate){gate.require(token);return delegate.encodeBase64String(data);}}
        };
    } }

    static void saveCertificate(Context context,long token,String id,X509Certificate certificate) throws java.security.cert.CertificateEncodingException {
        synchronized(gate) {
            gate.require(token);
            context.getSharedPreferences("dd_pairs",Context.MODE_PRIVATE).edit()
                    .putString(id,Base64.encodeToString(certificate.getEncoded(),Base64.NO_WRAP)).commit();
        }
    }

    private static void clearFiles(Context context) {
        context.getSharedPreferences("dd_pairs",Context.MODE_PRIVATE).edit().clear().commit();
        File directory = new File(context.getNoBackupFilesDir(),"dd-account-pairing");
        new File(directory,"client.crt").delete();
        new File(directory,"client.key").delete();
        uniqueId = null;
    }
    static void finishLogout(Context context) {synchronized(gate){clearFiles(context);gate.closed();}}
}
