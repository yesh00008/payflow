export default function AuditLogs() {
  const logs = [
    { id: 'aud-001', event: 'transfer.completed', service: 'transaction-service', user: 'alice', action: 'transfer', time: '2026-02-14 10:23:15', ip: '192.168.1.10' },
    { id: 'aud-002', event: 'payment.initiated', service: 'payment-connector', user: 'bob', action: 'payment', time: '2026-02-14 10:18:42', ip: '192.168.1.11' },
    { id: 'aud-003', event: 'auth.login', service: 'auth-service', user: 'admin', action: 'login', time: '2026-02-14 09:55:33', ip: '192.168.1.1' },
    { id: 'aud-004', event: 'kyc.document_submitted', service: 'user-kyc-service', user: 'charlie', action: 'kyc_upload', time: '2026-02-14 09:30:11', ip: '10.0.0.5' },
    { id: 'aud-005', event: 'fraud.high_risk_detected', service: 'fraud-detection', user: 'unknown', action: 'fraud_check', time: '2026-02-14 08:12:05', ip: '203.0.113.42' },
    { id: 'aud-006', event: 'account.created', service: 'account-service', user: 'alice', action: 'create', time: '2026-02-14 07:45:00', ip: '192.168.1.10' },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold">Audit & Compliance Logs</h2>
        <div className="flex gap-2">
          <select className="border rounded-lg px-3 py-1.5 text-sm"><option>All Services</option><option>auth-service</option><option>transaction-service</option><option>payment-connector</option><option>fraud-detection</option></select>
          <select className="border rounded-lg px-3 py-1.5 text-sm"><option>All Events</option><option>transfer.*</option><option>payment.*</option><option>auth.*</option><option>fraud.*</option></select>
        </div>
      </div>

      <div className="grid grid-cols-3 gap-4">
        <div className="bg-white rounded-xl shadow-sm border p-4 text-center">
          <p className="text-sm text-gray-500">Total Events (24h)</p>
          <p className="text-2xl font-bold">15,847</p>
        </div>
        <div className="bg-white rounded-xl shadow-sm border p-4 text-center">
          <p className="text-sm text-gray-500">Compliance Status</p>
          <p className="text-2xl font-bold text-green-600">Compliant</p>
        </div>
        <div className="bg-white rounded-xl shadow-sm border p-4 text-center">
          <p className="text-sm text-gray-500">Storage</p>
          <p className="text-2xl font-bold">MongoDB</p>
          <p className="text-xs text-gray-400">Event-sourced via RabbitMQ</p>
        </div>
      </div>

      <div className="bg-white rounded-xl shadow-sm border overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50"><tr>
            <th className="text-left p-3">Audit ID</th><th className="text-left p-3">Event</th>
            <th className="text-left p-3">Service</th><th className="text-left p-3">User</th>
            <th className="text-left p-3">Action</th><th className="text-left p-3">IP</th><th className="text-left p-3">Time</th>
          </tr></thead>
          <tbody>
            {logs.map((l) => (
              <tr key={l.id} className="border-t hover:bg-gray-50">
                <td className="p-3 font-mono text-xs">{l.id}</td>
                <td className="p-3 text-xs"><code className="bg-gray-100 px-1.5 py-0.5 rounded">{l.event}</code></td>
                <td className="p-3 text-xs">{l.service}</td>
                <td className="p-3">{l.user}</td>
                <td className="p-3">{l.action}</td>
                <td className="p-3 font-mono text-xs text-gray-400">{l.ip}</td>
                <td className="p-3 text-gray-500 text-xs">{l.time}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
