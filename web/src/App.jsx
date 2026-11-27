import React, { Suspense, lazy } from 'react';
import { BrowserRouter, Navigate, Route, Routes, Link } from 'react-router-dom';
import { AuthProvider, useAuth } from './auth.jsx';
import Shell from './Shell.jsx';
import Splash from './Splash.jsx';
import { Toaster, Loading } from './components.jsx';

const Login = lazy(() => import('./pages/Login.jsx'));
const Home = lazy(() => import('./pages/Home.jsx'));
const Groups = lazy(() => import('./pages/Groups.jsx'));
const GroupDetail = lazy(() => import('./pages/GroupDetail.jsx'));
const Meetings = lazy(() => import('./pages/Meetings.jsx'));
const MeetingDetail = lazy(() => import('./pages/MeetingDetail.jsx'));
const Payments = lazy(() => import('./pages/Payments.jsx'));
const Loans = lazy(() => import('./pages/Loans.jsx'));
const LoanDetail = lazy(() => import('./pages/LoanDetail.jsx'));
const Dashboard = lazy(() => import('./pages/Dashboard.jsx'));
const Ranking = lazy(() => import('./pages/Ranking.jsx'));
const Learning = lazy(() => import('./pages/Learning.jsx'));
const Disputes = lazy(() => import('./pages/Disputes.jsx'));
const Tasks = lazy(() => import('./pages/Tasks.jsx'));
const Admin = lazy(() => import('./pages/Admin.jsx'));

function Protected({ children }) {
  const { user, loading } = useAuth();
  if (loading) return <Loading rows={2} />;
  if (!user) return <Navigate to="/login" replace />;
  return children;
}

function NotFound() {
  return <div className="card"><h1 className="page-title">Page not found</h1><p className="muted">That page does not exist in Pamoja.</p><Link className="btn btn-secondary" to="/">Go home</Link></div>;
}

function Inner() {
  const { splash, splashFade } = useAuth();
  return (
    <>
      {splash && <Splash fade={splashFade} />}
      <Suspense fallback={<div className="content"><Loading rows={4} /></div>}>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route element={<Protected><Shell /></Protected>}>
            <Route path="/" element={<Home />} />
            <Route path="/groups" element={<Groups />} />
            <Route path="/groups/:id" element={<GroupDetail />} />
            <Route path="/meetings" element={<Meetings />} />
            <Route path="/meetings/:id" element={<MeetingDetail />} />
            <Route path="/payments" element={<Payments />} />
            <Route path="/loans" element={<Loans />} />
            <Route path="/loans/:id" element={<LoanDetail />} />
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/ranking" element={<Ranking />} />
            <Route path="/learning" element={<Learning />} />
            <Route path="/disputes" element={<Disputes />} />
            <Route path="/tasks" element={<Tasks />} />
            <Route path="/admin/*" element={<Admin />} />
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </Suspense>
      <Toaster />
    </>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Inner />
      </AuthProvider>
    </BrowserRouter>
  );
}
