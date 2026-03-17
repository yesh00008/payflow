import { useState } from 'react';

export default function Login() {
  const [email, setEmail] = useState('admin@payflow.dev');
  const [password, setPassword] = useState('');
  const [isRegister, setIsRegister] = useState(false);
  const [fullName, setFullName] = useState('');

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    // In production, call authAPI.login() or authAPI.register()
    localStorage.setItem('payflow_token', 'demo_jwt_token');
    window.location.href = '/';
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-gray-900 via-blue-900 to-gray-900">
      <div className="bg-white rounded-2xl shadow-2xl p-8 w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-primary-600">PayFlow</h1>
          <p className="text-sm text-gray-500 mt-1">Digital Payments Benchmark Platform</p>
          <div className="flex justify-center gap-2 mt-3">
            <span className="text-[10px] px-2 py-0.5 rounded-full bg-blue-100 text-blue-700">Go</span>
            <span className="text-[10px] px-2 py-0.5 rounded-full bg-green-100 text-green-700">Python</span>
            <span className="text-[10px] px-2 py-0.5 rounded-full bg-yellow-100 text-yellow-700">Node.js</span>
            <span className="text-[10px] px-2 py-0.5 rounded-full bg-purple-100 text-purple-700">React</span>
          </div>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          {isRegister && (
            <input value={fullName} onChange={(e) => setFullName(e.target.value)} placeholder="Full Name"
              className="w-full border rounded-lg px-4 py-2.5 text-sm" required />
          )}
          <input value={email} onChange={(e) => setEmail(e.target.value)} placeholder="Email" type="email"
            className="w-full border rounded-lg px-4 py-2.5 text-sm" required />
          <input value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Password" type="password"
            className="w-full border rounded-lg px-4 py-2.5 text-sm" required />
          <button type="submit" className="w-full bg-primary-600 text-white py-2.5 rounded-lg hover:bg-primary-700 text-sm font-semibold">
            {isRegister ? 'Create Account' : 'Sign In'}
          </button>
        </form>

        <p className="text-center text-sm text-gray-500 mt-4">
          {isRegister ? 'Already have an account?' : "Don't have an account?"}{' '}
          <button onClick={() => setIsRegister(!isRegister)} className="text-primary-600 hover:underline">
            {isRegister ? 'Sign In' : 'Register'}
          </button>
        </p>

        <div className="mt-6 p-3 bg-gray-50 rounded-lg text-xs text-gray-500">
          <strong>Demo:</strong> admin@payflow.dev / any password<br />
          <strong>Services:</strong> 12 microservices across Go, Python (FastAPI), Node.js (Express)
        </div>
      </div>
    </div>
  );
}
