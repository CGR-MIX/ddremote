# DD远程 0.2.0-preview 第三方软件、源码与许可证

DD远程新增及修改代码采用 GNU GPL version 3；完整文本见根目录 `LICENSE`。第三方文件保留原作者版权及原有许可证，本说明不会把 BSD、MIT、Apache、LGPL 或系统组件重新授权为 GPL。DD远程是独立开发版本，不是 Sunshine、Moonlight、Tailscale 或网易 UU 的官方发行版。

## 发布构成

| 组件 | 实际版本 / 固定提交 | 用途与许可 |
| --- | --- | --- |
| Sunshine | `v2026.914.233613` / `63d35f702ee9e362e43263742981836ec0710384` | Windows 主机端；原版官方 AMD64 lite ZIP，GPL-3.0 |
| Moonlight Qt | `v6.2.0` / `de2467e433821664cdd2224aad8c89a625be1ad9` | Windows 观看端；原版官方 x64 portable ZIP，GPL-3.0 |
| Moonlight Qt dependencies | `v19` / `79621271459ebb945343f58d7acd6ad24d402c38` | Moonlight 6.2.0 的 `setup-deps.ps1` 指定的原生依赖构建仓库；各子组件许可分别适用 |
| Tailscale | `v1.104.7` / `cbc74fe48ef1b1649f69a4d9a602319427e25102` | Windows 和 Android 内嵌 `tsnet`，BSD-3-Clause |
| Moonlight Android | `v12.2` / `b48494cb96bff23d8886c4775cc4f39a1075495d` | Android 客户端修改基础，GPL-3.0 |
| GoWebView2 | `v0.0.0-20260205173254-56598839c808` | Windows WebView2 绑定，MIT；SDK 自带许可单独保留 |
| GoWinloader | `v0.0.0-20250406163304-c1995be93bd1` | GoWebView2 依赖，ISC |
| go-qrcode | `v0.0.0-20200617195104-da1b6568686e` | 本地连接二维码，MIT |
| airhand | 参考提交 `48d9ac8a13985f24409629028d6eb576dfc6e1b9` | MIT，仅作架构与交互参考，没有复制进交付程序 |

仓库：

- https://github.com/SunshineStream/Sunshine
- https://github.com/moonlight-stream/moonlight-qt
- https://github.com/moonlight-stream/moonlight-qt-deps
- https://github.com/moonlight-stream/moonlight-android
- https://github.com/tailscale/tailscale
- https://github.com/jchv/go-webview2
- https://github.com/jchv/go-winloader
- https://github.com/skip2/go-qrcode
- https://github.com/sqlpxc/airhand

Windows 的 Go 版本为 `1.27.4`。`apps/windows/go.mod`、`go.sum` 是实际依赖锁定依据；`licenses/go-modules.json` 和 `licenses/go/` 保留解析到的模块及其上游许可。该清单包含跨平台和构建依赖，并不表示每个模块都进入 Windows 二进制。Go runtime 的 BSD 许可也随 notices 保留。

Windows 首次运行把内嵌第三方程序解压到应用数据目录。`licenses/windows-payload-inventory.json` 记录两个原版 ZIP 的 SHA-256 和 EXE/DLL 文件清单；`apps/windows/payload/*.source.json` 记录官方发布资产 URL 和哈希。没有修改 Sunshine 或 Moonlight Qt 二进制；DD远程新增管理界面、生命周期管理、配对流程、受控 Tailscale 转发和文件/剪贴板服务。

Windows 默认注册 Tailscale ephemeral 节点并使用内存 state store：退出账号和彻底关闭程序会请求服务器注销该节点；普通窗口最小化不触发注销。彻底重开需要重新登录。网络身份密钥不会持久保存，tsnet 必需的日志/缓存使用独立临时目录并在关闭后删除。断网时无法确认立即从服务端移除，程序仍断开并明确报告，服务端随后按 ephemeral 规则清理。这些客户端路径已进行 mock/配置测试，真实账号删除行为未据此宣称通过验收。

## Windows 原生第三方库

