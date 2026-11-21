package com.limelight.ddremote;
import android.app.*;import android.content.*;import android.os.*;import android.view.*;import android.view.inputmethod.*;
import com.limelight.nvstream.*;import com.limelight.nvstream.http.*;import com.limelight.nvstream.input.*;
import java.util.*;
/** Real UU IME and production routing tested against a recording transport. No desktop input. */
final class InputVerification {
 static void check(boolean b,String message){if(!b)throw new AssertionError(message);}
 static class Recording extends NvConnection {
  final List<String> text=new ArrayList<>();final List<String> keys=new ArrayList<>();int left,right,up,scroll,moves,positions,totalX,totalScroll;
  Recording(Context c,long token){super(c,new ComputerDetails.AddressTuple("127.0.0.1",1),1,"test",new StreamConfiguration.Builder().build(),AccountSession.cryptoProvider(c,token),null);}
  public void sendUtf8Text(String s){text.add(s);}
  public void sendKeyboardInput(short code,byte direction,byte modifiers,byte flags){keys.add((code&65535)+":"+direction+":"+modifiers);}
  public void sendMouseButtonDown(byte b){if(b==MouseButtonPacket.BUTTON_LEFT)left++;if(b==MouseButtonPacket.BUTTON_RIGHT)right++;}
  public void sendMouseButtonUp(byte b){up++;}
  public void sendMouseHighResScroll(short amount){scroll++;totalScroll+=amount;}
  public void sendMouseMove(short x,short y){moves++;totalX+=x;}
  public void sendMousePosition(short x,short y,short w,short h){check(x>=0&&x<=w&&y>=0&&y<=h,"absolute bounds");positions++;}
 }
 static MotionEvent event(long down,int action,float x,float y,boolean two){
  int n=two?2:1;MotionEvent.PointerProperties[] props=new MotionEvent.PointerProperties[n];MotionEvent.PointerCoords[] coords=new MotionEvent.PointerCoords[n];
  for(int i=0;i<n;i++){props[i]=new MotionEvent.PointerProperties();props[i].id=i;props[i].toolType=MotionEvent.TOOL_TYPE_FINGER;coords[i]=new MotionEvent.PointerCoords();coords[i].x=x+i*50;coords[i].y=y;coords[i].pressure=1;coords[i].size=1;}
  return MotionEvent.obtain(down,SystemClock.uptimeMillis(),action,n,props,coords,0,0,1,1,0,0,InputDevice.SOURCE_TOUCHSCREEN,0);
 }
 static void touch(TouchRouter router,View view,long t,int action,float x,float y,boolean two){MotionEvent e=event(t,action,x,y,two);router.onTouch(view,e);e.recycle();}
 static void run(Instrumentation i){Bundle result=new Bundle();Activity a=null;
  try{
   a=i.startActivitySync(new Intent(i.getTargetContext(),UuInputLab.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));final Activity activity=a;
   final Throwable[] failure={null};final TouchRouter[] r={null};final Recording[] recording={null};final View[] view={null};
   i.runOnMainSync(()->{try{
    long token=AccountSession.open(activity);Recording c=new Recording(activity,token);recording[0]=c;SunshineInput keys=new SunshineInput(c);
    UuTextInputAdapter ime=UuTextInputAdapter.create(activity,new UuTextInputAdapter.Sink(){public void committedText(String s){keys.text(s);}public void keyStroke(int code){keys.stroke(code);}});
    EditorInfo info=new EditorInfo();InputConnection ic=ime.view().onCreateInputConnection(info);check((info.inputType&15)==1,"UU editor missing text input type");ic.setComposingText("ni",1);check(c.text.isEmpty(),"premature composition");ic.commitText("你好🙂",1);ic.finishComposingText();check(c.text.equals(Arrays.asList("你好🙂")),"Unicode transport");
    ic.commitText("\n",1);ic.deleteSurroundingText(1,0);check(c.keys.equals(Arrays.asList("32781:3:0","32781:4:0","32776:3:0","32776:4:0")),"enter/backspace mapping");
    c.keys.clear();keys.key(KeyEvent.KEYCODE_CTRL_LEFT,true);keys.chord(KeyEvent.KEYCODE_CTRL_LEFT,KeyEvent.KEYCODE_A);keys.close();
    check(c.keys.equals(Arrays.asList("32930:3:2","32833:3:2","32833:4:2","32930:4:0")),"held modifier/release");
    int count=c.text.size();keys.text("ignored");check(c.text.size()==count,"post-disconnect input");ime.close();
    View surface=new View(activity);surface.layout(0,0,1280,720);view[0]=surface;TouchRouter router=new TouchRouter(c,surface);r[0]=router;
    long t=SystemClock.uptimeMillis();touch(router,surface,t,0,500,300,false);touch(router,surface,t,1,500,300,false);check(c.left==1&&c.up==1,"single tap");
    t=SystemClock.uptimeMillis();touch(router,surface,t,0,500,300,false);touch(router,surface,t,2,650,350,false);touch(router,surface,t,1,650,350,false);check(c.moves>0&&c.left==1,"trackpad move");
    int initialX=c.totalX;router.sensitivity(1f);t=SystemClock.uptimeMillis();touch(router,surface,t,0,500,300,false);for(int j=1;j<=40;j++)touch(router,surface,t,2,500+j*.25f,300,false);touch(router,surface,t,MotionEvent.ACTION_CANCEL,510,300,false);check(c.totalX-initialX==10,"fractional trackpad motion lost");
    int initialScroll=c.totalScroll;t=SystemClock.uptimeMillis();touch(router,surface,t,0,500,300,false);touch(router,surface,t,5|(1<<8),500,300,true);for(int j=1;j<=40;j++)touch(router,surface,t,2,500,300+j*.25f,true);touch(router,surface,t,MotionEvent.ACTION_CANCEL,500,310,false);check(c.totalScroll-initialScroll==20,"fractional scroll lost");
    check(UuStreamActivity.localKey(KeyEvent.KEYCODE_VOLUME_UP)&&UuStreamActivity.localKey(KeyEvent.KEYCODE_VOLUME_DOWN),"phone volume intercepted");
    router.mode(false);t=SystemClock.uptimeMillis();touch(router,surface,t,0,400,250,false);touch(router,surface,t,1,400,250,false);check(c.positions>0&&c.left==2,"direct touch");
    t=SystemClock.uptimeMillis();touch(router,surface,t,0,500,300,false);touch(router,surface,t,5|(1<<8),500,300,true);touch(router,surface,t,6|(1<<8),500,300,true);touch(router,surface,t,1,500,300,false);check(c.right==1,"two-finger right click");
    t=SystemClock.uptimeMillis();touch(router,surface,t,0,500,300,false);touch(router,surface,t,5|(1<<8),500,300,true);for(int j=1;j<=60;j++)touch(router,surface,t,2,500,300+j*2,true);touch(router,surface,t,6|(1<<8),500,420,true);touch(router,surface,t,1,500,420,false);check(c.scroll>0&&c.right==1,"scroll must not right-click");
    t=SystemClock.uptimeMillis();touch(router,surface,t,0,500,300,false);
   }catch(Throwable e){failure[0]=e;}});
   if(failure[0]!=null)throw new AssertionError(failure[0]);Thread.sleep(650);
   i.runOnMainSync(()->{try{Recording c=recording[0];check(c.left==3,"long-press drag");int before=c.up;r[0].close();check(c.up==before+1,"drag released on disconnect");}catch(Throwable e){failure[0]=e;}});
   if(failure[0]!=null)throw new AssertionError(failure[0]);
   result.putString("inputLogic","PASS: original UU Unicode/composition, enter/backspace, held Ctrl chord, release, trackpad, direct touch, two-finger right click, fractional movement/scroll, phone volume keys, long-press drag release");
   i.runOnMainSync(activity::finish);i.finish(Activity.RESULT_OK,result);
  }catch(Throwable e){result.putString("failure",e.toString());if(a!=null){Activity last=a;i.runOnMainSync(last::finish);}i.finish(Activity.RESULT_CANCELED,result);}
 }
}
