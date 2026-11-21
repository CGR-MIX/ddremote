package com.limelight.ddremote;
import android.content.Context;
import com.limelight.binding.audio.AndroidAudioRenderer;
import java.util.Arrays;
import java.util.concurrent.atomic.AtomicLong;
/** Keeps host audio active while allowing a local remote-audio mute control. */
final class StreamAudio extends AndroidAudioRenderer {
 final AtomicLong packets=new AtomicLong(),nonSilentPackets=new AtomicLong(),mutedPackets=new AtomicLong();
 volatile boolean muted;
 StreamAudio(Context context,boolean muted){super(context,false);this.muted=muted;}
 @Override public void playDecodedAudio(short[] samples){packets.incrementAndGet();for(short sample:samples){if(sample!=0){nonSilentPackets.incrementAndGet();break;}}if(muted){Arrays.fill(samples,(short)0);mutedPackets.incrementAndGet();}super.playDecodedAudio(samples);}
}
