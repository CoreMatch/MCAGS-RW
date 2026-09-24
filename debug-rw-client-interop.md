# Debug Session: rw-client-interop
- **Status**: [OPEN]
- **Issue**: 真实铁锈战争客户端与当前 Go 服务端的房间期交互存在兼容性不确定性，需要围绕连接、房间展示、权限控制和开局行为做实机联调修正。
- **Debug Server**: http://127.0.0.1:7777/event
- **Log File**: .dbg/trae-debug-log-rw-client-interop.ndjson

## Reproduction Steps
1. 启动当前 Go 服务端。
2. 使用真实铁锈战争客户端连接服务端。
3. 观察建房/入房、房间展示、聊天命令、房主/管理员控制、开局按钮等行为。

## Hypotheses & Verification
| ID | Hypothesis | Likelihood | Effort | Evidence |
|----|------------|------------|--------|----------|
| A | 房间期关键包体字段不完整，导致客户端 UI/控制行为没有真正生效 | High | Low | Pending |
| B | 聊天命令链路正常，但客户端不会把这些操作映射为官方房间行为 | Medium | Low | Pending |
| C | 开局按钮触发的包型正确但包体缺少上下文，导致状态不同步 | High | Medium | Pending |
| D | TEAM_LIST 中权限标记落点不对，客户端未按预期识别房主/管理员 | Medium | Low | Pending |
| E | 玩家已完成入房，但在开局前后断开，或客户端切到本地流程，导致服务器侧看起来“游戏中没人在线” | High | Low | Pending |

## Log Evidence
- [L1-L16](file:///home/lnb/MCAGS-RW/.dbg/trae-debug-log-rw-client-interop.ndjson#L1-L16): 一次完整的连入与入房链路，包含 `160 -> 110 -> 118 -> join room success -> 170/161/106/115`
- [L17-L32](file:///home/lnb/MCAGS-RW/.dbg/trae-debug-log-rw-client-interop.ndjson#L17-L32): 第二次复现，结果相同
- [L33-L48](file:///home/lnb/MCAGS-RW/.dbg/trae-debug-log-rw-client-interop.ndjson#L33-L48): 第三次复现，结果相同
- [L49-L80](file:///home/lnb/MCAGS-RW/.dbg/trae-debug-log-rw-client-interop.ndjson#L49-L80): 后续再次复现，仍然能成功入房
- 当前缺失证据：没有看到 `START_GAME`、心跳或对局期后续包，需第二轮打点确认断开时机
- [L1-L21](file:///home/lnb/MCAGS-RW/.dbg/trae-debug-log-rw-client-interop.ndjson#L1-L21): 第二轮打点显示玩家完成入房后收发过一次心跳 `108/109`，随后服务端立刻收到 `EOF` 并关闭会话
- [job-83be output](file:///tmp/trae-agent-toolhost-1000/jobs/job-83be28c54afd4c8787d894e8eb9f0eda/output.log): 服务端只记录一次连接和一次断开，没有进入持续对局通信
- [post-fix L11-L19](file:///home/lnb/MCAGS-RW/.dbg/trae-debug-log-rw-client-interop.ndjson#L11-L19): 服务端构造的房间槽位映射是 `slot0=human(lnb), slot1=ai(AI)`，但客户端现象仍与上一轮相同，说明客户端误解析发生在包编码层而非房间状态层
- [post-fix L16-L18](file:///home/lnb/MCAGS-RW/.dbg/trae-debug-log-rw-client-interop.ndjson#L16-L18): `TEAM_LIST` 下发后，客户端在 active 状态又发来一次 `REGISTER_PLAYER(110)`，提示当前 dedicated 房间链路仍有未满足的协议预期

## Verification Conclusion
第一轮证据表明，用户“没有连接进入服务器”的结论不成立。更准确的现象是：客户端成功连接并完成入房。第二轮证据进一步确认，客户端在入房后仅保留了一次心跳往返，然后立即关闭连接，因此服务器侧自然认为玩家并未持续在线参与对局。当前最可疑方向是：现有实现实际上走的是偏 relay/房主本地承载的链路，而不是 dedicated-server 持续连接链路。

## Iteration Notes
- 已按用户决策去掉 `RELAY_BECOME_SERVER(170)`，并切换到 `post-fix` 观察。
- 本轮尚未完成 `post-fix` 复现验证，原因不是连接链路本身，而是当前房间行为更接近服务器房间后，用户无法再通过原来的方式手动添加 AI。
- 用户提出新的验证配套需求：当房间内只有一个真人玩家时默认添加 1 个 AI；当第二个真人玩家进入时自动移除该 AI。
- 已实现自动 AI 验证规则：单真人房间自动补 1 个 AI，并在第二个真人进入时立刻移除。
- 新出现的 `post-fix` 症状：客户端侧把真人玩家名字显示成了 `AI`，需要继续验证 `TEAM_LIST` 中的人类/AI 槽位映射是否写错。
- 最新证据表明：服务端内部槽位映射并未把真人覆盖成 AI，客户端误显示更可能源自 `TEAM_LIST` 或相关房间同步包的字段布局不兼容。
