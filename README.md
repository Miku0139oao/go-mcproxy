## 參數說明
```
Usage of ./mcproxy:
  -config string
        path to config.json (default "config.json")
  -control string
        control panel address (default "127.0.0.1:8080")
  -balancer string
        load balancer address (e.g., "0.0.0.0:25565")
```

`-config` 配置文件路徑

`-control` 控制面板監聽地址，預設為 127.0.0.1:8080

`-balancer` 負載均衡器監聽地址，例如 "0.0.0.0:25565"。啟用此選項將自動在所有代理之間進行負載均衡

## 配置文件說明

現在支援在一個配置文件中配置多個代理，每個代理可以有不同的監聽地址、目標伺服器和其他設定。

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
            "whitelist": [],
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

### 單代理配置 (舊格式，向後相容)

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

`listen`: 伺服器監聽地址

`description`: MOTD

`remote`: 反向代理的源伺服器

`local_addr`: 指定用於出站連接的本地地址（用於多網卡配置，特別是在Windows系統上）。格式為"IP:連接埠"，連接埠可以設為0讓系統自動分配。留空則使用系統預設網卡。

`max_player`: 最大玩家

`ping_mode`: 相應 ping 的方法，可以是 `real`（真實延遲），或 `fake`（假延遲）

`rewrite_host`：修改客戶端發送的伺服器地址（可以用來繞過 Hypixel 的地址檢測）

`rewrite_port`：修改客戶端發送的伺服器連接埠

`auth`：使用者名稱認證，可以是 `none`, `blacklist` 或 `whitelist`

## 負載均衡和連接限制

go-mcproxy 現在支援負載均衡和連接限制功能，可以更有效地管理多個代理和連接。

### 負載均衡

使用 `-balancer` 參數可以啟用負載均衡功能，例如：

```
./mcproxy -balancer 0.0.0.0:25565
```

負載均衡器會自動將新連接分配到負載最低的代理伺服器，確保資源得到最佳利用。負載均衡器現在直接使用代理的網路介面連接到遠端伺服器，無需通過本地轉發，提高了效能和效率。

### 動態代理切換

負載均衡器會根據每個代理的當前連接數動態選擇最佳代理，無需客戶端進行任何配置更改。每個連接都會直接使用選定代理的網路介面，確保最佳的網路路由。

### 連接限制

為了防止單個IP佔用過多資源，每個公網IP最多允許4個同時連接。當達到此限制時，新的連接請求將被拒絕。

## 控制面板

go-mcproxy 提供了一個簡潔而功能強大的網頁控制面板，可以用來監控和管理代理伺服器。

### 前後端分離（Vite）

從本版本起，控制面板採用前後端分離架構：
- 後端：Go 提供 JSON API（/api/*）、登入與驗證（/login, /auth, /logout）。
- 前端：使用 Vite + React 開發，編譯後為純靜態檔（web/dist）。

啟動時若偵測到 web/dist 存在，後端會自動改為提供該靜態前端；若不存在則回退至舊有的內嵌頁面（相容模式）。

### 開發方式

1. 啟動後端（預設 8080）
```
./mcproxy -control 127.0.0.1:8080
```
2. 啟動前端開發伺服器（Vite，預設 5173）
```
cd go-mcproxy/web
npm i
npm run dev
```
前端已設定開發代理，會自動把 /api、/login、/auth、/logout 轉發到後端 8080，因此無需額外 CORS 設定。

### 產出部署

於前端專案打包：
```
cd go-mcproxy/web
npm run build
```
會在 go-mcproxy/web/dist 產生靜態檔案。重新啟動或重新整理後端，即可在相同的控制面板網址提供新版前端。

### 訪問控制面板

啟動 go-mcproxy 後，可以通過瀏覽器訪問控制面板，預設地址為 http://127.0.0.1:8080

如果需要從其他裝置訪問控制面板，可以使用 `-control` 參數指定監聽地址，例如：

```
./mcproxy -control 0.0.0.0:8080
```

### 控制面板功能

控制面板提供以下功能：

1. **系統概覽**：顯示總連接數和每IP連接限制。

2. **代理狀態監控**：顯示每個代理的監聽地址、遠端伺服器、描述、公網IP、狀態、當前連接數和容量。

3. **連接管理**：查看和管理所有活動連接，包括使用者名稱、客戶端地址、代理地址、遠端伺服器、公網IP和連接時間。可以斷開特定連接。

4. **配置修改**：可以直接在控制面板上修改代理配置，包括監聽地址、遠端伺服器、本地地址、描述、最大玩家數、ping模式等。

5. **配置重載**：修改配置後，可以點擊"重載配置"按鈕使更改立即生效，無需重啟程式。

控制面板會自動保存修改後的配置到配置文件，並優化配置文件的儲存格式。控制面板的介面經過改進，更加美觀和易用。
