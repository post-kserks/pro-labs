import { Navigate, Route, Routes } from 'react-router-dom';
import { Layout } from './components/Layout';
import { useAuth } from './store/auth';
import { LoginPage } from './pages/LoginPage';
import { DashboardPage } from './pages/DashboardPage';
import { PatientsPage } from './pages/PatientsPage';
import { PatientDetailPage } from './pages/PatientDetailPage';
import { TimelinePage } from './pages/TimelinePage';
import { AdminPage } from './pages/AdminPage';
import type { ReactNode } from 'react';

function RequireAuth({ children }: { children: ReactNode }) {
  const { token } = useAuth();
  if (!token) return <Navigate to="/login" replace />;
  return <Layout>{children}</Layout>;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/dashboard" element={<RequireAuth><DashboardPage /></RequireAuth>} />
      <Route path="/patients" element={<RequireAuth><PatientsPage /></RequireAuth>} />
      <Route path="/patients/:id" element={<RequireAuth><PatientDetailPage /></RequireAuth>} />
      <Route path="/patients/:id/timeline" element={<RequireAuth><TimelinePage /></RequireAuth>} />
      <Route path="/admin" element={<RequireAuth><AdminPage /></RequireAuth>} />
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  );
}
