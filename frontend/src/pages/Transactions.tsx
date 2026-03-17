export default function Transactions() {
  const txns = [
    { id: 'txn-20260214-001', type: 'transfer', amount: 250, currency: 'USD', status: 'completed', from: 'Alice', to: 'Bob', time: '2026-02-14 10:23:15' },
    { id: 'txn-20260214-002', type: 'payment', amount: 1500, currency: 'USD', status: 'completed', from: 'Bob', to: 'Stripe', time: '2026-02-14 10:18:42' },
    { id: 'txn-20260214-003', type: 'transfer', amount: 500, currency: 'EUR', status: 'processing', from: 'Charlie', to: 'Alice', time: '2026-02-14 09:55:33' },
    { id: 'txn-20260214-004', type: 'refund', amount: 75, currency: 'USD', status: 'completed', from: 'System', to: 'Charlie', time: '2026-02-14 09:30:11' },
    { id: 'txn-20260214-005', type: 'transfer', amount: 8000, currency: 'USD', status: 'flagged', from: 'Unknown', to: 'Alice', time: '2026-02-14 08:12:05' },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold">Transaction History</h2>
        <div className="flex gap-2">
          <select className="border rounded-lg px-3 py-1.5 text-sm"><option>All Types</option><option>Transfers</option><option>Payments</option><option>Refunds</option></select>
          <select className="border rounded-lg px-3 py-1.5 text-sm"><option>All Status</option><option>Completed</option><option>Processing</option><option>Failed</option><option>Flagged</option></select>
        </div>
      </div>

      <div className="bg-white rounded-xl shadow-sm border overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50"><tr>
            <th className="text-left p-3">Transaction ID</th><th className="text-left p-3">Type</th>
            <th className="text-left p-3">From</th><th className="text-left p-3">To</th>
            <th className="text-right p-3">Amount</th><th className="text-left p-3">Status</th>
            <th className="text-left p-3">Timestamp</th>
          </tr></thead>
          <tbody>
            {txns.map((t) => (
              <tr key={t.id} className="border-t hover:bg-gray-50">
                <td className="p-3 font-mono text-xs">{t.id}</td>
                <td className="p-3 capitalize">{t.type}</td>
                <td className="p-3">{t.from}</td><td className="p-3">{t.to}</td>
                <td className="p-3 text-right font-semibold">{t.currency === 'EUR' ? '€' : '$'}{t.amount.toLocaleString()}</td>
                <td className="p-3">
                  <span className={`px-2 py-0.5 rounded-full text-xs ${
                    t.status === 'completed' ? 'bg-green-100 text-green-700' :
                    t.status === 'processing' ? 'bg-yellow-100 text-yellow-700' :
                    t.status === 'flagged' ? 'bg-red-100 text-red-700' : 'bg-gray-100 text-gray-600'
                  }`}>{t.status}</span>
                </td>
                <td className="p-3 text-gray-500 text-xs">{t.time}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
