import { useState } from 'react';
import { CreditCard } from 'lucide-react';

export default function Payments() {
  const [provider, setProvider] = useState('stripe');
  const [amount, setAmount] = useState('');
  const [method, setMethod] = useState('card');

  const payments = [
    { id: 'pay-001', provider: 'stripe', amount: 150, method: 'card', status: 'completed', ref: 'ext_abc123' },
    { id: 'pay-002', provider: 'wise', amount: 500, method: 'bank', status: 'completed', ref: 'ext_def456' },
    { id: 'pay-003', provider: 'bank_transfer', amount: 1000, method: 'bank', status: 'pending', ref: 'ext_ghi789' },
  ];

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold">Payments</h2>

      <div className="bg-white rounded-xl shadow-sm border p-6 space-y-4">
        <div className="flex items-center gap-2 text-gray-500"><CreditCard size={20} /> <span className="text-sm">Initiate external payment via provider connectors</span></div>
        <div className="grid grid-cols-3 gap-4">
          <select value={provider} onChange={(e) => setProvider(e.target.value)} className="border rounded-lg px-3 py-2 text-sm">
            <option value="stripe">Stripe</option><option value="wise">Wise</option><option value="bank_transfer">Bank Transfer</option>
          </select>
          <input value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="Amount" type="number" className="border rounded-lg px-3 py-2 text-sm" />
          <select value={method} onChange={(e) => setMethod(e.target.value)} className="border rounded-lg px-3 py-2 text-sm">
            <option value="card">Card</option><option value="bank">Bank</option><option value="wallet">Wallet</option>
          </select>
        </div>
        <button className="bg-primary-600 text-white px-6 py-2 rounded-lg hover:bg-primary-700 text-sm">Process Payment</button>
      </div>

      <div className="bg-white rounded-xl shadow-sm border overflow-hidden">
        <div className="p-4 border-b"><h3 className="font-semibold">Payment History</h3></div>
        <table className="w-full text-sm">
          <thead className="bg-gray-50"><tr>
            <th className="text-left p-3">ID</th><th className="text-left p-3">Provider</th>
            <th className="text-right p-3">Amount</th><th className="text-left p-3">Method</th>
            <th className="text-left p-3">Status</th><th className="text-left p-3">Reference</th>
          </tr></thead>
          <tbody>
            {payments.map((p) => (
              <tr key={p.id} className="border-t hover:bg-gray-50">
                <td className="p-3 font-mono text-xs">{p.id}</td>
                <td className="p-3 capitalize">{p.provider}</td>
                <td className="p-3 text-right font-semibold">${p.amount}</td>
                <td className="p-3">{p.method}</td>
                <td className="p-3"><span className={`px-2 py-0.5 rounded-full text-xs ${p.status === 'completed' ? 'bg-green-100 text-green-700' : 'bg-yellow-100 text-yellow-700'}`}>{p.status}</span></td>
                <td className="p-3 font-mono text-xs text-gray-400">{p.ref}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
