import { useEffect, useState } from 'react';
import {
  BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer,
  LineChart, Line, PieChart, Pie, Cell,
} from 'recharts';
import { Activity, DollarSign, Users, CreditCard, Shield, TrendingUp } from 'lucide-react';

const COLORS = ['#3b82f6', '#22c55e', '#f59e0b', '#ef4444', '#8b5cf6'];

const mockTxnVolume = [
  { hour: '00:00', count: 120 }, { hour: '04:00', count: 45 },
  { hour: '08:00', count: 320 }, { hour: '12:00', count: 580 },
  { hour: '16:00', count: 410 }, { hour: '20:00', count: 290 },
];
const mockServiceLatency = [
  { service: 'Auth', p50: 12, p95: 45, p99: 120 },
  { service: 'Account', p50: 18, p95: 52, p99: 89 },
  { service: 'Txn', p50: 35, p95: 95, p99: 180 },
  { service: 'Ledger', p50: 22, p95: 68, p99: 110 },
  { service: 'Payment', p50: 85, p95: 190, p99: 350 },
  { service: 'Notify', p50: 8, p95: 25, p99: 55 },
];
const mockStatusDist = [
  { name: 'Completed', value: 7850 }, { name: 'Pending', value: 320 },
  { name: 'Failed', value: 85 }, { name: 'Reversed', value: 45 },
];
const services = [
  { name: 'API Gateway', port: 8080, lang: 'Go', status: 'healthy' },
  { name: 'Auth Service', port: 8081, lang: 'Go', status: 'healthy' },
  { name: 'User/KYC', port: 8082, lang: 'Go', status: 'healthy' },
  { name: 'Account', port: 8083, lang: 'Go', status: 'healthy' },
  { name: 'Transaction', port: 8084, lang: 'Go', status: 'healthy' },
  { name: 'Ledger', port: 8085, lang: 'Go', status: 'healthy' },
  { name: 'Payment', port: 8086, lang: 'Go', status: 'healthy' },
  { name: 'Notification', port: 8087, lang: 'Go', status: 'healthy' },
  { name: 'Audit', port: 8088, lang: 'Go', status: 'healthy' },
  { name: 'Fraud Detection', port: 8089, lang: 'Python', status: 'healthy' },
  { name: 'Risk Engine', port: 8090, lang: 'Python', status: 'healthy' },
  { name: 'Real-time WS', port: 8091, lang: 'Node.js', status: 'healthy' },
];

function StatCard({ title, value, sub, icon: Icon, color }: {
  title: string; value: string; sub: string; icon: React.ElementType; color: string;
}) {
  return (
    <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm text-gray-500">{title}</p>
          <p className="text-2xl font-bold mt-1">{value}</p>
          <p className="text-xs text-gray-400 mt-1">{sub}</p>
        </div>
        <div className={`p-3 rounded-lg ${color}`}>
          <Icon size={24} className="text-white" />
        </div>
      </div>
    </div>
  );
}

export default function Dashboard() {
  const [time, setTime] = useState(new Date());
  useEffect(() => {
    const t = setInterval(() => setTime(new Date()), 1000);
    return () => clearInterval(t);
  }, []);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-gray-900">Dashboard</h2>
          <p className="text-sm text-gray-500">Real-time overview of PayFlow operations</p>
        </div>
        <span className="text-sm text-gray-400 font-mono">{time.toLocaleTimeString()}</span>
      </div>

      {/* KPI Cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 xl:grid-cols-6 gap-4">
        <StatCard title="Total Volume" value="$2.4M" sub="Today" icon={DollarSign} color="bg-blue-500" />
        <StatCard title="Transactions" value="8,300" sub="Last 24h" icon={Activity} color="bg-green-500" />
        <StatCard title="Active Users" value="1,240" sub="Online now" icon={Users} color="bg-purple-500" />
        <StatCard title="Payments" value="2,150" sub="Processed today" icon={CreditCard} color="bg-amber-500" />
        <StatCard title="Fraud Blocked" value="12" sub="Last 24h" icon={Shield} color="bg-red-500" />
        <StatCard title="Success Rate" value="99.2%" sub="All services" icon={TrendingUp} color="bg-teal-500" />
      </div>

      {/* Charts Row */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div className="bg-white rounded-xl shadow-sm border p-6">
          <h3 className="text-lg font-semibold mb-4">Transaction Volume (Today)</h3>
          <ResponsiveContainer width="100%" height={250}>
            <LineChart data={mockTxnVolume}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="hour" />
              <YAxis />
              <Tooltip />
              <Line type="monotone" dataKey="count" stroke="#3b82f6" strokeWidth={2} dot={false} />
            </LineChart>
          </ResponsiveContainer>
        </div>

        <div className="bg-white rounded-xl shadow-sm border p-6">
          <h3 className="text-lg font-semibold mb-4">Service Latency (p95 ms)</h3>
          <ResponsiveContainer width="100%" height={250}>
            <BarChart data={mockServiceLatency}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="service" />
              <YAxis />
              <Tooltip />
              <Bar dataKey="p50" fill="#22c55e" name="p50" />
              <Bar dataKey="p95" fill="#f59e0b" name="p95" />
              <Bar dataKey="p99" fill="#ef4444" name="p99" />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        {/* Transaction Status */}
        <div className="bg-white rounded-xl shadow-sm border p-6">
          <h3 className="text-lg font-semibold mb-4">Transaction Status</h3>
          <ResponsiveContainer width="100%" height={200}>
            <PieChart>
              <Pie data={mockStatusDist} cx="50%" cy="50%" innerRadius={60} outerRadius={80} dataKey="value" label>
                {mockStatusDist.map((_, i) => <Cell key={i} fill={COLORS[i]} />)}
              </Pie>
              <Tooltip />
            </PieChart>
          </ResponsiveContainer>
        </div>

        {/* Service Health Grid */}
        <div className="bg-white rounded-xl shadow-sm border p-6 col-span-2">
          <h3 className="text-lg font-semibold mb-4">Service Health Matrix</h3>
          <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-3">
            {services.map((s) => (
              <div key={s.name} className="border rounded-lg p-3 flex flex-col">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-medium">{s.name}</span>
                  <span className={`w-2 h-2 rounded-full ${s.status === 'healthy' ? 'bg-green-500' : 'bg-red-500'}`} />
                </div>
                <div className="flex items-center justify-between mt-1">
                  <span className="text-[10px] text-gray-400">:{s.port}</span>
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-gray-100 text-gray-600">{s.lang}</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
