package com.limelight.ddremote;

import android.app.Activity;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.view.View;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

final class DdUi {
    static final int INK = Color.rgb(25, 38, 63);
    static final int BLUE = Color.rgb(23, 78, 214);
    static final int MUTED = Color.rgb(90, 105, 129);
    static int dp(Activity a, int n) { return Math.round(a.getResources().getDisplayMetrics().density * n); }

    static LinearLayout page(Activity a, String title, String subtitle) {
        ScrollView scroll = new ScrollView(a);
        scroll.setFillViewport(true);
        LinearLayout col = new LinearLayout(a);
        col.setOrientation(LinearLayout.VERTICAL);
        col.setPadding(dp(a, 24), dp(a, 32), dp(a, 24), dp(a, 32));
        scroll.addView(col);
        a.setContentView(scroll);
        if (android.os.Build.VERSION.SDK_INT >= 23) {
            a.getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR);
        } else {
            a.getWindow().setStatusBarColor(INK);
        }
        // Insets are applied by the platform on older Android versions and explicitly here on 15+.
        scroll.setOnApplyWindowInsetsListener((v, insets) -> {
            col.setPadding(dp(a, 24) + insets.getSystemWindowInsetLeft(),
                    dp(a, 24) + insets.getSystemWindowInsetTop(),
                    dp(a, 24) + insets.getSystemWindowInsetRight(),
                    dp(a, 24) + insets.getSystemWindowInsetBottom());
            return insets;
        });
        TextView h = text(a, title, 32, INK);
        h.setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL));
        col.addView(h);
        TextView sub = text(a, subtitle, 15, MUTED);
        sub.setLineSpacing(dp(a, 4), 1);
        margin(a, col, sub, 10, 20);
        return col;
    }

    static TextView text(Activity a, String value, int size, int color) {
        TextView view = new TextView(a);
        view.setText(value);
        view.setTextSize(size);
        view.setTextColor(color);
        return view;
    }

    static Button button(Activity a, LinearLayout col, String title, boolean primary, View.OnClickListener listener) {
        Button button = new Button(a);
        button.setText(title);
        button.setAllCaps(false);
        button.setTextSize(16);
        button.setTextColor(primary ? Color.WHITE : INK);
        GradientDrawable bg = new GradientDrawable();
        bg.setColor(primary ? BLUE : Color.WHITE);
        bg.setCornerRadius(dp(a, 12));
        if (!primary) bg.setStroke(dp(a, 1), Color.rgb(216, 224, 238));
        button.setBackground(bg);
        button.setPadding(dp(a, 16), dp(a, 14), dp(a, 16), dp(a, 14));
        button.setMinHeight(dp(a, 56));
        button.setOnClickListener(listener);
        margin(a, col, button, 6, 8);
        return button;
    }

    static void margin(Activity a, LinearLayout col, View view, int top, int bottom) {
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(-1, -2);
        p.setMargins(0, dp(a, top), 0, dp(a, bottom));
        col.addView(view, p);
    }
}
