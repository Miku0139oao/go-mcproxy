package core

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"mcproxy/config"
	"mcproxy/logger"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ProxyStats tracks statistics for each proxy
type ProxyStats struct {
	Config          config.ProxyConfig
	ConnectionCount atomic.Int32
	PublicIP        string
}

// ControlPanel manages the web-based control panel
type ControlPanel struct {
	Stats           map[string]*ProxyStats
	ConfigPath      string
	CurrentConfig   *config.Config
	TotalConnections int32
	ConnectionLimit  int
	mutex           sync.RWMutex
}

var controlPanel *ControlPanel
var controlPanelOnce sync.Once

// GetControlPanel returns the singleton control panel instance
func GetControlPanel() *ControlPanel {
	controlPanelOnce.Do(func() {
		controlPanel = &ControlPanel{
			Stats: make(map[string]*ProxyStats),
		}
	})
	return controlPanel
}

// InitControlPanel initializes the control panel with the given configuration
func InitControlPanel(cfg *config.Config, configPath string) {
	cp := GetControlPanel()
	cp.mutex.Lock()
	defer cp.mutex.Unlock()

	cp.ConfigPath = configPath
	cp.CurrentConfig = cfg
	cp.ConnectionLimit = MaxConnectionsPerIP

	// Initialize stats for each proxy
	for _, proxy := range cfg.Proxies {
		listenAddr := proxy.Listen
		publicIP := GetPublicIP(proxy.LocalAddr)

		if _, exists := cp.Stats[listenAddr]; !exists {
			cp.Stats[listenAddr] = &ProxyStats{
				Config: proxy,
				PublicIP: publicIP,
			}
		} else {
			// Update config if proxy already exists
			cp.Stats[listenAddr].Config = proxy
			cp.Stats[listenAddr].PublicIP = publicIP
		}
	}
}

// IncrementConnectionCount increments the connection count for a proxy
func (cp *ControlPanel) IncrementConnectionCount(listenAddr string) {
	cp.mutex.RLock()
	defer cp.mutex.RUnlock()

	if stats, exists := cp.Stats[listenAddr]; exists {
		stats.ConnectionCount.Add(1)
	}
}

// DecrementConnectionCount decrements the connection count for a proxy
func (cp *ControlPanel) DecrementConnectionCount(listenAddr string) {
	cp.mutex.RLock()
	defer cp.mutex.RUnlock()

	if stats, exists := cp.Stats[listenAddr]; exists {
		stats.ConnectionCount.Add(-1)
	}
}

