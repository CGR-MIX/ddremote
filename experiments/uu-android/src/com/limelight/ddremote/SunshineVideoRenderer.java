package com.limelight.ddremote;

import android.media.MediaCodec;
import android.media.MediaFormat;
import android.os.Build;
import android.os.SystemClock;
import android.view.Surface;
import com.limelight.nvstream.av.video.VideoDecoderRenderer;
import com.limelight.nvstream.jni.MoonBridge;
import java.io.ByteArrayOutputStream;
import java.nio.ByteBuffer;
import java.util.concurrent.atomic.AtomicLong;

/** H.264 decoder for the original UU SurfaceView, fed by the GameStream adapter. */
public final class SunshineVideoRenderer extends VideoDecoderRenderer {
    private final Surface surface;
    private MediaCodec codec;
    private volatile boolean stopping;
    private Thread output;
    private final ByteArrayOutputStream config=new ByteArrayOutputStream();
    private boolean configured;
    public final AtomicLong frames=new AtomicLong(), rendered=new AtomicLong();
    public SunshineVideoRenderer(Surface surface){this.surface=surface;}
    @Override public int setup(int format,int width,int height,int fps) {
        if(format!=MoonBridge.VIDEO_FORMAT_H264||width<1||height<1||!surface.isValid())return -1;
        try {
            codec=MediaCodec.createDecoderByType("video/avc");
            MediaFormat media=MediaFormat.createVideoFormat("video/avc",width,height);
            media.setInteger(MediaFormat.KEY_MAX_INPUT_SIZE,Math.max(2*1024*1024,width*height));
            media.setInteger(MediaFormat.KEY_FRAME_RATE,fps);
            if(Build.VERSION.SDK_INT>=30)media.setInteger(MediaFormat.KEY_LOW_LATENCY,1);
            codec.configure(media,surface,null,0);codec.start();
            android.util.Log.i("DDSunshineVideo","decoder="+codec.getName()+" "+width+"x"+height+"@"+fps);
            return 0;
        }catch(Exception ex){android.util.Log.e("DDSunshineVideo","setup failed: "+ex.getClass().getSimpleName());cleanup();return -1;}
    }
    @Override public void start(){
        stopping=false;output=new Thread(() -> {
            MediaCodec.BufferInfo info=new MediaCodec.BufferInfo();
            while(!stopping)try {
                int index=codec.dequeueOutputBuffer(info,10000);
                if(index>=0){codec.releaseOutputBuffer(index,true);rendered.incrementAndGet();}
            } catch(IllegalStateException ex){if(!stopping&&surface.isValid())android.util.Log.e("DDSunshineVideo","decoder output stopped");break;}
        },"dd-uu-video-output");output.start();
    }
    private boolean queue(byte[] data,int length,int flags) {
        if(stopping||codec==null)return false;
        int index=codec.dequeueInputBuffer(20000);
        if(index<0)return false;
        ByteBuffer buffer=codec.getInputBuffer(index);
        if(buffer==null||buffer.capacity()<length){codec.queueInputBuffer(index,0,0,0,0);return false;}
        buffer.clear();buffer.put(data,0,length);
        codec.queueInputBuffer(index,0,length,SystemClock.elapsedRealtimeNanos()/1000,flags);return true;
    }
    @Override public int submitDecodeUnit(byte[] data,int length,int type,int frame,int frameType,char latency,long received,long enqueued) {
        if(stopping)return MoonBridge.DR_OK;
        try {
            if(type!=MoonBridge.BUFFER_TYPE_PICDATA) {
                if(!configured){if(config.size()+length>65536)return MoonBridge.DR_NEED_IDR;config.write(data,0,length);}
                return MoonBridge.DR_OK;
            }
            if(!configured){byte[] headers=config.toByteArray();if(headers.length==0||!queue(headers,headers.length,MediaCodec.BUFFER_FLAG_CODEC_CONFIG))return MoonBridge.DR_NEED_IDR;configured=true;config.reset();}
            if(!queue(data,length,frameType==MoonBridge.FRAME_TYPE_IDR?MediaCodec.BUFFER_FLAG_KEY_FRAME:0))return MoonBridge.DR_NEED_IDR;
            frames.incrementAndGet();return MoonBridge.DR_OK;
        } catch(IllegalStateException ex){return MoonBridge.DR_NEED_IDR;}
    }
    @Override public void stop(){stopping=true;if(output!=null&&output!=Thread.currentThread())try{output.join(1000);}catch(InterruptedException ex){Thread.currentThread().interrupt();}}
    @Override public void cleanup(){stop();if(codec!=null){try{codec.stop();}catch(Exception ignored){}try{codec.release();}catch(Exception ignored){}codec=null;}}
    @Override public int getCapabilities(){return MoonBridge.CAPABILITY_DIRECT_SUBMIT;}
    @Override public void setHdrMode(boolean enabled,byte[] metadata){}
}
