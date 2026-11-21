# DD远程 UI 开发交接

交接基线：2026-11-21。本次交接用于 Windows 与 Android 的界面开发，现有业务代码和协议是行为依据。先选择需要修改的平台，使用 `ui/<工作内容>` 分支提交 PR，并在 PR 中附界面截图和验证结果。

## 1. 当前项目与版本

DD远程用于 Windows / Android 远程桌面、设备发现，以及文件和文字传输。串流基于 Sunshine / Moonlight；账户网络内嵌 Tailscale。

| 部分 | 当前入口 | 交接说明 |
| --- | --- | --- |
| Windows | `apps/windows` | Go + WebView2；源码版本 `0.3.3-super-screen-lab` |
| Windows 界面 | `apps/windows/web/index.html` | HTML、CSS、原生 JavaScript 同文件；由 Go embed 嵌入，不需要前端打包器 |
| 独立源码 Android | `apps/android` | 基于 Moonlight Android 的 Java / XML 工程，包名 `io.ddremote.android`；构建以 `BUILD-DDREMOTE.md` 为准 |
| Android 适配实验 | `experiments/uu-android` | DD 自己编写的账号首页、串流界面、输入和工具栏代码；最新实验交付记录为 `0.3.4`，包名 `io.ddremote.uuadapter` |

`apps/android` 和 `experiments/uu-android` 是两条不同路线，修改前在 PR 中写清目标，避免改完旧客户端却误认为改到了实验版。根目录 README 的 0.2.x 功能说明保留为历史基线；试验版范围见本文件和 `experiments/uu-android/DELIVERY-0.3.4.zh-CN.md`。

## 2. Windows UI 从这里开始

直接在浏览器打开 `apps/windows/web/index.html` 可以查看静态布局。也可以从仓库根目录运行：

```powershell
python -m http.server 8080 --bind 127.0.0.1 --directory apps/windows/web
```

访问 `http://127.0.0.1:8080`。浏览器没有 Go 注入的桥，因此会提示等待本机服务，登录和设备列表不能据此判定可用。需要查看已登录等状态时，参考 `scripts/verify-windows-ui.cjs` 的 `page.addInitScript` fixture；正式页面不要内置虚假成功结果。

界面主要区域：我的设备 / 账户与本机接收、文件与剪贴板、设置与帮助，以及高级手动连接。可以调整布局、字体、颜色、图标、按钮层级、空态和错误态。优先保留已有元素 ID、`data-page` / `data-goto` 和事件行为；如果确需改 ID，同时更新脚本和回归用例。

### 原生桥约定

浏览器通过 `window.ddInvoke(JSON.stringify({id, action, payload}))` 发请求；Go 通过 `window.ddResolve({id, ok, data, error})` 回传结果。关联 ID 和异步完成顺序必须保留。完整请求分派位于 `apps/windows/main.go` 的 `handle`，页面封装为 `rpc`。

| 操作组 | 现有 action |
| --- | --- |
| 状态与账号 | `status`、`accountDevices`、`accountLogin`、`accountConnect`、`accountLogout` |
| 接收与显示恢复 | `startHost`、`stopHost`、`privacyStatus`、`privacyStop`、`superScreenStatus`、`superScreenStop` |
| 高级网络与手动配对 | `networkStart`、`networkLogin`、`networkStop`、`pair`、`client`、`clientPair` |
| 文件与文字 | `files`、`link`、`copy`、`revoke`、`clipboardGet`、`clipboardSet`、`openFolder` |
| 设置与诊断 | `saveSettings`、`wake`、`lock`、`openLogs`、`openNotices`、`openSunshine` |

参数和返回值以现有 Go 实现为准。例如 `accountConnect` 的 payload 为 `{id: 设备ID}`；不要把 UI 请求 ID 当成设备 ID。`status` 包含 `host`、`network`、`transfer`、`privacy`、`superScreen` 等实际状态，不能仅凭按钮点击就显示已连接。

### 完整 Windows 构建

需要 x64 Windows、PowerShell 7、Go 1.27.4 和 Edge WebView2 Runtime。Go 版本以 `apps/windows/go.mod` 为准。首次克隆不包含 Sunshine / Moonlight 二进制载荷，需要下载并校验：

```powershell
pwsh -File scripts/download-payload.ps1 -Component sunshine
pwsh -File scripts/download-payload.ps1 -Component moonlight
Copy-Item apps/windows/payload/sunshine.parallel.zip apps/windows/payload/sunshine.zip
Copy-Item apps/windows/payload/moonlight.parallel.zip apps/windows/payload/moonlight.zip
pwsh -File scripts/build-windows.ps1
```

下载脚本按 `apps/windows/payload/*.source.json` 的固定版本与 SHA-256 校验，生成 `.parallel.zip`，所以首次下载后需要上面的复制步骤。构建产物为 `dist/DDRemote-Windows-x64.exe`。构建脚本默认运行 Go 测试；只改 HTML 也需要重新构建 EXE 才能刷新内嵌页面。

单独执行后端测试：

```powershell
go -C apps/windows test ./... -count=1 -timeout=90s
```

