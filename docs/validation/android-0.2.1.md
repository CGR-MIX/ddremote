# Android 0.2.1-preview 登录初始化修复

日期：2026-11-20，Asia/Hong_Kong。

用户报告小米13、Android16，在点击登录后出现“网络组件发生内部错误”。Android16/API36 AOSP x86_64模拟器安装正式0.2.0包后，点击登录复现同样提示。

## 修复

`tsnet.Server.Dir`只指定部分状态路径。固定版本Tailscale 1.104.7的`ipnlocal.NewLocalBackend`还调用`logpolicy.LogsDir`；Android应用进程没有可用的桌面HOME、工作目录和/tmp时，该调用会panic。DD在启动后端前创建、验证no-backup应用私有目录中的`logs`，设置该组件支持的`TS_LOGS_DIR`。不会改变系统权限或请求存储权限。

异常处理补充操作名称和安全的符号堆栈，写入Android `DDNetwork`日志；不输出原始panic值、参数、token或授权链接。失败目录返回具体错误。APK版本码从2升至3，沿用同一发布签名。

## 验证

- 12项Go生产网络测试通过，包括私有日志目录被上游实际选用、目录冲突/相对路径拒绝、panic诊断不泄露请求数据，以及原有账号所有权、退出取消、会话代次和TCP/UDP测试。
- JVM会话代次测试及100次并发退出竞争通过。
- 四ABI网络库重建；release assemble和lint完成。Lint报告0错误、194警告；SARIF quick-fix报告器对既有土耳其语资源打印异常，但任务成功，XML诊断正常。
- 正式0.2.0 APK在Android16模拟器复现内部错误。
- 以`adb install -r`升级到正式签名0.2.1成功。点击登录后，实际浏览器`org.chromium.webview_shell`打开`login.tailscale.com`授权地址；新进程未产生`DDNetwork`或`AndroidRuntime`错误。
- 不自动输入账号或完成身份验证；小米13真机复测、已登录设备发现、串流及云端退出删除仍待验证。

证据：`apps/android/build-evidence/android16-login-0.2.1.json`、`embedded-network-tests-0.2.1.txt`、`account-generation-tests-0.2.1.txt`、`release-build-0.2.1.log`、`apk-signature-verification-0.2.1.txt`。测试XML和旧版截图放在本地`.testdata/android-startup-0.2.1`，不进入源码交付。

## 交付

- `dist/DDRemote-0.2.1-preview-android.apk`，44,088,693字节。
- SHA256：`cc6807009818636ecf7d368f4d79bbfb01c4eab99c4be96343fa0d3af5351a49`。
- 包名`io.ddremote.android`，versionCode3，minSDK21、targetSDK36、compileSDK37。
- arm64-v8a、armeabi-v7a、x86、x86_64；v1/v2/v3签名及16KB对齐验证通过。
- 证书SHA256：`0917f9d1eedfdfa810edbeb0699117a3eb65dc545c05dbd127f6a12be5f20ae1`。
- Windows仍使用原0.2.0 EXE。0.2.1源码归档包含此次Android修改及对应Windows源码，可用`scripts/fetch-sources.ps1 -SkipUpstream -SourceVersion 0.2.1-preview`重建，不覆盖旧0.2.0源码包。
