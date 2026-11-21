package com.limelight.ddremote;

import android.app.Service;
import android.content.Intent;
import android.os.IBinder;

/** Best-effort logout when the user explicitly removes the Android task. */
public final class NetworkLifecycleService extends Service {
    @Override public IBinder onBind(Intent intent) {return null;}
    @Override public int onStartCommand(Intent intent,int flags,int startId) {return START_NOT_STICKY;}
    @Override public void onTaskRemoved(Intent rootIntent) {
        EmbeddedNetwork.finishSession(getApplicationContext());
        stopSelf();
    }
    // Background service limits can call onDestroy without user intent.
    // Deliberately keep the in-process network session in that case.
}