// SaveConfig saves the current configuration to the config file
func (cp *ControlPanel) SaveConfig() error {
	cp.mutex.RLock()
	defer cp.mutex.RUnlock()

	// Pretty format the JSON
	jsonData, err := json.MarshalIndent(cp.CurrentConfig, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	err = os.WriteFile(cp.ConfigPath, jsonData, 0644)
	if err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// ReloadConfig reloads the configuration and restarts the proxies
func (cp *ControlPanel) ReloadConfig() error {
	cp.mutex.Lock()
	defer cp.mutex.Unlock()

	// Save the current configuration first
	jsonData, err := json.MarshalIndent(cp.CurrentConfig, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	err = os.WriteFile(cp.ConfigPath, jsonData, 0644)
	if err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	// Restart the proxies with the new configuration
	log.Printf("[INFO] Reloading proxy configuration from control panel")
	Restart(*cp.CurrentConfig)

	// Re-initialize the control panel stats for the new proxies
	// Clear existing stats first
	for k := range cp.Stats {
		delete(cp.Stats, k)
	}

	// Initialize stats for each proxy in the new configuration
	for _, proxy := range cp.CurrentConfig.Proxies {
		listenAddr := proxy.Listen
		cp.Stats[listenAddr] = &ProxyStats{
			Config: proxy,
		}
	}

	return nil
}

// StartControlPanel starts the HTTP server for the control panel
func StartControlPanel(addr string) {
	// Web UI routes
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/update", handleUpdate)
	http.HandleFunc("/reload", handleReload)

	// API routes for connection management
	http.HandleFunc("/api/connections", handleAPIConnections)
	http.HandleFunc("/api/disconnect", handleAPIDisconnect)

	// API routes for logs
	http.HandleFunc("/api/logs", handleAPILogs)

	log.Printf("[INFO] Control panel listening on %s", addr)
	go func() {
		err := http.ListenAndServe(addr, nil)
		if err != nil {
			log.Fatalf("[ERROR] Control panel server failed: %v", err)
		}
	}()
}

// handleIndex handles the main control panel page
func handleIndex(w http.ResponseWriter, r *http.Request) {
	cp := GetControlPanel()
	cp.mutex.RLock()

	// Calculate total connections
	var totalConnections int32
	for _, stats := range cp.Stats {
		totalConnections += stats.ConnectionCount.Load()
	}
	cp.TotalConnections = totalConnections

	cp.mutex.RUnlock()

	// Simple HTML template for the control panel
	tmpl := `
<!DOCTYPE html>
<html>
<head>
    <title>Minecraft Proxy Control Panel</title>
    <style>
        :root {
            --primary-color: #3498db;
            --primary-dark: #2980b9;
            --secondary-color: #2ecc71;
            --secondary-dark: #27ae60;
            --danger-color: #e74c3c;
            --danger-dark: #c0392b;
            --text-color: #333;
            --light-bg: #f8f9fa;
            --border-color: #e0e0e0;
            --shadow: 0 4px 6px rgba(0,0,0,0.1);
        }

        body {
            font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
            margin: 0;
            padding: 0;
            background-color: var(--light-bg);
            color: var(--text-color);
            line-height: 1.6;
        }

        .container {
            max-width: 1200px;
            margin: 20px auto;
            background-color: white;
            padding: 25px;
            border-radius: 8px;
            box-shadow: var(--shadow);
        }

        h1, h2, h3 {
            color: var(--primary-color);
            margin-top: 0;
        }

        h1 {
            border-bottom: 2px solid var(--primary-color);
            padding-bottom: 10px;
            margin-bottom: 20px;
        }

        table {
            width: 100%;
            border-collapse: collapse;
            margin-bottom: 20px;
            box-shadow: 0 2px 3px rgba(0,0,0,0.05);
        }

        th, td {
            padding: 12px 15px;
            border: 1px solid var(--border-color);
            text-align: left;
        }

        th {
            background-color: var(--primary-color);
            color: white;
            font-weight: 500;
        }

        tr:nth-child(even) {
            background-color: rgba(0,0,0,0.02);
        }

        .tab {
            display: flex;
            border-bottom: 2px solid var(--border-color);
            margin-bottom: 20px;
            overflow: hidden;
        }

        .tab button {
            background-color: transparent;
            border: none;
            outline: none;
            cursor: pointer;
            padding: 12px 20px;
            font-size: 16px;
            color: var(--text-color);
            transition: all 0.3s ease;
            position: relative;
            margin-right: 5px;
        }

        .tab button:hover {
            color: var(--primary-color);
        }

        .tab button.active {
            color: var(--primary-color);
            font-weight: bold;
        }

        .tab button.active::after {
            content: '';
            position: absolute;
            bottom: 0;
            left: 0;
            width: 100%;
            height: 3px;
            background-color: var(--primary-color);
        }

        .tabcontent {
            display: none;
            padding: 20px 0;
            animation: fadeIn 0.5s;
        }

        @keyframes fadeIn {
            from { opacity: 0; }
            to { opacity: 1; }
        }

        .active-tabcontent {
            display: block;
        }

        .connection-row:hover {
            background-color: rgba(52, 152, 219, 0.05) !important;
        }

        .disconnect-btn {
            background-color: var(--danger-color);
            color: white;
            padding: 6px 12px;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            transition: background-color 0.3s;
        }

        .disconnect-btn:hover {
            background-color: var(--danger-dark);
        }

        .form-group {
            margin-bottom: 20px;
        }

        label {
            display: block;
            margin-bottom: 8px;
            font-weight: 500;
            color: var(--text-color);
        }

        input[type="text"], input[type="number"], select {
            width: 100%;
            padding: 10px;
            border: 1px solid var(--border-color);
            border-radius: 4px;
            font-size: 14px;
            transition: border-color 0.3s;
        }

        input[type="text"]:focus, input[type="number"]:focus, select:focus {
            border-color: var(--primary-color);
            outline: none;
            box-shadow: 0 0 0 3px rgba(52, 152, 219, 0.1);
        }

        button {
            background-color: var(--primary-color);
            color: white;
            padding: 10px 16px;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 14px;
            transition: background-color 0.3s;
        }

        button:hover {
            background-color: var(--primary-dark);
        }

        .card {
            background-color: white;
            border-radius: 8px;
            box-shadow: var(--shadow);
            padding: 20px;
            margin-bottom: 20px;
        }

        .status-indicator {
            display: inline-block;
            width: 12px;
            height: 12px;
            border-radius: 50%;
            margin-right: 8px;
        }

        .status-good {
            background-color: var(--secondary-color);
        }

        .status-warning {
            background-color: #f39c12;
        }

        .status-error {
            background-color: var(--danger-color);
        }

        .refresh-btn {
            background-color: var(--secondary-color);
            margin-right: 10px;
        }

        .refresh-btn:hover {
            background-color: var(--secondary-dark);
        }

        .action-buttons {
            margin-top: 20px;
            display: flex;
            gap: 10px;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>Minecraft Proxy Control Panel</h1>

        <div class="tab">
            <button class="tablinks active" onclick="openTab(event, 'status')">Status</button>
            <button class="tablinks" onclick="openTab(event, 'connections')">Active Connections</button>
            <button class="tablinks" onclick="openTab(event, 'logs')">Logs</button>
            <button class="tablinks" onclick="openTab(event, 'config')">Configuration</button>
        </div>

        <div id="status" class="tabcontent active-tabcontent">
            <h2>Proxy Status</h2>
            <div class="card">
                <h3>System Overview</h3>
                <p>Total active connections: <strong>{{.TotalConnections}}</strong></p>
                <p>Connection limit per IP: <strong>{{.ConnectionLimit}}</strong></p>
            </div>

            <div class="card">
                <h3>Proxy Servers</h3>
                <table>
                    <tr>
                        <th>Listen Address</th>
                        <th>Description</th>
                        <th>Remote Server</th>
                        <th>Public IP</th>
                        <th>Status</th>
                        <th>Connections</th>
                        <th>Capacity</th>
                    </tr>
                    {{range $addr, $stats := .Stats}}
                    <tr>
                        <td>{{$addr}}</td>
                        <td>{{$stats.Config.Description}}</td>
                        <td>{{$stats.Config.Remote}}</td>
                        <td>{{$stats.PublicIP}}</td>
                        <td>
                            {{if lt $stats.ConnectionCount.Load 1}}
                                <span class="status-indicator status-good" title="Idle"></span>Idle
                            {{else if lt $stats.ConnectionCount.Load (MaxConnectionsPerIP)}}
                                <span class="status-indicator status-good" title="Active"></span>Active
                            {{else if eq $stats.ConnectionCount.Load (MaxConnectionsPerIP)}}
                                <span class="status-indicator status-warning" title="Full"></span>Full
                            {{else}}
                                <span class="status-indicator status-error" title="Overloaded"></span>Overloaded
                            {{end}}
                        </td>
                        <td>{{$stats.ConnectionCount.Load}}</td>
                        <td>{{$stats.Config.MaxPlayer}} ({{MaxConnectionsPerIP}} per IP)</td>
                    </tr>
                    {{end}}
                </table>
            </div>

            <div class="action-buttons">
                <form action="/reload" method="post">
                    <button type="submit" class="refresh-btn">Reload Configuration</button>
                </form>
            </div>
        </div>

        <div id="connections" class="tabcontent">
            <h2>Active Connections</h2>

            <div class="card">
                <h3>Connection Management</h3>
                <p>Manage active client connections to the proxy servers. You can disconnect clients if needed.</p>

                <table id="connections-table">
                    <thead>
                        <tr>
                            <th>Username</th>
                            <th>Client Address</th>
                            <th>Proxy Address</th>
                            <th>Remote Server</th>
                            <th>Public IP</th>
                            <th>Connected At</th>
                            <th>Actions</th>
                        </tr>
                    </thead>
                    <tbody id="connections-tbody">
                        <!-- Connection rows will be populated by JavaScript -->
                        <tr>
                            <td colspan="7" style="text-align: center;">Loading connections...</td>
                        </tr>
                    </tbody>
                </table>

                <div class="action-buttons">
                    <button onclick="refreshConnections()" class="refresh-btn">Refresh Connections</button>
                </div>
            </div>
        </div>

        <div id="logs" class="tabcontent">
            <h2>System Logs</h2>

            <div class="card">
                <h3>Log Management</h3>
                <p>View and filter system logs. Use the filters below to narrow down the results.</p>

                <div class="form-group" style="display: flex; gap: 20px; flex-wrap: wrap;">
                    <div style="flex: 1; min-width: 200px;">
                        <label for="log-level">Log Level:</label>
                        <select id="log-level" onchange="refreshLogs()">
                            <option value="">All Levels</option>
                            <option value="DEBUG">Debug</option>
                            <option value="INFO">Info</option>
                            <option value="WARN">Warning</option>
                            <option value="ERROR">Error</option>
                            <option value="FATAL">Fatal</option>
                        </select>
                    </div>
                    <div style="flex: 1; min-width: 200px;">
                        <label for="log-start-time">Start Time:</label>
                        <input type="datetime-local" id="log-start-time" onchange="refreshLogs()">
                    </div>
                    <div style="flex: 1; min-width: 200px;">
                        <label for="log-end-time">End Time:</label>
                        <input type="datetime-local" id="log-end-time" onchange="refreshLogs()">
                    </div>
                </div>

                <table id="logs-table">
                    <thead>
                        <tr>
                            <th>Time</th>
                            <th>Level</th>
                            <th>Source</th>
                            <th>Message</th>
                        </tr>
                    </thead>
                    <tbody id="logs-tbody">
                        <!-- Log rows will be populated by JavaScript -->
                        <tr>
                            <td colspan="4" style="text-align: center;">Loading logs...</td>
                        </tr>
                    </tbody>
                </table>

                <div id="logs-pagination" style="margin-top: 20px; display: flex; justify-content: space-between; align-items: center;">
                    <div>
                        <span>Showing <span id="logs-showing">0</span> of <span id="logs-total">0</span> logs</span>
                    </div>
                    <div>
                        <button onclick="previousLogsPage()" class="refresh-btn" id="logs-prev-btn" disabled>Previous</button>
                        <button onclick="nextLogsPage()" class="refresh-btn" id="logs-next-btn" disabled>Next</button>
                    </div>
                </div>

                <div class="action-buttons">
                    <button onclick="refreshLogs()" class="refresh-btn">Refresh Logs</button>
                    <button onclick="clearLogFilters()" class="refresh-btn">Clear Filters</button>
                </div>
            </div>
        </div>

        <div id="config" class="tabcontent">
            <h2>Configuration</h2>

            <div class="card">
                <h3>Proxy Configuration</h3>
                <p>Configure your proxy servers. Changes will take effect after saving and reloading.</p>

                <form action="/update" method="post">
                    {{range $index, $proxy := .CurrentConfig.Proxies}}
                    <div class="card" style="margin-bottom: 30px;">
                        <h3>Proxy {{$index}}: {{$proxy.Description}}</h3>

                        <div class="form-group">
                            <label for="listen{{$index}}">Listen Address:</label>
                            <input type="text" id="listen{{$index}}" name="proxies[{{$index}}].listen" value="{{$proxy.Listen}}">
                        </div>

                        <div class="form-group">
                            <label for="remote{{$index}}">Remote Server:</label>
                            <input type="text" id="remote{{$index}}" name="proxies[{{$index}}].remote" value="{{$proxy.Remote}}">
                        </div>

                        <div class="form-group">
                            <label for="local_addr{{$index}}">Local Address (for outgoing connections):</label>
                            <input type="text" id="local_addr{{$index}}" name="proxies[{{$index}}].local_addr" value="{{$proxy.LocalAddr}}">
                        </div>

                        <div class="form-group">
                            <label for="description{{$index}}">Description:</label>
                            <input type="text" id="description{{$index}}" name="proxies[{{$index}}].description" value="{{$proxy.Description}}">
                        </div>

                        <div class="form-group">
                            <label for="maxplayer{{$index}}">Max Players:</label>
                            <input type="number" id="maxplayer{{$index}}" name="proxies[{{$index}}].max_player" value="{{$proxy.MaxPlayer}}">
                            <small>Note: Maximum connections per IP is limited to {{MaxConnectionsPerIP}}</small>
                        </div>

                        <div class="form-group">
                            <label for="pingmode{{$index}}">Ping Mode:</label>
                            <select id="pingmode{{$index}}" name="proxies[{{$index}}].ping_mode">
                                <option value="fake" {{if eq $proxy.PingMode "fake"}}selected{{end}}>Fake</option>
                                <option value="real" {{if eq $proxy.PingMode "real"}}selected{{end}}>Real</option>
                            </select>
                        </div>

                        <div class="form-group">
                            <label for="fakeping{{$index}}">Fake Ping (ms):</label>
                            <input type="number" id="fakeping{{$index}}" name="proxies[{{$index}}].fake_ping" value="{{$proxy.FakePing}}">
                        </div>

                        <div class="form-group">
                            <label for="auth{{$index}}">Authentication Mode:</label>
                            <select id="auth{{$index}}" name="proxies[{{$index}}].auth">
                                <option value="none" {{if eq $proxy.Auth "none"}}selected{{end}}>None</option>
                                <option value="whitelist" {{if eq $proxy.Auth "whitelist"}}selected{{end}}>Whitelist</option>
                                <option value="blacklist" {{if eq $proxy.Auth "blacklist"}}selected{{end}}>Blacklist</option>
                            </select>
                        </div>
                    </div>
                    {{end}}

                    <div class="action-buttons">
                        <button type="submit">Save Configuration</button>
                        <button type="button" onclick="window.location.href='/'" class="refresh-btn">Cancel</button>
                    </div>
                </form>

                <div class="action-buttons" style="margin-top: 20px;">
                    <form action="/reload" method="post">
                        <button type="submit" class="refresh-btn">Reload Configuration</button>
                    </form>
                </div>
            </div>
        </div>

    <script>
        // Tab switching functionality
        function openTab(evt, tabName) {
            var i, tabcontent, tablinks;

            // Hide all tab content
            tabcontent = document.getElementsByClassName("tabcontent");
            for (i = 0; i < tabcontent.length; i++) {
                tabcontent[i].className = tabcontent[i].className.replace(" active-tabcontent", "");
            }

            // Remove active class from all tab buttons
            tablinks = document.getElementsByClassName("tablinks");
            for (i = 0; i < tablinks.length; i++) {
                tablinks[i].className = tablinks[i].className.replace(" active", "");
            }

            // Show the current tab and add active class to the button
            document.getElementById(tabName).className += " active-tabcontent";
            evt.currentTarget.className += " active";

            // If connections tab is opened, refresh the connections list
            if (tabName === 'connections') {
                refreshConnections();
            }

            // If logs tab is opened, refresh the logs list
            if (tabName === 'logs') {
                refreshLogs();
            }
        }

        // Function to refresh the connections list
        function refreshConnections() {
            fetch('/api/connections')
                .then(response => response.json())
                .then(connections => {
                    const tbody = document.getElementById('connections-tbody');
                    tbody.innerHTML = '';

                    if (connections.length === 0) {
                        const row = document.createElement('tr');
                        row.innerHTML = '<td colspan="7" style="text-align: center;">No active connections</td>';
                        tbody.appendChild(row);
                        return;
                    }

                    connections.forEach(conn => {
                        const row = document.createElement('tr');
                        row.className = 'connection-row';

                        // Format the connected at time
                        const connectedAt = new Date(conn.connected_at);
                        const formattedTime = connectedAt.toLocaleString();

                        row.innerHTML = 
                            '<td>' + (conn.username || '&lt;unknown&gt;') + '</td>' +
                            '<td>' + conn.client_addr + '</td>' +
                            '<td>' + conn.proxy_addr + '</td>' +
                            '<td>' + conn.remote_addr + '</td>' +
                            '<td>' + conn.public_ip + '</td>' +
                            '<td>' + formattedTime + '</td>' +
                            '<td>' +
                                '<button class="disconnect-btn" onclick="disconnectClient(\'' + conn.id + '\')">Disconnect</button>' +
                            '</td>';
                        tbody.appendChild(row);
                    });
                })
                .catch(error => {
                    console.error('Error fetching connections:', error);
                    const tbody = document.getElementById('connections-tbody');
                    tbody.innerHTML = '<tr><td colspan="7" style="text-align: center; color: red;">Error loading connections</td></tr>';
                });
        }

        // Function to disconnect a client
        function disconnectClient(id) {
            if (!confirm('Are you sure you want to disconnect this client?')) {
                return;
            }

            const formData = new FormData();
            formData.append('id', id);
            formData.append('reason', 'Disconnected by administrator');

            fetch('/api/disconnect', {
                method: 'POST',
                body: formData
            })
            .then(response => response.json())
            .then(result => {
                if (result.success) {
                    refreshConnections();
                } else {
                    alert('Failed to disconnect client');
                }
            })
            .catch(error => {
                console.error('Error disconnecting client:', error);
                alert('Error disconnecting client');
            });
        }

        // Auto-refresh connections every 10 seconds when the tab is active
        setInterval(() => {
            const connectionsTab = document.getElementById('connections');
            if (connectionsTab.className.includes('active-tabcontent')) {
                refreshConnections();
            }
        }, 10000);

        // Logs pagination variables
        let logsCurrentPage = 0;
        let logsPageSize = 100;
        let logsTotalCount = 0;

        // Function to refresh the logs list
        function refreshLogs() {
            // Get filter values
            const level = document.getElementById('log-level').value;
            const startTime = document.getElementById('log-start-time').value ? 
                new Date(document.getElementById('log-start-time').value).toISOString() : '';
            const endTime = document.getElementById('log-end-time').value ? 
                new Date(document.getElementById('log-end-time').value).toISOString() : '';

            // Build the query URL
            let url = '/api/logs?limit=' + logsPageSize + '&offset=' + (logsCurrentPage * logsPageSize);
            if (level) url += '&level=' + encodeURIComponent(level);
            if (startTime) url += '&start_time=' + encodeURIComponent(startTime);
            if (endTime) url += '&end_time=' + encodeURIComponent(endTime);

            fetch(url)
                .then(response => response.json())
                .then(data => {
                    const tbody = document.getElementById('logs-tbody');
                    tbody.innerHTML = '';

                    logsTotalCount = data.total_count;

                    if (data.logs.length === 0) {
                        const row = document.createElement('tr');
                        row.innerHTML = '<td colspan="4" style="text-align: center;">No logs found</td>';
                        tbody.appendChild(row);
                    } else {
                        data.logs.forEach(log => {
                            const row = document.createElement('tr');

                            // Format the timestamp
                            const timestamp = new Date(log.timestamp);
                            const formattedTime = timestamp.toLocaleString();

                            // Set row color based on log level
                            let rowClass = '';
                            if (log.level === 'ERROR' || log.level === 'FATAL') {
                                rowClass = 'style="background-color: rgba(231, 76, 60, 0.1);"';
                            } else if (log.level === 'WARN') {
                                rowClass = 'style="background-color: rgba(243, 156, 18, 0.1);"';
                            }

                            row.innerHTML = 
                                '<tr ' + rowClass + '>' +
                                '<td>' + formattedTime + '</td>' +
                                '<td>' + log.level + '</td>' +
                                '<td>' + log.source + '</td>' +
                                '<td>' + log.message + '</td>' +
                                '</tr>';
                            tbody.appendChild(row);
                        });
                    }

                    // Update pagination info
                    document.getElementById('logs-showing').textContent = 
                        data.logs.length > 0 ? 
                        ((logsCurrentPage * logsPageSize) + 1) + '-' + 
                        Math.min((logsCurrentPage + 1) * logsPageSize, logsTotalCount) : 0;
                    document.getElementById('logs-total').textContent = logsTotalCount;

                    // Update pagination buttons
                    document.getElementById('logs-prev-btn').disabled = logsCurrentPage === 0;
                    document.getElementById('logs-next-btn').disabled = 
                        (logsCurrentPage + 1) * logsPageSize >= logsTotalCount;
                })
                .catch(error => {
                    console.error('Error fetching logs:', error);
                    const tbody = document.getElementById('logs-tbody');
                    tbody.innerHTML = '<tr><td colspan="4" style="text-align: center; color: red;">Error loading logs</td></tr>';
                });
        }

        // Function to go to the previous page of logs
        function previousLogsPage() {
            if (logsCurrentPage > 0) {
                logsCurrentPage--;
                refreshLogs();
            }
        }

        // Function to go to the next page of logs
        function nextLogsPage() {
            if ((logsCurrentPage + 1) * logsPageSize < logsTotalCount) {
                logsCurrentPage++;
                refreshLogs();
            }
        }

        // Function to clear log filters
        function clearLogFilters() {
            document.getElementById('log-level').value = '';
            document.getElementById('log-start-time').value = '';
            document.getElementById('log-end-time').value = '';
            logsCurrentPage = 0;
            refreshLogs();
        }

        // Auto-refresh logs every 30 seconds when the tab is active
        setInterval(() => {
            const logsTab = document.getElementById('logs');
            if (logsTab.className.includes('active-tabcontent')) {
                refreshLogs();
            }
        }, 30000);
    </script>
</body>
</html>
`

	// Create template with functions
	funcMap := template.FuncMap{
		"MaxConnectionsPerIP": func() int {
			return MaxConnectionsPerIP
		},
	}

	t, err := template.New("index").Funcs(funcMap).Parse(tmpl)
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	err = t.Execute(w, cp)
	if err != nil {
		http.Error(w, "Template execution error: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleUpdate handles the configuration update
func handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cp := GetControlPanel()
	cp.mutex.Lock()
	defer cp.mutex.Unlock()

	// Parse form data
	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Create a new config to store the updated values
	newConfig := config.Config{
		Proxies: make([]config.ProxyConfig, len(cp.CurrentConfig.Proxies)),
	}

	// Copy the existing proxies as a starting point
	copy(newConfig.Proxies, cp.CurrentConfig.Proxies)

	// Update each proxy configuration based on form data
	for i := range newConfig.Proxies {
		// Basic fields
		if listen := r.FormValue(fmt.Sprintf("proxies[%d].listen", i)); listen != "" {
			newConfig.Proxies[i].Listen = listen
		}

		if remote := r.FormValue(fmt.Sprintf("proxies[%d].remote", i)); remote != "" {
			newConfig.Proxies[i].Remote = remote
		}

		if description := r.FormValue(fmt.Sprintf("proxies[%d].description", i)); description != "" {
			newConfig.Proxies[i].Description = description
		}

		if localAddr := r.FormValue(fmt.Sprintf("proxies[%d].local_addr", i)); localAddr != "" {
			newConfig.Proxies[i].LocalAddr = localAddr
		}

		if favicon := r.FormValue(fmt.Sprintf("proxies[%d].favicon", i)); favicon != "" {
			newConfig.Proxies[i].Favicon = favicon
		}

		// Numeric fields
		if maxPlayer := r.FormValue(fmt.Sprintf("proxies[%d].max_player", i)); maxPlayer != "" {
			var val int
			fmt.Sscanf(maxPlayer, "%d", &val)
			newConfig.Proxies[i].MaxPlayer = val
		}

		if fakePing := r.FormValue(fmt.Sprintf("proxies[%d].fake_ping", i)); fakePing != "" {
			var val int
			fmt.Sscanf(fakePing, "%d", &val)
			newConfig.Proxies[i].FakePing = val
		}

		if rewritePort := r.FormValue(fmt.Sprintf("proxies[%d].rewrite_port", i)); rewritePort != "" {
			var val int
			fmt.Sscanf(rewritePort, "%d", &val)
			newConfig.Proxies[i].RewirtePort = val
		}

		// Select fields
		if pingMode := r.FormValue(fmt.Sprintf("proxies[%d].ping_mode", i)); pingMode != "" {
			newConfig.Proxies[i].PingMode = pingMode
		}

		if auth := r.FormValue(fmt.Sprintf("proxies[%d].auth", i)); auth != "" {
			newConfig.Proxies[i].Auth = auth
		}

		if rewriteHost := r.FormValue(fmt.Sprintf("proxies[%d].rewrite_host", i)); rewriteHost != "" {
			newConfig.Proxies[i].RewirteHost = rewriteHost
		}

		// TODO: Handle whitelist and blacklist arrays
	}

	// Update the current configuration
	cp.CurrentConfig = &newConfig

	// Save the updated configuration
	err = cp.SaveConfig()
	if err != nil {
		http.Error(w, "Failed to save configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Redirect back to the index page
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleReload handles the configuration reload
func handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cp := GetControlPanel()

	// Reload the configuration
	err := cp.ReloadConfig()
	if err != nil {
		http.Error(w, "Failed to reload configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Redirect back to the index page
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleAPIConnections returns a JSON list of all active connections
func handleAPIConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set content type
	w.Header().Set("Content-Type", "application/json")

	// Get all active connections
	connections := GetAllConnections()

	// Create a simplified version for the API response
	type ConnectionInfo struct {
		ID          string `json:"id"`
		Username    string `json:"username"`
		ClientAddr  string `json:"client_addr"`
		ProxyAddr   string `json:"proxy_addr"`
		RemoteAddr  string `json:"remote_addr"`
		PublicIP    string `json:"public_ip"`
		ConnectedAt string `json:"connected_at"`
		ProxyIndex  int    `json:"proxy_index"`
	}

	// Convert to the simplified format
	connectionInfos := make([]ConnectionInfo, 0, len(connections))
	for _, conn := range connections {
		connectionInfos = append(connectionInfos, ConnectionInfo{
			ID:          conn.ID,
			Username:    conn.Username,
			ClientAddr:  conn.ClientAddr,
			ProxyAddr:   conn.ProxyAddr,
			RemoteAddr:  conn.RemoteAddr,
			PublicIP:    conn.PublicIP,
			ConnectedAt: conn.ConnectedAt.Format(time.RFC3339),
			ProxyIndex:  conn.ProxyIndex,
		})
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(connectionInfos)
	if err != nil {
		http.Error(w, "Failed to marshal connections: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Write the response
	w.Write(jsonData)
}

// handleAPIDisconnect disconnects a specific client
func handleAPIDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse form data
	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Get connection ID
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "Connection ID is required", http.StatusBadRequest)
		return
	}

	// Get disconnect reason
	reason := r.FormValue("reason")
	if reason == "" {
		reason = "Disconnected by administrator"
	}

	// Disconnect the client
	err = DisconnectClient(id, reason)
	if err != nil {
		http.Error(w, "Failed to disconnect client: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Set content type
	w.Header().Set("Content-Type", "application/json")

	// Return success response
	w.Write([]byte(`{"success": true}`))
}

// handleAPILogs returns a JSON list of logs with optional filtering
func handleAPILogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set content type
	w.Header().Set("Content-Type", "application/json")

	// Parse query parameters for filtering
	query := r.URL.Query()

	// Parse limit and offset
	limit := 100 // Default limit
	if limitStr := query.Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	offset := 0 // Default offset
	if offsetStr := query.Get("offset"); offsetStr != "" {
		if parsedOffset, err := strconv.Atoi(offsetStr); err == nil && parsedOffset >= 0 {
			offset = parsedOffset
		}
	}

	// Parse level filter
	level := query.Get("level")

	// Parse time range
	var startTime, endTime time.Time
	if startTimeStr := query.Get("start_time"); startTimeStr != "" {
		if parsed, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			startTime = parsed
		}
	}

	if endTimeStr := query.Get("end_time"); endTimeStr != "" {
		if parsed, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			endTime = parsed
		}
	}

	// Get logs from the logger
	l := logger.GetLogger()
	logs, err := l.GetLogs(limit, offset, level, startTime, endTime)
	if err != nil {
		http.Error(w, "Failed to retrieve logs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Get total count for pagination
	totalCount, err := l.GetLogCount(level, startTime, endTime)
	if err != nil {
		http.Error(w, "Failed to count logs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create response with logs and pagination info
	response := struct {
		Logs       []logger.LogEntry `json:"logs"`
		TotalCount int               `json:"total_count"`
		Limit      int               `json:"limit"`
		Offset     int               `json:"offset"`
	}{
		Logs:       logs,
		TotalCount: totalCount,
		Limit:      limit,
		Offset:     offset,
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(response)
	if err != nil {
		http.Error(w, "Failed to marshal logs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Write the response
	w.Write(jsonData)
}
