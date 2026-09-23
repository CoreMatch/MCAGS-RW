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

## Log Evidence
Pending

## Verification Conclusion
Pending
