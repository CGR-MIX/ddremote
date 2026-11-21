# DD远程 0.2.0-preview 验证记录

日期：2026-11-20，Asia/Hong_Kong。此文区分本机真实组件运行、自动测试和仍需真实账号/设备的验收，不将预览版称为完整UU替代品。

## Windows

- 构建命令：`scripts/build-windows.ps1`。Go 1.27.4，windows/amd64，内嵌Sunshine、Moonlight Qt与tsnet。文件SHA256保存在`dist/DDRemote-Windows-x64.exe.sha256`。
- 生产代码与生命周期修订后的`go test ./... -count=1 -timeout=90s`通过。测试使用隔离目录和本机服务，不使用用户账号。
- 实际WebView2应用已查看“我的设备”及“文件与剪贴板”，账户/接收状态通过原生桥接返回。未登录时设备列表为空，桌面与文件接收未自动开启。独立布局检查覆盖1050px与760px宽度。
- `scripts/verify-windows-ui.cjs`使用生产HTML与受控桥接夹具，在真实Edge中点击控件：连接等待期间可退出、迟到连接结果不覆盖退出状态、云端注销未确认时显示错误并清空旧会话文字；760px没有横向溢出。夹具不是登录或远控的真实证据。
- 2026-11-20 04:18实际Sunshine与portable Moonlight自动首次配对通过，用时32.28秒。经生产TCP/UDP代理，nonce绑定配对请求，保存客户端证书后再次mTLS认证通过。退出后真实Sunshine状态中当前及旧格式客户端配对记录均为空，主机UUID保留。
- 03:19的本机串流记录为1280×720、H.264、目标30fps，约12秒实际视频帧；接收与解码30.38fps，渲染30.11fps，收到音频首包。没有注入鼠标或键盘输入。这是同机环回生产代理链路，不能证明异地网络或真实tailnet账号链路。
- Windows剪贴板显式本机写入/回读已通过，内容包含中文及emoji；测试恢复原有文字。未宣称自动剪贴板同步。

## 退出和授权清理

`lifecycle_test.go`覆盖：退出先取消正在进行的会话操作，再清空设备缓存；旧文件令牌被拒绝；控制端会话目录删除；Sunshine新旧两种配对记录删除且保留其他状态；异常退出后重开清理旧授权。

使用Windows独占文件句柄制造真实删除失败：退出报告失败，下次会话新建目录，不复用上一账号的私钥。桌面授权记录损坏时拒绝开启接收。控制端结束等待有时限，即使窗口退出失败也继续清理其他授权。

`network_test.go`另验证ephemeral+内存身份、退出先发注销请求、失败仍断开、注销超时及不夸大云端删除成功。**真实Tailscale账号云端删除仍未验证。**

两个本机真实tsnet节点经WireGuard与回环DERP完成同账号双向发现和生产WhoIs身份校验。生产Logout后本地身份、列表、连接与临时目录清理通过，集成测试4.453秒。上游testcontrol没有实现ephemeral注销删除，仍保留两条节点记录，不能证明云端删除或对方列表移除。完整范围和命令见`account-network-local.md`。

## Android与互通

- 最低API21，包含arm64-v8a、armeabi-v7a、x86、x86_64四种ABI；内嵌tsnet，不要求额外Tailscale应用。
- release APK经assemble与lint、zipalign和apksigner检查，证据在`apps/android/build-evidence/`。APK SHA256与最终构建变化以`dist`旁校验文件为准。
- 生产Java文件协议测试通过14项断言。实际Windows Go文件服务与生产Java客户端本机联测5/5：TLS指纹及Bearer、中文文件上传200KB、下载内容一致、剪贴板API、错误指纹拒绝。
- 最新Android退出修订通过9项生产Go测试、JVM会话代次测试和100次并发退出竞争；退出取消长请求、拒绝旧会话启动游戏或写入凭据、校验设备StableID、限制关闭等待。release lint为0错误、194警告，未将上游兼容/资源警告视为真机验证。
- 没有连接Android真机或启动模拟器。JNI加载、系统证书、触控、音画、后台生命周期以及登录/发现/配对的完整手机流程仍需真机验收。

## 尚未完成

真实账号登录及云端删除、不同网络双机串流、Android实际画面和输入、断线恢复；4K/144fps/HDR/4:4:4与手柄；UU虚拟屏、防窥、无线副屏、完整多屏并发、自动解锁、跨网唤醒辅助节点、文件夹/断点续传等。完整范围见`docs/feature-parity.md`。

源码归档只收录开源项目与DD代码。UU安装包、提取资源、用户测试状态、账号凭据及APK私有签名密钥不进入交付源码。

## 最终安装包校验

| 文件 | 字节数 | SHA256 |
| --- | ---: | --- |
| DDRemote-Windows-x64.exe | 100016640 | `fb53d9472b46eb499b2f659e666b87cfc36b79f022f16bf80c46d411d004816b` |
| DDRemote-0.2.0-preview-android.apk | 44080501 | `13b37297aa89a2b602b56b9421586079fcbad20c24b1574f3639631cf097c7d1` |

Windows产物未做商业代码签名。Android包名`io.ddremote.android`，版本码2，v1/v2/v3签名通过，16KB对齐通过；签名证书SHA256为`0917f9d1eedfdfa810edbeb0699117a3eb65dc545c05dbd127f6a12be5f20ae1`。
