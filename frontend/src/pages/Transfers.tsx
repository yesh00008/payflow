import { useState } from 'react';
import { ArrowLeftRight, Send } from 'lucide-react';

export default function Transfers() {
  const [sourceId, setSourceId] = useState('');
  const [destId, setDestId] = useState('');
  const [amount, setAmount] = useState('');
  const [desc, setDesc] = useState('');
  const [result, setResult] = useState<string | null>(null);

  const handleTransfer = () => {
    setResult(`Transfer of $${amount} from ${sourceId.slice(0, 8)}... to ${destId.slice(0, 8)}... initiated. Status: processing (saga orchestration running).`);
  };

  const recentTransfers = [
    { id: 'txn-001', from: 'Alice', to: 'Bob', amount: 250, status: 'completed', time: '2 min ago' },
    { id: 'txn-002', from: 'Bob', to: 'Charlie', amount: 100, status: 'completed', time: '15 min ago' },
    { id: 'txn-003', from: 'Alice', to: 'Charlie', amount: 500, status: 'processing', time: '1 hr ago' },
    { id: 'txn-004', from: 'Charlie', to: 'Alice', amount: 75, status: 'failed', time: '3 hr ago' },
  ];

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold">Send Money</h2>

      <div className="bg-white rounded-xl shadow-sm border p-6 space-y-4">
        <div className="flex items-center gap-2 text-gray-500"><ArrowLeftRight size={20} /><span className="text-sm">Fund transfer via Saga Orchestration (4-step ACID)</span></div>
        <div className="grid grid-cols-2 gap-4">
          <input value={sourceId} onChange={(e) => setSourceId(e.target.value)} placeholder="Source Account ID" className="border rounded-lg px-3 py-2 text-sm" />
          <input value={destId} onChange={(e) => setDestId(e.target.value)} placeholder="Destination Account ID" className="border rounded-lg px-3 py-2 text-sm" />
          <input value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="Amount (USD)" type="number" className="border rounded-lg px-3 py-2 text-sm" />
          <input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="Description (optional)" className="border rounded-lg px-3 py-2 text-sm" />
        </div>
        <button onClick={handleTransfer} className="flex items-center gap-2 bg-primary-600 text-white px-6 py-2 rounded-lg hover:bg-primary-700 text-sm">
          <Send size={16} /> Send Transfer
        </button>
        {result && <div className="bg-green-50 border border-green-200 rounded-lg p-3 text-sm text-green-800">{result}</div>}
      </div>

      <div className="bg-white rounded-xl shadow-sm border overflow-hidden">
        <div className="p-4 border-b"><h3 className="font-semibold">Recent Transfers</h3></div>
        <table className="w-full text-sm">
          <thead className="bg-gray-50"><tr>
            <th className="text-left p-3">ID</th><th className="text-left p-3">From</th><th className="text-left p-3">To</th>
            <th className="text-right p-3">Amount</th><th className="text-left p-3">Status</th><th className="text-left p-3">Time</th>
          </tr></thead>
          <tbody>
            {recentTransfers.map((t) => (
              <tr key={t.id} className="border-t hover:bg-gray-50">
                <td className="p-3 font-mono text-xs">{t.id}</td>
                <td className="p-3">{t.from}</td><td className="p-3">{t.to}</td>
                <td className="p-3 text-right font-semibold">${t.amount}</td>
                <td className="p-3">
                  <span className={`px-2 py-0.5 rounded-full text-xs ${
                    t.status === 'completed' ? 'bg-green-100 text-green-700' :
                    t.status === 'processing' ? 'bg-yellow-100 text-yellow-700' : 'bg-red-100 text-red-700'
                  }`}>{t.status}</span>
                </td>
                <td className="p-3 text-gray-500">{t.time}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
