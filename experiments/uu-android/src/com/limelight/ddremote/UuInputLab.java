package com.limelight.ddremote;

import android.app.Activity;
import android.os.Bundle;
import android.view.View;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputConnection;
import android.view.inputmethod.InputMethodManager;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;
import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;

/** Runs the original UU text input view and key-layout assets behind a DD callback. */
public final class UuInputLab extends Activity {
    private final List<String> events=new ArrayList<>();
    private EditText input;
    private UuTextInputAdapter inputAdapter;
    private TextView status, summary;
    private LinearLayout keys;
    private int textEvents,keyEvents;
    private boolean testing;
    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        LinearLayout page=UuAdapterHome.page(this);
        UuAdapterHome.heading(page,"UU 输入组件适配");
        UuAdapterHome.text(page,"原包输入组件与键盘布局。输入仅在本页验证，不发送到电脑。",15);
        status=UuAdapterHome.text(page,"正在加载…",16);
        summary=UuAdapterHome.text(page,"文字提交 0 次 · 按键 0 次",14);
        try {
            inputAdapter=UuTextInputAdapter.create(this,new UuTextInputAdapter.Sink() {
                public void committedText(String value){textEvents++;if(testing)events.add("text:"+value);showCounts();}
                public void keyStroke(int code){keyEvents++;if(testing)events.add("key:"+code);showCounts();}
            });
            input=inputAdapter.view();input.setHint("点击后使用手机输入法");input.setMinHeight(70);
            page.addView(input,new LinearLayout.LayoutParams(-1,-2));
            UuAdapterHome.button(page,"打开手机输入法",v -> {input.requestFocus();((InputMethodManager)getSystemService(INPUT_METHOD_SERVICE)).showSoftInput(input,InputMethodManager.SHOW_IMPLICIT);});
            UuAdapterHome.button(page,"运行输入组件自检",v -> selfTest());
            UuAdapterHome.button(page,"主键盘",v -> loadKeys("win_full_keyboard_main.json"));
            UuAdapterHome.button(page,"数字键盘",v -> loadKeys("full_keyboard_num.json"));
            UuAdapterHome.button(page,"功能键盘",v -> loadKeys("full_keyboard_func.json"));
            keys=new LinearLayout(this);keys.setOrientation(LinearLayout.VERTICAL);page.addView(keys);
            loadKeys("win_full_keyboard_main.json");
            status.setText("已加载 UU 原包 InvisibleEditText 与键盘布局。");
            if(getIntent().getBooleanExtra("selfTest",false))input.post(this::selfTest);
        } catch(Exception|LinkageError ex) {status.setText("组件加载失败："+UuAdapterHome.friendly(ex));}
    }
    private void showCounts(){summary.setText("文字提交 "+textEvents+" 次 · 按键 "+keyEvents+" 次");}
    @Override protected void onDestroy(){if(inputAdapter!=null)inputAdapter.close();events.clear();super.onDestroy();}
    private void selfTest() {
        try {
            testing=true;events.clear();textEvents=keyEvents=0;
            InputConnection connection=input.onCreateInputConnection(new EditorInfo());
            if(connection==null)throw new IllegalStateException("输入连接为空");
            connection.setComposingText("ni",1);
            if(!events.isEmpty())throw new IllegalStateException("未确认的拼音被提前提交");
            connection.commitText("你好🙂",1);
            connection.finishComposingText();
            connection.commitText("\n",1);
            connection.deleteSurroundingText(1,0);
            if(events.size()!=3||!"text:你好🙂".equals(events.get(0))||!"key:66".equals(events.get(1))||!"key:67".equals(events.get(2)))
                throw new IllegalStateException("组合输入回调次数或按键不符合预期");
            connection.setComposingText("中文",1);connection.finishComposingText();connection.finishComposingText();
            if(events.size()!=4||!"text:中文".equals(events.get(3)))throw new IllegalStateException("结束组合输入重复或遗漏");
            status.setText("自检通过：中文、emoji、组合输入、回车、退格与重复完成。");
            android.util.Log.i("DDUuInputTest","PASS original UU input: composition, unicode, enter, backspace, finish-once");
        } catch(Exception|LinkageError ex) {
            status.setText("自检失败："+UuAdapterHome.friendly(ex));
            android.util.Log.e("DDUuInputTest","FAIL "+ex.getClass().getSimpleName());
        } finally {testing=false;events.clear();}
    }
    private void loadKeys(String file) {
        try(java.io.InputStream source=getAssets().open("fullkeyboard/"+file);java.io.ByteArrayOutputStream out=new java.io.ByteArrayOutputStream()) {
            byte[] buffer=new byte[4096];int n;while((n=source.read(buffer))>=0)out.write(buffer,0,n);
            JSONArray rows=new JSONObject(new String(out.toByteArray(),java.nio.charset.StandardCharsets.UTF_8)).getJSONArray("keyRowList");
            keys.removeAllViews();
            android.widget.HorizontalScrollView scroll=new android.widget.HorizontalScrollView(this);
            LinearLayout board=new LinearLayout(this);board.setOrientation(LinearLayout.VERTICAL);scroll.addView(board);keys.addView(scroll);
            float density=getResources().getDisplayMetrics().density;
            for(int r=0;r<rows.length();r++) {
                LinearLayout row=new LinearLayout(this);board.addView(row);
                JSONArray entries=rows.getJSONObject(r).getJSONArray("keyItemList");
                for(int i=0;i<entries.length();i++) {
                    JSONObject entry=entries.getJSONObject(i);int code=entry.optInt("keyCode");String label=entry.optString("displayName");
                    android.widget.Button key=new android.widget.Button(this);key.setAllCaps(false);key.setText(label);key.setTextSize(12);key.setMinWidth(0);key.setMinimumWidth(0);key.setPadding(0,0,0,0);
                    int width=(int)(Math.max(1,entry.optDouble("width",1))*46*density);
                    LinearLayout.LayoutParams p=new LinearLayout.LayoutParams(width,(int)(44*density));
                    p.leftMargin=(int)(Math.min(8,entry.optDouble("startMargin",0))*density);row.addView(key,p);
                    key.setOnClickListener(v -> {keyEvents++;summary.setText("文字提交 "+textEvents+" 次 · 按键 "+keyEvents+" 次；最近按键 "+label+" / "+code);});
                }
            }
        } catch(Exception ex) {status.setText("键盘布局加载失败："+UuAdapterHome.friendly(ex));}
    }
}
