# DD远程 Android 构建

源码来源：Moonlight Android `b48494cb96bff23d8886c4775cc4f39a1075495d`。
Moonlight Common C：`874ac9548f1bd6f095ef2b435c42cdde460e7821`。
ENet：`aca87840b57f045a1f7f9299e4b1b9b8e2a5e2f1`；NanoRS：`b1e3c22ca0cdc0bb83e3cd6ed1a2fc77869ed99a`。

DD远程新增账号首页、内嵌 tsnet、安全设备发现、自动配对、品牌图标及文件/剪贴板原生客户端；保留 Moonlight 的 Sunshine 协议、音视频、触控、键鼠、手柄、唤醒、编码和画质选项。原作者版权与 GPLv3 文本保留在 `LICENSE.txt` 和应用内“关于与开源许可”。这不是 Moonlight 官方发行版，包名为 `io.ddremote.android`。

## 依赖

- JDK 21（本次 Temurin 21.0.13+9）
- Gradle 9.7.1，Android Gradle Plugin 9.3.2
- Android SDK Platform 37 / Build Tools 37.0.0
- Android NDK 29.0.14206865（r29）
- Go 1.27.4、Tailscale tsnet 1.104.7（锁定在 `network/go.mod` 和 `go.sum`）
- 首次构建需要访问 Google Maven、Maven Central、JitPack

NDK 官方下载：`https://dl.google.com/android/repository/android-ndk-r29-windows.zip`。
官方仓库元数据给出的 SHA1 为 `ab3bb30fbb9e6903666d60c55d11e78b04e07472`，下载后已验证一致。

本项目使用 Moonlight 上游包含的 OpenSSL/Opus 静态库，NDK 从源码编译 Moonlight 协议核心、ENet、NanoRS 和 JNI 封装。APK 不是修改现有第三方 APK 得到的。

## 普通环境构建

先编译内嵌网络库（四 ABI，JNI C 包装调用 Go c-shared；不需要 gomobile）：

```powershell
.\build-network.ps1 -NdkRoot 'C:\path\to\android-ndk-r29' -StageRoot 'C:\temp\ddremote-android-build'
```

脚本写入 `app/src/main/jniLibs/<ABI>/libddnetwork.so`，然后构建 APK：

```powershell
$env:JAVA_HOME = 'C:\path\to\jdk21'
$env:ANDROID_HOME = 'C:\path\to\android-sdk'
'sdk.dir=C\:/path/to/android-sdk' | Set-Content local.properties
.\gradlew.bat :app:assembleNonRootRelease :app:lintNonRootRelease --no-daemon
```

如果从 Git 拉取源码，先执行 `git submodule update --init --recursive`。输出为 `app/build/outputs/apk/nonRoot/release/app-nonRoot-release-unsigned.apk`。使用自己的签名密钥通过 SDK 的 `apksigner` 签名；升级安装必须沿用同一密钥。

`sign-ddremote.ps1` 依次执行 16 KB 页面 zipalign、签名、验签、对齐检查和 SHA256。签名密码以本地文件参数读取，避免出现在进程参数和日志中。本次本地生成的私密密钥备份位于根目录 `.tools/android/signing/`，不得放入公开源码压缩包；在下一版本继续使用该密钥才能覆盖安装并保留配对信息。

## 本机暂存构建

本机 Java 子进程不能向 OneDrive 工作区和默认 Temp 写入。没有更改用户权限、安全设置或现有工具。源码在此目录编辑，通过 `build-ddremote.ps1` 复制至独立的 `D:\DSHworkspace\_toolchain\ddremote-android-build\source` 构建。SDK/JDK/Gradle 使用机器上已有工具，新增 NDK 放在本项目 `.tools/android/`。

脚本要求指定 ASCII 暂存路径并将所有 Gradle 缓存、JVM 临时目录和 Android 用户目录限定在该目录。`robocopy /E` 仅同步输入，不删除暂存目录中的其他文件；不要使用已有业务源码目录作为暂存路径。

```powershell
.\build-ddremote.ps1 `
  -JavaHome 'D:\DSHworkspace\_toolchain\jdk21\jdk-21.0.13+9' `
  -SdkRoot 'D:\DSHworkspace\_toolchain\ddremote-android-build\sdk' `
  -GradleExe 'D:\DSHworkspace\_toolchain\gradle-9.7.1\bin\gradle.bat' `
  -StageRoot 'D:\DSHworkspace\_toolchain\ddremote-android-build'
```