Sunshine 使用 GPL 主程序及 FFmpeg 编码组件，另有 Boost（BSL-1.0）、curl（curl license）、MiniUPnPc（BSD）、nlohmann/json（MIT）、OpenSSL（Apache-2.0）、Opus（BSD）、oneVPL（MIT）、Qt（GPL/LGPL）、zlib（zlib）、MinHook（BSD）、cppwinrt（MIT）、NVIDIA NVAPI 及仓库中的显示/虚拟输入组件。原始文本保存在 `licenses/` 和 `licenses/upstream/Sunshine/`；NVAPI 与各子模块以自己的许可证为准。Sunshine 的构建脚本位于固定源码中的 `.github/workflows/ci-windows.yml` 和 `third-party/build-deps/`。

Moonlight Qt 的 Windows 发布包包含 Qt 6.12.0（其固定工作流指定版本）、FFmpeg、dav1d、libplacebo、OpenSSL、Opus、SDL3、sdl2-compat、SDL_ttf、Discord RPC、Microsoft Detours 相关 AntiHooking、Microsoft VC runtime 等。相关源码及构建脚本位于 Moonlight Qt 与 `MoonlightQtDeps` 固定提交及递归子模块内。Qt 的原始 GPLv3/LGPLv3 和 Qt 例外文本保留于 `licenses/Qt-*`；Qt 自带第三方代码仍按其原许可证授权。

Moonlight Qt dependencies 的 Windows FFmpeg 脚本启用动态库并保留原始补丁；FFmpeg 的 LGPL/GPL 选择取决于构建选项，许可说明和各文本均随源码保留，不能把所有 FFmpeg 构建一律描述成同一许可。libplacebo 为 LGPL-2.1-or-later，dav1d 为 BSD-2-Clause；SDL 系列为 zlib；FreeType、HarfBuzz、Vulkan 相关源码及许可通过递归子模块保留。完整声明见 `licenses/upstream/MoonlightQtDeps/`。

Microsoft VC runtime、Windows SDK/System DLL 和用户机器上的 WebView2 Runtime 是 Microsoft 组件，受 Microsoft 许可约束。GoWebView2 绑定及 WebView2 SDK 声明与 Runtime 本体许可不同。DD远程不把这些组件声明为自己拥有的 GPL 代码，也不限制用户替换动态链接的开源库以进行调试。

## Android

Android 是从修改后的 Moonlight 源码编译，包名 `io.ddremote.android`，并非修改已有第三方 APK。新增 DD 品牌、首页、连接说明、文件/剪贴板客户端、内嵌 tsnet 账号网络及设备发现；保留上游原作者版权。全部修改源码在 `apps/android/`，构建步骤见 `apps/android/BUILD-DDREMOTE.md`。

| 原生依赖 | 固定版本 / 来源 | 许可 |
| --- | --- | --- |
| Moonlight Common C | `874ac9548f1bd6f095ef2b435c42cdde460e7821` | GPL-3.0 |
| ENet | `aca87840b57f045a1f7f9299e4b1b9b8e2a5e2f1` | MIT |
| NanoRS | `b1e3c22ca0cdc0bb83e3cd6ed1a2fc77869ed99a` | MIT |
| OpenSSL | 上游预编译 `libcrypto.a`，头文件显示 4.0.2 | Apache-2.0 |
| Opus | Moonlight 固定提交内四种 ABI 的 `libopus.a` | BSD；版本字符串仅为 `libopus unknown` |
| SDL 摘录 | 上游 `minisdl.c` 等 | zlib |

OpenSSL/Opus 静态库来源由上游 `Build.txt` 指向 https://github.com/cgutman/moonlight-mobile-deps 的 AppVeyor 构建；没有提供对应二进制的精确构建提交。文件哈希分别在 `licenses/android-openssl-artifacts.json` 和 `licenses/android-native-artifacts.json`。本交付明确记录这一溯源限制，未声称验证了它们逐字节可重建。

