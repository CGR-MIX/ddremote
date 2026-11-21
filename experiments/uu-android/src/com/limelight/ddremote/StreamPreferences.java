package com.limelight.ddremote;

import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;

/** Device-local usability preferences; contains no account or remote identity. */
final class StreamPreferences {
 static final String[] QUALITY_LABELS={"流畅 · 720p / 30帧 / 5 Mbps","均衡 · 720p / 60帧 / 10 Mbps","高清 · 1080p / 60帧 / 20 Mbps"};
 private final SharedPreferences values;
 StreamPreferences(Context context){values=context.getSharedPreferences("dd-stream-preferences",Context.MODE_PRIVATE);}
 boolean trackpad(){return values.getBoolean("trackpad",true);}
 void trackpad(boolean value){values.edit().putBoolean("trackpad",value).apply();}
 float sensitivity(){return Math.max(.5f,Math.min(2f,values.getFloat("pointerSensitivity",1f)));}
 void sensitivity(float value){values.edit().putFloat("pointerSensitivity",Math.max(.5f,Math.min(2f,value))).apply();}
 int quality(){return Math.max(0,Math.min(2,values.getInt("quality",1)));}
 void quality(int value){if(value>=0&&value<3)values.edit().putInt("quality",value).apply();}
 boolean muted(){return values.getBoolean("muted",false);}
 void muted(boolean value){values.edit().putBoolean("muted",value).apply();}
 static void applyQuality(Intent intent,int index){
  if(index<0||index>2)throw new IllegalArgumentException("Unknown quality preset");
  intent.putExtra("width",index==2?1920:1280);intent.putExtra("height",index==2?1080:720);intent.putExtra("fps",index==0?30:60);intent.putExtra("bitrate",index==0?5000:index==1?10000:20000);
 }
}
