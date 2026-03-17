import { Shield, Upload, CheckCircle, XCircle, Clock } from 'lucide-react';

export default function KYC() {
  const documents = [
    { id: 'doc-001', type: 'passport', status: 'approved', uploaded: '2026-02-10', reviewed: '2026-02-11' },
    { id: 'doc-002', type: 'utility_bill', status: 'pending', uploaded: '2026-02-13', reviewed: null },
    { id: 'doc-003', type: 'drivers_license', status: 'rejected', uploaded: '2026-02-08', reviewed: '2026-02-09' },
  ];

  const statusIcon = (s: string) => {
    if (s === 'approved') return <CheckCircle size={16} className="text-green-500" />;
    if (s === 'rejected') return <XCircle size={16} className="text-red-500" />;
    return <Clock size={16} className="text-yellow-500" />;
  };

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold">KYC Verification</h2>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div className="bg-white rounded-xl shadow-sm border p-6 text-center">
          <Shield size={32} className="mx-auto text-green-500 mb-2" />
          <p className="font-semibold">KYC Status</p>
          <p className="text-lg font-bold text-green-600 mt-1">Verified</p>
        </div>
        <div className="bg-white rounded-xl shadow-sm border p-6 text-center">
          <p className="text-sm text-gray-500">Risk Score</p>
          <p className="text-3xl font-bold text-blue-600 mt-1">12 / 100</p>
          <p className="text-xs text-gray-400">Low Risk</p>
        </div>
        <div className="bg-white rounded-xl shadow-sm border p-6 text-center">
          <p className="text-sm text-gray-500">Documents</p>
          <p className="text-3xl font-bold mt-1">3</p>
          <p className="text-xs text-gray-400">1 approved, 1 pending, 1 rejected</p>
        </div>
      </div>

      <div className="bg-white rounded-xl shadow-sm border p-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="font-semibold">Upload Document</h3>
        </div>
        <div className="border-2 border-dashed border-gray-300 rounded-lg p-8 text-center">
          <Upload size={32} className="mx-auto text-gray-400 mb-2" />
          <p className="text-sm text-gray-500">Drag & drop or click to upload</p>
          <p className="text-xs text-gray-400 mt-1">Passport, Driver's License, or Utility Bill</p>
          <p className="text-xs text-gray-400 mt-1">Files stored in MinIO (S3-compatible)</p>
        </div>
      </div>

      <div className="bg-white rounded-xl shadow-sm border overflow-hidden">
        <div className="p-4 border-b"><h3 className="font-semibold">Submitted Documents</h3></div>
        <table className="w-full text-sm">
          <thead className="bg-gray-50"><tr>
            <th className="text-left p-3">ID</th><th className="text-left p-3">Type</th>
            <th className="text-left p-3">Status</th><th className="text-left p-3">Uploaded</th><th className="text-left p-3">Reviewed</th>
          </tr></thead>
          <tbody>
            {documents.map((d) => (
              <tr key={d.id} className="border-t hover:bg-gray-50">
                <td className="p-3 font-mono text-xs">{d.id}</td>
                <td className="p-3 capitalize">{d.type.replace('_', ' ')}</td>
                <td className="p-3 flex items-center gap-1">{statusIcon(d.status)} <span className="capitalize">{d.status}</span></td>
                <td className="p-3 text-gray-500">{d.uploaded}</td>
                <td className="p-3 text-gray-500">{d.reviewed ?? '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