Android 实际解析的 Java/Kotlin runtime 版本为：Kotlin stdlib 2.2.21、JetBrains annotations 13.0、BC provider 1.85.2、BC PKIX/util 1.85、JCodec 0.2.5、OkHttp 5.5.0、Okio 3.18.1、AndroidX annotation 1.10.0、startup 1.2.0、tracing 1.0.0、JmDNS 3.6.3、SLF4J 2.0.7、ShieldControllerExtensions 1.0.1。构建还使用 desugar_jdk_libs 2.1.5（GPLv2 with Classpath Exception）。完整 Gradle 解析报告在 `apps/android/build-evidence/runtime-dependencies.txt`。POM 与包内 notices 保存在 `licenses/android/`。

APK 内“关于与开源许可”显示 GPL 以及 `android-third-party-notices.txt` 中的完整许可文本。Android 内嵌 Tailscale tsnet 1.104.7，通过 Go c-shared/JNI 提供应用自己的网络连接；不要求另装 Tailscale App。`apps/android/network/go.mod`、`go.sum` 锁定依赖，`licenses/android-go-modules.json` 记录 Android arm64 tsnet 依赖，源码归档包含这些 Go 模块。Tailscale 账号服务条款独立于开源客户端代码许可证。

## 源码交付与复现

`scripts/fetch-sources.ps1` 固定到上述 Git 提交，验证 HEAD 并递归检出、验证所有子模块；归档时保留源码、原许可证、构建脚本和补丁。每个源码包旁边有 SHA-256 文件和 Git/submodule 清单。不要用 GitHub 自动生成的单仓库 ZIP 替代递归子模块包。

源码脚本要求 PowerShell 7、Git、Python 3.10+，收集 Go 模块时使用 Go 1.27.4。`archive-source.py` 使用 Python tarfile 处理 Windows 长路径；没有调用上游构建脚本或安装服务。

```powershell
# 获取固定上游仓库及全部递归子模块，归档修改后的 DD 工程和 Go 模块源码
./scripts/fetch-sources.ps1 -IncludeGoModules

# 仅重新生成当前修改后的 DD 工程源码包
./scripts/fetch-sources.ps1 -SkipUpstream
```

默认输出 `dist/source/`：

- `DDRemote-0.2.0-preview-source.tar.gz`：修改后的 Windows/Android 工程、许可证、文档和构建脚本；包含 Android 所用 JNI 源码与上游预编译静态库。
- 各 `*-source.tar.gz`：固定上游仓库及全部递归子模块。
- `DDRemote-go-module-sources.tar.gz`：`go.mod` 选择版本的模块源码 ZIP、module metadata、Go checksum 和 SHA-256。

源码脚本刻意只打包 `apps/`、`scripts/`、`docs/`、`LICENSE`、`README.md` 和 `.gitignore`，排除临时构建目录、签名密钥、状态文件、EXE/APK、Windows 载荷 ZIP。UU 逆向研究材料位于 `research/`，完全不进入交付源码或程序。

构建前按 `payload/*.source.json` 获取并验证原版 Sunshine/Moonlight Windows ZIP，然后执行 Windows 构建脚本；Android 按其 BUILD-DDREMOTE 文档构建。上游原始 UI、商标、版权声明保留在被嵌入的上游程序中，DD远程不声称其为原创。

当前源码包涵盖 DD 修改、Go 模块与上游仓库/子模块。上游 Windows release 使用的 MSYS2 系统包精确版本清单及 Qt SDK 完整供应链、Android 预编译 Opus 的精确构建版本，未能从所发布元数据完整恢复；不能把这份包表述为所有上游二进制的逐字节可重建证明。向公众再分发时，应同步提供相应上游构建的完整对应源码及必要的系统包/SDK来源，并继续保留各组件 notices。本地交付中记录了已获取来源与这一明确限制。

## 许可文件组织

`licenses/` 中保留收到的许可原文，`licenses/upstream/` 按上游源码路径保存子模块 notices，`licenses/go/` 按 Go 模块保存 notices，`licenses/android/` 保存实际 Maven 坐标的 POM 和包内文本。参考项目 airhand 的 MIT 文本仅用于来源记录。原始许可内容适用于相应组件；构建工具与未进入目标平台的模块可能一并列出。
