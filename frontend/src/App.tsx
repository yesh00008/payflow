import { Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import Dashboard from './pages/Dashboard';
import Accounts from './pages/Accounts';
import Transfers from './pages/Transfers';
import Payments from './pages/Payments';
import Transactions from './pages/Transactions';
import Login from './pages/Login';
import KYC from './pages/KYC';
import AuditLogs from './pages/AuditLogs';

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/" element={<Layout />}>
        <Route index element={<Dashboard />} />
        <Route path="accounts" element={<Accounts />} />
        <Route path="transfers" element={<Transfers />} />
        <Route path="payments" element={<Payments />} />
        <Route path="transactions" element={<Transactions />} />
        <Route path="kyc" element={<KYC />} />
        <Route path="audit" element={<AuditLogs />} />
      </Route>
    </Routes>
  );
}
