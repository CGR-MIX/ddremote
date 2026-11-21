# DD远程 0.3.3 UU 适配试验版

安卓基于官方 UU 远程 4.48.1 APK 重建，保留原 DEX、资源、InvisibleEditText、StreamerSurfaceView 和完整键盘资源。DD 提供账号入口和远控工具栏；连接 Sunshine 仍使用 Moonlight 的协议及音频库，不加载 Moonlight Game 页面。它并非完整照搬 UU ScreenActivity。

本次产物：
- DDRemote-Windows-0.3.3-super-screen-lab.exe
- DDRemote-UU-0.3.3-super-screen-lab.apk（包名 io.ddremote.uuadapter，versionCode 4，可与旧 io.ddremote.android 共存）

已验证：Android16 模拟器安装；原 UU 输入法组件与输入/触控路由断言；真实 Sunshine 720p90帧→1080p93帧、画质重连、启动取消和释放；Windows 原有测试与新增布局恢复/身份/账号租约测试；760px 界面与退出账号回归。测试过程出现 UDP 丢包和 RTSP 截断，调整本地测试转发器缓冲/分段后最终通过，失败记录仍保留。

超级屏已接入手机工具栏和 Windows 辅助进程，但尚未通过真实驱动验收：只操作 DD 自己创建的 SWD\DDRemote 设备；按本次实例查找显示接口并核对实际绑定 INF；切换前持久化原布局；退出、租约失效、父进程退出、本地 Ctrl+Alt+Shift+F11 时尝试恢复，恢复失败保留记录并显示错误；重启电脑端后可点击“恢复电脑显示”重试。当前只接受720p/1080p/1440p、30/60Hz横屏，其中手机快捷画质仍提供720p/1080p。

当前没有安装虚拟屏驱动，也没有切换物理输出。一次性驱动安装脚本 scripts/prepare-super-screen-driver.ps1 已完成只读哈希/签名核验。使用 -Install 必须在管理员 PowerShell 运行，只暂存固定版本的签名驱动包，不创建显示器、不导入证书、不修改驱动签名策略。超级屏试验还要求电脑端以管理员身份运行。Windows 拒绝安装时停止，不绕过系统签名检查。驱动暂存在 DriverStore 会保留；会话创建的虚拟设备设计为随辅助进程关闭删除，真实行为待验收。

隐私遮罩延续0.3.2实现，但物理屏遮黑/远程仍可见的效果仍未验证，不可据此依赖其防窥。新 APK 的小米13真机、真实账号发现、云端退出移除、声音仍未重新验收。整体 UU 功能对齐目标未完成。

原可用0.2.1 APK与0.2.0 EXE保留，不覆盖。此版为本地试验产物。
