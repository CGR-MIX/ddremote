package com.limelight.ddremote;

import android.view.KeyEvent;
import com.limelight.binding.input.KeyboardTranslator;
import com.limelight.nvstream.NvConnection;
import com.limelight.nvstream.input.KeyboardPacket;
import java.util.LinkedHashSet;
import java.util.Set;

/** Maps UU Android keycodes to the protocol, with explicit release on teardown. */
public final class SunshineInput implements AutoCloseable {
    private final NvConnection conn;
    private final KeyboardTranslator translator=new KeyboardTranslator();
    private final Set<Integer> down=new LinkedHashSet<>();
    private boolean active=true;
    public SunshineInput(NvConnection conn){this.conn=conn;}
    public boolean supports(int code){return translator.translate(code,-1)!=0;}
    private byte modifiers(){return (byte)((down.contains(59)||down.contains(60)?1:0)|(down.contains(113)||down.contains(114)?2:0)|(down.contains(57)||down.contains(58)?4:0)|(down.contains(117)||down.contains(118)?8:0));}
    public synchronized void key(int code,boolean pressed){
        if(!active)return;short key=translator.translate(code,-1);if(key==0)return;
        if(pressed){if(!down.add(code))return;}else if(!down.remove(code))return;
        conn.sendKeyboardInput(key,pressed?KeyboardPacket.KEY_DOWN:KeyboardPacket.KEY_UP,modifiers(),(byte)0);
    }
    public synchronized void stroke(int code){if(!active||down.contains(code))return;key(code,true);key(code,false);}
    public synchronized void text(String text){if(active&&text!=null&&!text.isEmpty())conn.sendUtf8Text(text);}
    public synchronized void chord(int modifier,int code){boolean held=down.contains(modifier);if(!held)key(modifier,true);stroke(code);if(!held)key(modifier,false);}
    public synchronized void release(){for(Integer code:down.toArray(new Integer[0]))key(code,false);}
    @Override public synchronized void close(){release();active=false;}
}
