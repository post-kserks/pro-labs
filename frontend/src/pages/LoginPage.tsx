import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { login } from '../api/client';
import { setAuth, useAuth } from '../store/auth';

const demoAccounts = [
  { label: 'Врач', email: 'doctor@clinic.ru' },
  { label: 'Администратор', email: 'admin@clinic.ru' },
  { label: 'Регистратор', email: 'receptionist@clinic.ru' },
];

export function LoginPage() {
  const navigate = useNavigate();
  const { token } = useAuth();
  const [email, setEmail] = useState('doctor@clinic.ru');
  const [password, setPassword] = useState('demo123');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  if (token) {
    navigate('/dashboard', { replace: true });
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      const { token, user } = await login(email, password);
      setAuth(token, user);
      navigate('/dashboard', { replace: true });
    } catch (err: any) {
      setError(err?.response?.data?.error?.message ?? 'Неверный email или пароль');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-blue-50 to-slate-100 p-4">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="text-4xl font-bold text-blue-600">🏥 MedVault</h1>
          <p className="text-gray-600 mt-2">Электронная медицинская карта на базе VaultDB</p>
        </div>

        <form onSubmit={onSubmit} className="bg-white rounded-2xl shadow-sm border border-gray-200 p-8 space-y-5">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Email</label>
            <input
              name="email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="w-full border border-gray-300 rounded-lg px-3 py-2 focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Пароль</label>
            <input
              name="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full border border-gray-300 rounded-lg px-3 py-2 focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            />
          </div>

          {error && <p className="text-sm text-red-600">{error}</p>}

          <button
            type="submit"
            disabled={loading}
            className="w-full bg-blue-600 hover:bg-blue-700 text-white rounded-lg py-2.5 font-medium transition-colors disabled:opacity-50"
          >
            {loading ? 'Вход…' : 'Войти'}
          </button>

          <div className="pt-2 border-t border-gray-100">
            <p className="text-xs text-gray-400 mb-2">Демо-аккаунты (пароль: demo123):</p>
            <div className="flex flex-wrap gap-2">
              {demoAccounts.map((a) => (
                <button
                  key={a.email}
                  type="button"
                  onClick={() => {
                    setEmail(a.email);
                    setPassword('demo123');
                  }}
                  className="text-xs px-2.5 py-1 bg-gray-100 hover:bg-gray-200 rounded-full text-gray-700"
                >
                  {a.label}
                </button>
              ))}
            </div>
          </div>
        </form>

        <p className="text-center text-xs text-gray-400 mt-6">
          Powered by VaultDB — собственная СУБД с Time Travel
        </p>
      </div>
    </div>
  );
}
