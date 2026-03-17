import { Outlet, Link, useLocation } from 'react-router-dom';
import {
  LayoutDashboard, Wallet, ArrowLeftRight, CreditCard,
  Receipt, Shield, FileSearch, LogOut,
} from 'lucide-react';
import clsx from 'clsx';

const navItems = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/accounts', label: 'Accounts', icon: Wallet },
  { to: '/transfers', label: 'Transfers', icon: ArrowLeftRight },
  { to: '/payments', label: 'Payments', icon: CreditCard },
  { to: '/transactions', label: 'Transactions', icon: Receipt },
  { to: '/kyc', label: 'KYC Verification', icon: Shield },
  { to: '/audit', label: 'Audit Logs', icon: FileSearch },
];

export default function Layout() {
  const location = useLocation();

  const handleLogout = () => {
    localStorage.removeItem('payflow_token');
    window.location.href = '/login';
  };

  return (
    <div className="flex h-screen bg-gray-50">
      {/* Sidebar */}
      <aside className="w-64 bg-gray-900 text-white flex flex-col">
        <div className="p-6 border-b border-gray-700">
          <h1 className="text-2xl font-bold text-primary-400">PayFlow</h1>
          <p className="text-xs text-gray-400 mt-1">Digital Payments Platform</p>
        </div>

        <nav className="flex-1 py-4">
          {navItems.map(({ to, label, icon: Icon }) => (
            <Link
              key={to}
              to={to}
              className={clsx(
                'flex items-center gap-3 px-6 py-3 text-sm transition-colors',
                location.pathname === to
                  ? 'bg-primary-600 text-white border-r-4 border-primary-400'
                  : 'text-gray-300 hover:bg-gray-800 hover:text-white',
              )}
            >
              <Icon size={18} />
              {label}
            </Link>
          ))}
        </nav>

        <div className="p-4 border-t border-gray-700">
          <div className="text-xs text-gray-500 mb-2">
            Go · Python · Node.js · React
          </div>
          <button
            onClick={handleLogout}
            className="flex items-center gap-2 text-sm text-gray-400 hover:text-red-400 transition-colors"
          >
            <LogOut size={16} /> Sign Out
          </button>
        </div>
      </aside>

      {/* Main Content */}
      <main className="flex-1 overflow-auto">
        <header className="bg-white border-b border-gray-200 px-8 py-4 flex items-center justify-between">
          <div>
            <p className="text-xs text-gray-500">Fintech Microservices Benchmark</p>
          </div>
          <div className="flex items-center gap-4">
            <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-800">
              9 Services Running
            </span>
            <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-800">
              v1.0.0
            </span>
          </div>
        </header>
        <div className="p-8">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