已有 Windows UI 回归脚本使用 Playwright。仓库当前没有 npm 工程或锁定的前端依赖，按需在本地安装：

```powershell
npm install --no-save --package-lock=false playwright
npx playwright install chromium
node scripts/verify-windows-ui.cjs
```

此脚本使用模拟桥验证连接期间仍可退出、迟到结果不恢复旧会话、退出错误仍清理旧界面，以及 760px 无横向溢出；不验证真实账户、串流或云端删除。

## 3. Android UI 从这里开始

### 可独立构建的源码客户端

查看 `apps/android/BUILD-DDREMOTE.md`。优先入口：

- `app/src/main/java/com/limelight/ddremote/DdHomeActivity.java`：账号设备首页。
- `app/src/main/java/com/limelight/ddremote/DdUi.java`：DD 页面样式与控件辅助函数。
- 同目录的文件传输等 Activity，以及 `app/src/main/res` 的布局、字符串和样式。
- `app/src/main/java/com/limelight/Game.java`：Moonlight 串流页；涉及输入、解码和会话，改动要单独验证。

此仓库将现有 Android 及其已检出的原生子模块源码完整纳入主仓库，保留上游许可和 `UPSTREAM.json`。克隆主仓库即可取得这些源码，不需要依赖交接机器上的嵌套 Git 仓库。Android 内部保留的 `.gitmodules` 是来源记录，主仓库不使用 gitlink 引用这些目录。

### 0.3.4 Android 适配实验

代码路径位于 `experiments/uu-android/src/com/limelight/ddremote/`：

- `UuAdapterHome.java`：账号登录、状态、设备列表和首页控件。
- `UuStreamActivity.java`：远程画面、工具栏、手机键盘、完整键盘面板、画质和触控选项。
- `StreamPreferences.java`：画质、触控、速度和静音偏好。
- `TouchRouter.java`、`SunshineInput.java`、`UuTextInputAdapter.java`：触摸及输入逻辑；视觉修改时特别注意焦点和输入法生命周期。

该路线仍依赖本地提供的原始 UU APK、原始资源和构建工具。本仓库不包含这些第三方提取内容，因此不能宣称克隆后可独立构建此实验 APK。依赖和构建方式见该目录 README、`dependencies.json` 与 `THIRD_PARTY_NOTICES.md`。如果需求是让任意 UI 开发者直接构建 Android，先使用 `apps/android`；实验路线所需依赖由项目负责人另行提供，并确认使用权限。

## 4. 必须保留的交互

1. 登录账号不会自动打开接收；开始 / 停止接收由用户明确操作。
2. 连接进行中仍允许退出。退出后清空旧设备、连接链接和文字等会话数据；迟到回调不能恢复旧 UI 或启动下一段连接。
3. 账号退出未确认云端移除时，保留真实错误提示；不能仅因本地断开就显示云端已删除。
4. 文件链接包含授权凭据；只由明确操作复制，不记录到日志或截图。文字剪贴板只由按钮读取 / 写入。
5. 隐私遮罩与超级屏保留停止和恢复入口；不要为截图自动执行锁屏、驱动安装或显示器切换。
6. Android 保留中文 / emoji 组合输入、键盘弹出后画面缩小与收起恢复、完整键盘可滚动、触控板 / 直接触控、音量键和静音行为。
7. 画质切换需要等待旧连接释放。不得为了让界面显得流畅而忽略取消、断开和会话代次检查。

## 5. UI 交付验收

- Windows 在 1050×800 与 760×650 检查布局；账号登录前、设备列表为空、设备在线 / 离线、连接中、接收启动失败、退出失败均有对应展示。
- Android 检查竖屏首页、横屏串流、输入法展开 / 收起、长文案、大字体、系统返回和工具栏展开；触控区域不能遮住必要的远程内容。
- 提交截图或录屏、启动命令、验证环境、涉及文件和测试结果。截图使用测试账号与虚构设备名。
- 运行涉及平台的现有测试；将静态 UI / 模拟桥结果与真实设备连接结果分开写。
- 物理显示器黑屏、虚拟屏输出与恢复、真实账号跨网连接及云端删除仍有待验收项。具体见最新交付记录；不要将已有按钮或模拟器画面当成真机通过。

## 6. 仓库范围与协作

仓库包含 DD 源码、Android 上游源码及许可证、构建和测试脚本，以及 DD 编写的实验适配源码。`research/`、`.tools/`、`dist/`、运行状态、签名密钥、原始 APK、浏览器截图和本地实验日志不提交；部分历史文档指向这些本地证据的链接在克隆后不可访问。

原有 `apps/android/.git` 与子模块元数据只留在交接机器本地，主仓库跟踪的是源码文件。新增文件不要提交凭据、签名材料或第三方私有资源。

分支示例：`ui/windows-device-page`、`ui/android-toolbar`。按页面或平台提交小 PR，说明视觉变更及任何交互变更。尚未指定外部开发者的 GitHub 用户名，因此交接任务先保持未分配；仓库负责人添加协作者后即可接手。
