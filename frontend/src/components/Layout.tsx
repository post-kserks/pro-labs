import type { ReactNode } from 'react';
import { NavLink } from 'react-router-dom';
import { Activity, BarChart2, LogOut, Users } from 'lucide-react';
import { useAuth } from '../store/auth';

const navItems = [
  { to: '/dashboard', icon: Activity, label: 'Дашборд' },
  { to: '/patients', icon: Users, label: 'Пациенты' },
  { to: '/admin', icon: BarChart2, label: 'Администратор', role: 'admin' },
];

export function Layout({ children }: { children: ReactNode }) {
  const { user, logout } = useAuth();

  return (
    <div className="flex h-screen bg-gray-50">
      <aside className="w-64 bg-white border-r border-gray-200 flex flex-col">
        <div className="p-6 border-b border-gray-200">
          <h1 className="text-xl font-bold text-blue-600">🏥 MedVault</h1>
          <p className="text-xs text-gray-500 mt-1">на базе VaultDB</p>
        </div>

        <nav className="flex-1 p-4 space-y-1">
          {navItems.map((item) => {
            if (item.role && user?.role !== item.role) return null;
            return (
              <NavLink
                key={item.to}
                to={item.to}
                className={({ isActive }) =>
                  `flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors ${
                    isActive ? 'bg-blue-50 text-blue-700 font-medium' : 'text-gray-600 hover:bg-gray-50'
                  }`
                }
              >
                <item.icon className="w-4 h-4" />
                {item.label}
              </NavLink>
            );
          })}
        </nav>

        <div className="p-4 border-t border-gray-200">
          <div className="flex items-center gap-3 mb-3">
            <div className="w-8 h-8 rounded-full bg-blue-100 flex items-center justify-center">
              <span className="text-blue-600 text-sm font-medium">{user?.name?.[0] ?? '?'}</span>
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-gray-900 truncate">{user?.name}</p>
              <p className="text-xs text-gray-500 capitalize">{user?.role}</p>
            </div>
          </div>
          <button
            onClick={logout}
            className="flex items-center gap-2 w-full px-3 py-2 text-sm text-gray-600 hover:bg-gray-50 rounded-lg transition-colors"
          >
            <LogOut className="w-4 h-4" />
            Выйти
          </button>
        </div>
      </aside>

      <main className="flex-1 overflow-y-auto p-6">{children}</main>
    </div>
  );
}
