package com.limelight.ddremote;

import android.app.Activity;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.os.Bundle;
import android.provider.OpenableColumns;
import android.text.InputType;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.InputStream;
import java.io.OutputStream;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** Explicit user-triggered file and clipboard exchange, scoped to one pinned host. */
public final class CompanionActivity extends Activity {
    private static final int OPEN_FILE = 30;
    private static final int SAVE_FILE = 31;
    private final ExecutorService worker = Executors.newSingleThreadExecutor();
    private final List<Button> actionButtons = new ArrayList<>();
    private TextView status;
    private LinearLayout files;
    private EditText linkInput;
    private EditText clipboardInput;
    private CompanionClient client;
    private String downloadName;
    private boolean busy;
    private boolean closed;
    private long accountToken;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        accountToken=getIntent().getLongExtra("DdAccountGeneration",0);
        if (accountToken!=0 && !AccountSession.isCurrent(accountToken)) {finish();return;}
        LinearLayout col = DdUi.page(this, "文件与剪贴板", "从电脑端复制连接链接。连接凭证仅在本页内存中使用，关闭页面后清除。");
        linkInput = new EditText(this);
        linkInput.setHint("ddremote://connect?host=…");
        linkInput.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        linkInput.setSingleLine(true);
        linkInput.setSaveEnabled(false);
        if (getIntent().getData() != null) linkInput.setText(getIntent().getData().toString());
        DdUi.margin(this, col, linkInput, 0, 8);
        actionButtons.add(DdUi.button(this, col, "连接电脑", true, v -> connect()));
        status = DdUi.text(this, "尚未连接", 14, DdUi.MUTED);
        DdUi.margin(this, col, status, 6, 24);
        actionButtons.add(DdUi.button(this, col, "选择文件并发送到电脑", false, v -> chooseUpload()));
        actionButtons.add(DdUi.button(this, col, "刷新电脑共享文件", false, v -> refresh()));
        files = new LinearLayout(this);
        files.setOrientation(LinearLayout.VERTICAL);
        DdUi.margin(this, col, files, 8, 24);
        DdUi.margin(this, col, DdUi.text(this, "共享剪贴板", 21, DdUi.INK), 12, 8);
        clipboardInput = new EditText(this);
        clipboardInput.setHint("输入或粘贴要发送的文字");
        clipboardInput.setMinLines(3);
        clipboardInput.setMaxLines(8);
        clipboardInput.setGravity(android.view.Gravity.TOP);
        clipboardInput.setSaveEnabled(false);
        DdUi.margin(this, col, clipboardInput, 0, 8);
        actionButtons.add(DdUi.button(this, col, "发送文字到电脑剪贴板", false, v -> sendClipboard()));
        actionButtons.add(DdUi.button(this, col, "读取电脑剪贴板", false, v -> readClipboard()));
        actionButtons.add(DdUi.button(this, col, "复制上方文字到手机", false, v -> {
            ClipboardManager clipboard = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            clipboard.setPrimaryClip(ClipData.newPlainText("DD远程", clipboardInput.getText().toString()));
            status.setText("已复制到手机剪贴板");
        }));
        DdUi.button(this, col, "返回首页", false, v -> finish());
        if (getIntent().getBooleanExtra("DdAutoConnect", false)) connect();
    }

    private boolean connected() {
        if (!sessionActive()) {finish();return false;}
        if (client == null) { status.setText("请先粘贴连接链接并连接电脑。"); return false; }
        return true;
    }

    private void connect() {
        if (!sessionActive()) {finish();return;}
        try {
            CompanionLink link = CompanionLink.parse(linkInput.getText().toString());
            CompanionClient candidate = new CompanionClient(link);
            run("正在验证电脑证书并连接…", () -> {
                JSONArray list = new JSONObject(candidate.getJson("/api/files")).getJSONArray("files");
                runOnUiThread(() -> {
                    if (closed || !sessionActive()) return;
                    client = candidate;
                    linkInput.setText("");
                    getIntent().setData(null);
                    renderFiles(list);
                    status.setText("已连接 " + link.authority() + " · 证书指纹已验证");
                });
            });
        } catch (Exception ex) { status.setText(ex.getMessage()); }
    }

    private void refresh() {
        if (!connected()) return;
        CompanionClient active = client;
        run("正在读取共享文件…", () -> {
            JSONArray list = new JSONObject(active.getJson("/api/files")).getJSONArray("files");
            runOnUiThread(() -> { if (!closed) { renderFiles(list); status.setText("文件列表已更新"); } });
        });
    }

    private void renderFiles(JSONArray list) {
        files.removeAllViews();
        if (list.length() == 0) files.addView(DdUi.text(this, "电脑共享目录中暂无文件。发送一个文件即可在这里看到。", 14, DdUi.MUTED));
        for (int i = 0; i < list.length(); i++) {
            JSONObject item = list.optJSONObject(i);
            if (item == null) continue;
            String name = item.optString("name");
            long size = item.optLong("size");
            if (name.isEmpty()) continue;
            DdUi.button(this, files, name + "  ·  " + android.text.format.Formatter.formatShortFileSize(this, size), false,
                    v -> chooseDownload(name));
        }
    }

    private void sendClipboard() {
        if (!connected()) return;
        CompanionClient active = client;
        String text = clipboardInput.getText().toString();
        run("正在发送文字…", () -> {
            JSONObject payload = new JSONObject();
            payload.put("text", text);
            active.postJson("/api/clipboard", payload.toString());
            message("文字已发送到电脑剪贴板");
        });
    }

    private void readClipboard() {
        if (!connected()) return;
        CompanionClient active = client;
        run("正在读取电脑剪贴板…", () -> {
            String text = new JSONObject(active.getJson("/api/clipboard")).getString("text");
            runOnUiThread(() -> { if (!closed) { clipboardInput.setText(text); status.setText("已读取文字；点击复制按钮可复制到手机。"); } });
        });
    }

    private void chooseUpload() {
        if (!connected() || busy) return;
        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        intent.addCategory(Intent.CATEGORY_OPENABLE);
        intent.setType("*/*");
        try { startActivityForResult(intent, OPEN_FILE); }
        catch (android.content.ActivityNotFoundException ex) { status.setText("此设备没有可用的文件选择器。"); }
    }

    private void chooseDownload(String name) {
        if (!connected() || busy) return;
        downloadName = name;
        Intent intent = new Intent(Intent.ACTION_CREATE_DOCUMENT);
        intent.addCategory(Intent.CATEGORY_OPENABLE);
        intent.setType("application/octet-stream");
        intent.putExtra(Intent.EXTRA_TITLE, name);
        try { startActivityForResult(intent, SAVE_FILE); }
        catch (android.content.ActivityNotFoundException ex) { status.setText("此设备没有可用的文件保存位置选择器。"); }
    }

    @Override protected void onActivityResult(int request, int result, Intent data) {
        super.onActivityResult(request, result, data);
        if (result != RESULT_OK || data == null || data.getData() == null || client == null) return;
        Uri uri = data.getData();
        CompanionClient active = client;
        if (request == OPEN_FILE) {
            run("正在读取并发送所选文件…", () -> {
                String displayName = "file";
                long fileLength = -1;
                try (Cursor cursor = getContentResolver().query(uri, new String[] {OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE}, null, null, null)) {
                    if (cursor != null && cursor.moveToFirst()) {
                        if (!cursor.isNull(0)) displayName = cursor.getString(0);
                        if (!cursor.isNull(1)) fileLength = cursor.getLong(1);
                    }
                }
                final String fileName = displayName;
                try (InputStream input = getContentResolver().openInputStream(uri)) {
                    if (input == null) throw new java.io.IOException("无法打开文件");
                    active.upload(fileName, input, fileLength);
                }
                JSONArray list = new JSONObject(active.getJson("/api/files")).getJSONArray("files");
                runOnUiThread(() -> { if (!closed) { renderFiles(list); status.setText("发送完成：" + fileName); } });
            });
        } else if (request == SAVE_FILE && downloadName != null) {
            String name = downloadName;
            downloadName = null;
            run("正在保存 " + name + "…", () -> {
                try (OutputStream output = getContentResolver().openOutputStream(uri, "wt")) {
                    if (output == null) throw new java.io.IOException("无法写入所选位置");
                    active.download(name, output);
                }
                message("文件已保存：" + name);
            });
        }
    }

    private interface Work { void execute() throws Exception; }
    private void run(String label, Work work) {
        if (busy || closed || !sessionActive()) return;
        busy = true;
        actionButtons.forEach(b -> b.setEnabled(false));
        status.setText(label);
        worker.execute(() -> {
            try { if (sessionActive()) work.execute(); }
            catch (Exception ex) {
                if (ex instanceof javax.net.ssl.SSLException) message("证书验证失败。请确认电脑端连接链接与当前电脑一致。");
                else message("操作失败：" + (ex.getMessage() == null ? "请检查网络后重试。" : ex.getMessage()));
            } finally {
                runOnUiThread(() -> { if (!closed) { busy = false; actionButtons.forEach(b -> b.setEnabled(true)); } });
            }
        });
    }

    private void message(String value) { runOnUiThread(() -> { if (!closed) status.setText(value); }); }

    private boolean sessionActive() {return accountToken==0 || AccountSession.isCurrent(accountToken);}

    @Override protected void onDestroy() {
        closed = true;
        client = null;
        worker.shutdownNow();
        super.onDestroy();
    }
}