## 安全协议测试

`tests/run-protocol-tests.ps1` 直接编译生产 `CompanionLink`、`CompanionClient`，与本地自签 HTTPS 服务通信。覆盖有效连接、IPv6、重复参数、协议/主机注入、错误证书指纹、Bearer 鉴权、禁止重定向、UTF-8 剪贴板和二进制文件往返。

```powershell
.\tests\run-protocol-tests.ps1 -JavaHome $env:JAVA_HOME -OutputDirectory 'C:\temp\ddremote-tests'
```

文件/剪贴板入口接受 `ddremote://connect?host=IP&port=48200&token=BASE64URL&fingerprint=SHA256HEX`。点击连接后才通信；每个请求验证电脑证书指纹并带 Bearer 凭证，拒绝重定向。凭证不写入偏好或日志，关闭页面后清除。上传/下载通过 Android 系统文档选择器授权，不申请全盘存储权限；剪贴板仅由明确按钮操作读取或写入。

## 账号与自动串流

0.2.1-preview（版本码3）修复Android初始化时日志路径缺失：`tsnet.Server.Dir`不覆盖上游`ipnlocal.NewLocalBackend`调用的`logpolicy.LogsDir`。在创建后端前建立并验证应用no-backup私有目录下的`logs`，通过`TS_LOGS_DIR`交给该日志组件。原生panic返回操作名称及允许列出的原因，并用`DDNetwork`标签记录函数名/文件名/行号；不记录原始panic内容、登录URL或请求数据。Android 16/API36模拟器已复现0.2.0相同错误，并验证同签名升级后0.2.1能打开Tailscale授权页。尚未代替小米13真机登录与串流验收。

APK 内嵌 Tailscale tsnet 1.104.7，使用系统浏览器登录账号；不需要外部 Tailscale 应用，也不创建系统 VPN。只接受 `Self.UserID` 相同、非共享、非 tagged 节点，且须通过安全网络响应 DD `/v1/device`，返回的 `id` 必须等于该节点的 Tailscale StableID，才在“我的电脑”出现。点击机器后再次校验此身份，再核对账号通道给出的 Sunshine 公开证书，以随机 nonce/PIN 自动配对并启动 Desktop。用户不需输入 IP、端口或 PIN。主机仍须由用户显式开启桌面共享。

本机 `127.0.0.2` TCP/UDP 代理承接 Moonlight 协议并由 tsnet 发往所选机器；远程串流使用 1024 字节包尺寸。文件入口从账号通道读取凭证，保留精确 TLS 证书指纹验证。Android 系统信任根提供给 Go 标准 TLS 验证器；Android 平台网卡列表与网络变化通过上游 netmon 注册接口传入，兼容 Android 11+ 的 netlink 权限限制。

账号节点默认使用 `Ephemeral:true` 与内存状态存储。退出账号立即使 Java 会话代次失效，并从独立线程取消正在执行的配对/发现和本地代理，然后请求服务端注销并清理 DD 专用配对证书与 UUID。云端注销 context 最长 5 秒；原生退出调用最多等待 12 秒，超时明确提示未确认云端移除，本地关闭继续完成且期间拒绝启动新网络。迟到的网络调用也携带会话代次，不能进入下一次登录；旧回调不能启动串流或重新写入账号凭证。手动 LAN 电脑和下载文件保留。正常切后台/屏幕旋转不注销。彻底结束程序后需重新登录；正常任务关闭会尝试注销，Android 强制停止或系统杀进程可能不执行回调，此时由账号服务清理离线临时节点。退出完成后只有用户点击登录才开启新会话。

`test-account-lifecycle.ps1 -JavaHome <JDK路径>` 运行生产 Go 网络测试和纯 JVM 会话防护测试。覆盖账号所有权、StableID 绑定、长配对和发现取消、慢关闭限时返回、旧代次拒绝、TCP/UDP 往返关闭与 Android 网卡快照，并验证旧回调/凭证写入拒绝和 100 次退出并发竞争。端到端账号登录、视频、硬件解码、触摸和手柄仍需在真实 Android 设备与目标电脑上验证。
