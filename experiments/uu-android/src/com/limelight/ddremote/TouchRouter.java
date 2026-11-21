package com.limelight.ddremote;

import android.os.Handler;
import android.os.Looper;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewConfiguration;
import com.limelight.nvstream.NvConnection;
import com.limelight.nvstream.input.MouseButtonPacket;

/** One-finger pointer/trackpad, long press drag, two-finger right click and scrolling. */
final class TouchRouter implements View.OnTouchListener,AutoCloseable {
    private final NvConnection conn;
    private final Handler ui=new Handler(Looper.getMainLooper());
    private final float slop;
    private final View surface;
    private float lastX,lastY,startX,startY,scrollY,scrollDistance,residualX,residualY,residualScroll,sensitivity=1f;
    private long began;
    private boolean moved,multiple,dragging,active=true,trackpad=true;
    private final Runnable drag=this::beginDrag;
    private void beginDrag(){if(active&&!moved&&!multiple){conn.sendMouseButtonDown(MouseButtonPacket.BUTTON_LEFT);dragging=true;}}
    TouchRouter(NvConnection conn,View surface){this.conn=conn;this.surface=surface;slop=ViewConfiguration.get(surface.getContext()).getScaledTouchSlop();}
    void mode(boolean trackpad){cancel();this.trackpad=trackpad;}
    boolean isTrackpad(){return trackpad;}
 void sensitivity(float value){sensitivity=Math.max(.5f,Math.min(2f,value));}
 private static short bounded(float value){return (short)Math.max(Short.MIN_VALUE,Math.min(Short.MAX_VALUE,(int)value));}
    private void position(float x,float y){conn.sendMousePosition((short)Math.max(0,Math.min(surface.getWidth(),x)),(short)Math.max(0,Math.min(surface.getHeight(),y)),(short)surface.getWidth(),(short)surface.getHeight());}
    private float centerY(MotionEvent e){return e.getPointerCount()>1?(e.getY(0)+e.getY(1))/2:e.getY();}
    @Override public boolean onTouch(View view,MotionEvent e){
        if(!active)return true;
        switch(e.getActionMasked()) {
            case MotionEvent.ACTION_DOWN:
                cancel();lastX=startX=e.getX();lastY=startY=e.getY();began=e.getEventTime();moved=multiple=false;
                if(!trackpad)position(lastX,lastY);ui.postDelayed(drag,500);return true;
            case MotionEvent.ACTION_POINTER_DOWN:
                ui.removeCallbacks(drag);if(dragging){conn.sendMouseButtonUp(MouseButtonPacket.BUTTON_LEFT);dragging=false;}
                multiple=true;scrollY=centerY(e);scrollDistance=0;residualScroll=0;return true;
            case MotionEvent.ACTION_MOVE:
                if(multiple){if(e.getPointerCount()>=2){float y=centerY(e),dy=y-scrollY;residualScroll+=dy*2;short delta=bounded(residualScroll);if(delta!=0){conn.sendMouseHighResScroll(delta);residualScroll-=delta;}scrollDistance+=Math.abs(dy);if(scrollDistance>slop)moved=true;scrollY=y;}return true;}
                float x=e.getX(),y=e.getY(),dx=x-lastX,dy=y-lastY;
                if(Math.hypot(x-startX,y-startY)>slop){moved=true;ui.removeCallbacks(drag);}
                if(trackpad){float scale=1280f/Math.max(1,surface.getWidth());residualX+=dx*scale*sensitivity;residualY+=dy*scale*sensitivity;short mx=bounded(residualX),my=bounded(residualY);if(mx!=0||my!=0){conn.sendMouseMove(mx,my);residualX-=mx;residualY-=my;}}
                else position(x,y);
                lastX=x;lastY=y;return true;
            case MotionEvent.ACTION_POINTER_UP:ui.removeCallbacks(drag);return true;
            case MotionEvent.ACTION_UP:
                ui.removeCallbacks(drag);
                if(dragging){conn.sendMouseButtonUp(MouseButtonPacket.BUTTON_LEFT);dragging=false;}
                else if(!moved&&e.getEventTime()-began<500){byte button=multiple?MouseButtonPacket.BUTTON_RIGHT:MouseButtonPacket.BUTTON_LEFT;conn.sendMouseButtonDown(button);conn.sendMouseButtonUp(button);}
                return true;
            case MotionEvent.ACTION_CANCEL:cancel();return true;
        }
        return true;
    }
    void cancel(){residualX=residualY=residualScroll=0;ui.removeCallbacks(drag);if(dragging){conn.sendMouseButtonUp(MouseButtonPacket.BUTTON_LEFT);dragging=false;}}
    @Override public void close(){cancel();active=false;}
}
