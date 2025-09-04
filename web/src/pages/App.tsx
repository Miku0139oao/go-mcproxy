import React, { useEffect, useState } from 'react'

interface StatsResponse {
  total_connections: number
  connection_limit: number
  proxies: Array<{
    listen: string
    remote: string
    description: string
    public_ip: string
    connections: number
  }>
}

export const App: React.FC = () => {
  const [stats, setStats] = useState<StatsResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetch('/api/stats', { credentials: 'include' })
      .then(async (r) => {
        if (!r.ok) throw new Error(await r.text())
        return r.json()
      })
      .then(setStats)
      .catch((e) => setError(String(e)))
  }, [])

  return (
    <div style={{ padding: 24, fontFamily: 'Segoe UI, Tahoma, Geneva, Verdana, sans-serif' }}>
      <h1>MC Proxy 控制台</h1>
      {error && <div style={{ color: 'red' }}>載入失敗: {error}</div>}
      {!stats ? (
        <div>載入中…</div>
      ) : (
        <div>
          <div style={{ marginBottom: 12 }}>
            連線總數: {stats.total_connections} / 限制: {stats.connection_limit}
          </div>
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr>
                <th style={{ textAlign: 'left', borderBottom: '1px solid #ddd', padding: 8 }}>Listen</th>
                <th style={{ textAlign: 'left', borderBottom: '1px solid #ddd', padding: 8 }}>Remote</th>
                <th style={{ textAlign: 'left', borderBottom: '1px solid #ddd', padding: 8 }}>描述</th>
                <th style={{ textAlign: 'left', borderBottom: '1px solid #ddd', padding: 8 }}>Public IP</th>
                <th style={{ textAlign: 'left', borderBottom: '1px solid #ddd', padding: 8 }}>連線數</th>
              </tr>
            </thead>
            <tbody>
              {stats.proxies.map((p) => (
                <tr key={p.listen}>
                  <td style={{ borderBottom: '1px solid #f0f0f0', padding: 8 }}>{p.listen}</td>
                  <td style={{ borderBottom: '1px solid #f0f0f0', padding: 8 }}>{p.remote}</td>
                  <td style={{ borderBottom: '1px solid #f0f0f0', padding: 8 }}>{p.description}</td>
                  <td style={{ borderBottom: '1px solid #f0f0f0', padding: 8 }}>{p.public_ip}</td>
                  <td style={{ borderBottom: '1px solid #f0f0f0', padding: 8 }}>{p.connections}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
