# Windows 网络与账号边界验证

执行环境：Windows，Go 1.27.4。自动测试只使用进程内模拟身份与本机 TCP/UDP 服务，不代表真实 Tailscale 账号、跨 NAT、Android 实机或硬件编码验证。

执行：`go -C apps/windows test ./... -count=1 -timeout=60s`。

已覆盖：

- TCP 双向传输及 half-close，UDP 数据报边界、空数据报、大包、空闲超时和取消。
- 监听失败时回收已打开端口；不转发 Sunshine 管理端口 47990。
- 默认不开启接收；账号登录本身不暴露本机串流；明确开启后才转发。
- `SetReceiving(false)` 返回前关闭监听和活跃连接；后续在线状态刷新不重新打开。
- 同账号设备列表排除其他 UserID、sharee、共享节点和 tagged 节点；未登录不返回旧账号设备。
- 账号控制服务仅使用 tsnet TCP 48201；逐请求读取真实 backend Status、WhoIs(RemoteAddr)，忽略伪造身份和转发地址头。
- 对端不同 UserID、tagged/sharee/shared、未知身份、资料不一致，以及本机登出/未知/tagged/shared身份均拒绝。
- 连接复用时重新校验身份；撤销 account handler 后请求被拒绝。
- 首次配对只改 HTTP 47989 上 `/pair?phrase=getservercert` 的 devicename，追加128位随机标识；保留 uniqueid、其他参数及其编码顺序。
- 后续不含 phrase 的配对阶段及 pairchallenge 原样透传；重复 phrase 或损坏 query 拒绝。
- 配对反代只连接所选目标；TLS 47984仍保持原始字节通道，不终止或伪造证书。
- 默认临时节点使用 `Ephemeral:true` 和 `mem.Store`，身份写入只进入内存；旧持久节点文件不被读取、更改或转换。
- 独立临时目录只用于 tsnet 诊断缓存，退出时回收；清理函数拒绝非 DD 临时目录。
- 网络运行时拒绝切换身份模式；停止时先请求服务器注销再关闭网络。
- 注销失败仍断开，明确报告未确认立即移除；注销请求有截止时间。

实际账号身份、服务器端设备删除与跨机串流，需要使用真实账号和第二台设备验证，不能根据上述 mock 测试宣称已验证。
