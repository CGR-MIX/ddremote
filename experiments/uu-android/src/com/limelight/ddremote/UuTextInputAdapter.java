package com.limelight.ddremote;

import android.app.Activity;
import android.util.AttributeSet;
import android.view.KeyEvent;
import android.widget.EditText;
import java.lang.reflect.Proxy;

/** Narrow adapter for the original UU IME widget, independent of UU account/room services. */
public final class UuTextInputAdapter implements AutoCloseable {
    public interface Sink {
        void committedText(String text);
        void keyStroke(int androidKeyCode);
    }
    private final EditText view;
    private volatile Sink sink;
    private UuTextInputAdapter(Activity activity,Sink sink) throws ReflectiveOperationException {
        this.sink=sink;
        Class<?> original=Class.forName("com.remote.screen_keyboard.ui.view.InvisibleEditText");
        view=(EditText)original.getConstructor(android.content.Context.class,AttributeSet.class).newInstance(activity,null);
        view.setSaveEnabled(false);
        // The original constructor uses defStyleAttr=0. Without its XML inputType,
        // Android can treat this programmatically created editor as TYPE_NULL.
        view.setInputType(android.text.InputType.TYPE_CLASS_TEXT|android.text.InputType.TYPE_TEXT_FLAG_MULTI_LINE);
        view.setImeOptions(view.getImeOptions()|android.view.inputmethod.EditorInfo.IME_FLAG_NO_EXTRACT_UI|android.view.inputmethod.EditorInfo.IME_FLAG_NO_FULLSCREEN);
        Class<?> callback=Class.forName("ke.f");
        Object unit=Class.forName("Vd.A").getField("a").get(null);
        Object listener=Proxy.newProxyInstance(activity.getClassLoader(),new Class<?>[]{callback},(proxy,method,args) -> {
            if(method.getDeclaringClass()==Object.class) {
                if(method.getName().equals("hashCode"))return System.identityHashCode(proxy);
                if(method.getName().equals("equals"))return proxy==args[0];
                return "DD UU input adapter";
            }
            if(method.getName().equals("invoke")) {
                Sink target=this.sink;
                if(target!=null) {
                    if(args[0] instanceof String)target.committedText((String)args[0]);
                    else if(args[1] instanceof Integer)target.keyStroke((Integer)args[1]);
                }
                return unit;
            }
            return null;
        });
        original.getMethod("setOnCommitTextListener",callback).invoke(view,listener);
    }
    public static UuTextInputAdapter create(Activity activity,Sink sink) throws ReflectiveOperationException {
        return new UuTextInputAdapter(activity,sink);
    }
    public EditText view(){return view;}
    @Override public void close(){sink=null;view.clearFocus();view.setText("");}
}
