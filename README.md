# 我的世界加速ip

## 如何运行
1. [下载release](https://github.com/sduoduo233/go-mcproxy/releases/latest)
2. 执行 `./mcproxy`

## 参数说明
```
Usage of ./mcproxy:
  -config string
        path to config.json (default "config.json")
  -control string
        control panel address (default "127.0.0.1:8080")
  -balancer string
        load balancer address (e.g., "0.0.0.0:25565")
```

`-config` 配置文件路径

`-control` 控制面板监听地址，默认为 127.0.0.1:8080

`-balancer` 负载均衡器监听地址，例如 "0.0.0.0:25565"。启用此选项将自动在所有代理之间进行负载均衡

## 配置文件说明

现在支持在一个配置文件中配置多个代理，每个代理可以有不同的监听地址、目标服务器和其他设置。

### 多代理配置 (新格式)

```json
{
    "proxies": [
        {
            "listen": "0.0.0.0:25565",
            "description": "Proxy 1\nHypixel Server",
            "remote": "mc.hypixel.net:25565",
            "local_addr": "192.168.1.2:0",
            "max_player": 20,
            "ping_mode": "fake",
            "fake_ping": 0,
            "rewrite_host": "mc.hypixel.net",
            "rewrite_port": 25565,
            "auth": "none",
            "whitelist": [
                "L1quidBounce"
            ],
            "blacklist": []
        },
        {
            "listen": "0.0.0.0:25566",
            "description": "Proxy 2\nAnother Server",
            "remote": "play.example.com:25565",
            "local_addr": "",
            "max_player": 50,
            "ping_mode": "fake",
            "fake_ping": 10,
            "rewrite_host": "play.example.com",
            "rewrite_port": 25565,
            "auth": "whitelist",
            "whitelist": [
                "Player1",
                "Player2"
            ],
            "blacklist": []
        }
    ]
}
```

### 单代理配置 (旧格式，向后兼容)

```json
{
    "listen": "0.0.0.0:25565",
    "description": "hello\nworld",
    "remote": "mc.hypixel.net:25565",
    "local_addr": "192.168.1.2:0",
    "max_player": 20,
    "ping_mode": "fake",
    "fake_ping": 0,
    "rewrite_host": "mc.hypixel.net",
    "rewrite_port": 25565,
    "auth": "none",
    "whitelist": [
        "L1quidBounce"
    ],
    "blacklist": []
}
```

`listen`: 服务器监听地址

`description`: MOTD

`remote`: 反向代理的源服务器

`local_addr`: 指定用于出站连接的本地地址（用于多网卡配置，特别是在Windows系统上）。格式为"IP:端口"，端口可以设为0让系统自动分配。留空则使用系统默认网卡。

`max_player`: 最大玩家

`ping_mode`: 相应 ping 的方法，可以是 `real`（真实延迟），或 `fake`（假延迟）

`rewrite_host`：修改客户端发送的服务器地址（可以用来绕过 Hypixel 的地址检测）

`rewrite_port`：修改客户端发送的服务器端口

`auth`：用户名认证，可以是 `none`, `blacklist` 或 `whitelist`

## 负载均衡和连接限制

go-mcproxy 现在支持负载均衡和连接限制功能，可以更有效地管理多个代理和连接。

### 负载均衡

使用 `-balancer` 参数可以启用负载均衡功能，例如：

```
./mcproxy -balancer 0.0.0.0:25565
```

负载均衡器会自动将新连接分配到负载最低的代理服务器，确保资源得到最佳利用。负载均衡器现在直接使用代理的网络接口连接到远程服务器，无需通过本地转发，提高了性能和效率。

### 动态代理切换

负载均衡器会根据每个代理的当前连接数动态选择最佳代理，无需客户端进行任何配置更改。每个连接都会直接使用选定代理的网络接口，确保最佳的网络路由。

### 连接限制

为了防止单个IP占用过多资源，每个公网IP最多允许4个同时连接。当达到此限制时，新的连接请求将被拒绝。

## 控制面板

go-mcproxy 提供了一个简洁而功能强大的网页控制面板，可以用来监控和管理代理服务器。

### 访问控制面板

启动 go-mcproxy 后，可以通过浏览器访问控制面板，默认地址为 http://127.0.0.1:8080

如果需要从其他设备访问控制面板，可以使用 `-control` 参数指定监听地址，例如：

```
./mcproxy -control 0.0.0.0:8080
```

### 控制面板功能

控制面板提供以下功能：

1. **系统概览**：显示总连接数和每IP连接限制。

2. **代理状态监控**：显示每个代理的监听地址、远程服务器、描述、公网IP、状态、当前连接数和容量。

3. **连接管理**：查看和管理所有活动连接，包括用户名、客户端地址、代理地址、远程服务器、公网IP和连接时间。可以断开特定连接。

4. **配置修改**：可以直接在控制面板上修改代理配置，包括监听地址、远程服务器、本地地址、描述、最大玩家数、ping模式等。

5. **配置重载**：修改配置后，可以点击"重载配置"按钮使更改立即生效，无需重启程序。

控制面板会自动保存修改后的配置到配置文件，并优化配置文件的存储格式。控制面板的界面经过改进，更加美观和易用。
