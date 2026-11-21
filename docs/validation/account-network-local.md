# 同账号网络本地集成验收

验收日期：2026-11-20。版本：DD远程 0.2.0-preview；Go 1.27.4；`tailscale.com v1.104.7`。

结果：`TestLocalAccountNetwork` 通过，测试用例耗时 4.27 秒，Go 测试进程报告 4.453 秒。两个节点使用真实 tsnet 用户态 WireGuard 和 LocalAPI，没有使用用户的 Tailscale 账号。

## 验证范围

测试位于 `apps/windows/account_network_integration_test.go`，默认不参与普通构建；必须同时启用 `ddnetworkintegration` 构建标签与 `DD_TEST_NETWORK=1`。

测试启动一个进程内 `testcontrol.Server`，配置 `AllNodesSameUser: true`，生成的测试账号 ID 为 `123`。DERP 服务监听本机回环地址，控制面 URL 使用 `httptest` 回环地址。两个节点均从生产 `newNetworkServer` 创建，检查 `Ephemeral: true`、`*mem.Store`、各自独立的临时目录与节点 ID，然后进入生产 `runNetwork`。

已实际运行的链路如下：

1. 两个节点注册到本地控制面，获得彼此的真实网络状态。
2. 两个节点经回环 DERP 转发的 WireGuard 通道双向访问 TCP 48201。
3. 生产 `networkAccountHandler` 对每次请求查询真实 LocalAPI `Status` 与 `WhoIs`，通过同账号校验后调用生产 `AccountManager.ServeHTTP`。
4. 生产 `AccountManager.Devices` 双向返回对方正确的节点 ID、IP、设备名、平台和版本；此测试未开启桌面共享，因此 `Ready` 为 false。
5. 生产 `NetworkManager.Logout` 成功完成真实 LocalAPI 注销，并同步停止该节点。本地账号、节点 ID、设备列表和临时目录已清理，后续 `DialContext` 被拒绝。

进程内禁用直接 UDP 和端口映射，以便数据走本地 DERP；不调用系统 Tailscale 服务，不读取其账号状态，不改变系统网络接口、路由、DNS、注册表或服务配置。节点使用显式虚构测试 AuthKey，覆盖环境中可能存在的 `TS_AUTHKEY`。这不是隔离沙箱或外网流量审计，但测试控制面和中继均固定为回环服务，未接入真实 tailnet。

## 不能据此声称的结果

本测试**不能证明 Tailscale 云端节点删除**。固定版本的 `tstest/integration/testcontrol/testcontrol.go` 中，`serveRegister` 未实现注册请求中的 `Ephemeral` 和 `Expiry` 注销删除语义；它仅在完成密钥轮换时删除旧 key 对应节点。成功调用 Logout 后，测试控制面仍保留两个节点，测试明确断言并记录这一能力边界，没有补写控制器删除逻辑。

`AllOnline: true` 是上游测试控制面提供的简化模型。因此本测试也不能证明真实控制面的离线检测、其他设备上的节点移除、互联网穿透、真实账号网页登录或端到端屏幕串流。这些范围需要另外验收。

## 复现

测试控制面依赖 6 个额外的间接测试模块：`hyperloglog`、`immutable`、`reco`、`go-metro`、`hashtriemap`、`intmap`。版本来自固定 Tailscale 模块图，已存在于生产工程 `go.sum`。为避免改变产品依赖，使用临时 `-modfile` 副本解析这些测试依赖；不修改 `apps/windows/go.mod` 或 `go.sum`。

在仓库根目录的 PowerShell 中运行：

```powershell
$testModule = Join-Path ([IO.Path]::GetTempPath()) ('ddremote-network-test-' + [guid]::NewGuid().ToString('N') + '.mod')
Copy-Item -LiteralPath apps/windows/go.mod -Destination $testModule
Copy-Item -LiteralPath apps/windows/go.sum -Destination ([IO.Path]::ChangeExtension($testModule, '.sum'))
$previousTestNetwork = $env:DD_TEST_NETWORK
try {
    $env:DD_TEST_NETWORK = '1'
    go -C apps/windows test -mod=mod "-modfile=$testModule" -tags=ddnetworkintegration -run '^TestLocalAccountNetwork$' -count=1 -timeout=60s -v
    if ($LASTEXITCODE -ne 0) { throw 'Local account network test failed' }
} finally {
    $env:DD_TEST_NETWORK = $previousTestNetwork
}
```

首次解析可能下载测试依赖。测试执行上下文最多 50 秒，Go 测试硬期限 60 秒；临时 `.mod`/`.sum` 副本留在系统临时目录供检查，不含任何登录凭据。测试自身创建的节点目录在正常注销和清理时移除。

成功输出中的关键证据：

```text
PASS: two real ephemeral mem.Store tsnet nodes discovered each other through production networkAccountHandler, fresh LocalAPI WhoIs and AccountManager.Devices
PASS: real LocalAPI Logout completed; DD listeners, identity, devices and temporary directory were cleared
LIMITATION: pinned testcontrol retains both nodes because it does not implement ephemeral logout deletion. This test does not verify Tailscale cloud deletion or peer removal from a cloud map.
--- PASS: TestLocalAccountNetwork (4.27s)
ok      ddremote/windows 4.453s
```
