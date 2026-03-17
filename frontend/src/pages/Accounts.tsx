import { useState } from 'react';
import { Wallet, Plus } from 'lucide-react';

interface MockAccount {
  id: string; name: string; type: string; currency: string; balance: number; status: string;
}
const mockAccounts: MockAccount[] = [
  { id: '10000000-0001', name: 'Alice Checking', type: 'checking', currency: 'USD', balance: 100000, status: 'active' },
  { id: '10000000-0002', name: 'Bob Checking', type: 'checking', currency: 'USD', balance: 50000, status: 'active' },
  { id: '10000000-0003', name: 'Charlie Savings', type: 'savings', currency: 'EUR', balance: 250000, status: 'active' },
];

export default function Accounts() {
  const [showCreate, setShowCreate] = useState(false);
  const [name, setName] = useState('');
  const [type, setType] = useState('checking');
  const [currency, setCurrency] = useState('USD');

  const handleCreate = () => {
    alert(`Creating account: ${name} (${type}, ${currency})`);
    setShowCreate(false);
    setName('');
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold">Accounts</h2>
        <button onClick={() => setShowCreate(!showCreate)} className="flex items-center gap-2 bg-primary-600 text-white px-4 py-2 rounded-lg hover:bg-primary-700 text-sm">
          <Plus size={16} /> New Account
        </button>
      </div>

      {showCreate && (
        <div className="bg-white rounded-xl shadow-sm border p-6 space-y-4">
          <h3 className="font-semibold">Create Account</h3>
          <div className="grid grid-cols-3 gap-4">
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Account name" className="border rounded-lg px-3 py-2 text-sm" />
            <select value={type} onChange={(e) => setType(e.target.value)} className="border rounded-lg px-3 py-2 text-sm">
              <option value="checking">Checking</option>
              <option value="savings">Savings</option>
              <option value="wallet">Wallet</option>
            </select>
            <select value={currency} onChange={(e) => setCurrency(e.target.value)} className="border rounded-lg px-3 py-2 text-sm">
              <option>USD</option><option>EUR</option><option>GBP</option>
            </select>
          </div>
          <button onClick={handleCreate} className="bg-green-600 text-white px-4 py-2 rounded-lg text-sm hover:bg-green-700">Create</button>
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {mockAccounts.map((a) => (
          <div key={a.id} className="bg-white rounded-xl shadow-sm border p-6">
            <div className="flex items-center justify-between mb-4">
              <div className="p-2 bg-blue-50 rounded-lg"><Wallet size={20} className="text-blue-600" /></div>
              <span className={`text-xs px-2 py-1 rounded-full ${a.status === 'active' ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-600'}`}>{a.status}</span>
            </div>
            <h4 className="font-semibold">{a.name}</h4>
            <p className="text-xs text-gray-400 mt-1">{a.type} · {a.currency}</p>
            <p className="text-2xl font-bold mt-3">${(a.balance / 100).toLocaleString()}</p>
            <p className="text-[10px] text-gray-400 mt-2 font-mono">{a.id}</p>
          </div>
        ))}
      </div>
    </div>
  );
}
