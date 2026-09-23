# rwgin

一个用 Go + Gin 编写的铁锈战争多房间服务端原型。

当前阶段目标是“最小联机闭环”：

- 独立的二进制游戏端口
- Gin 管理接口
- 多房间创建/加入
- 基础握手
- 房间聊天
- 心跳响应
- 房间列表与队伍列表下发

## 运行

```bash
go run ./cmd/rwgin
```

默认端口：

- 游戏协议：`:5123`
- 管理接口：`:8080`

## 管理接口

- `GET /healthz`
- `GET /api/rooms`
- `POST /api/rooms`

创建房间示例：

```json
{
  "code": "GWTEST",
  "title": "测试房间",
  "maxPlayers": 8
}
```

## 环境变量

- `RWGIN_GAME_ADDR`
- `RWGIN_HTTP_ADDR`
- `RWGIN_SERVER_ID`
- `RWGIN_RELAY_ID`
- `RWGIN_ROOM_PREFIX`
- `RWGIN_DEFAULT_MAP`
- `RWGIN_DEFAULT_ROOM`
- `RWGIN_MAX_PLAYERS`
- `RWGIN_CLIENT_VERSION`
- `RWGIN_INCOME`
- `RWGIN_MAX_UNITS`

## 说明

这个版本只覆盖大厅/房间阶段的最小协议行为，还没有实现完整对局同步、转发或帧驱动游戏逻辑。
