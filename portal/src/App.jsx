import React from 'react';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { AuthProvider, useAuth } from './context/AuthContext';
import { EnvProvider } from './context/EnvContext';

import { Login } from './pages/Login';
import { Dashboard } from './pages/Dashboard';
import { Sessions } from './pages/Sessions';
import { Services } from './pages/Services';
import { Infrastructure } from './pages/Infrastructure';
import { CallHistory } from './pages/CallHistory';
import { Operations } from './pages/Operations';
import { Environment } from './pages/Environment';

function ProtectedRoute({ children }) {
  const { isAuthenticated, error } = useAuth();
  
  if (!isAuthenticated && !error) {
    // If not authenticated and no specific error from login attempt, redirect to login
    return <Navigate to="/login" replace />;
  }
  
  if (!isAuthenticated && error) {
     return <Navigate to="/login" replace />;
  }

  return children;
}

function AppContent() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/" element={<ProtectedRoute><Dashboard /></ProtectedRoute>} />
      <Route path="/sessions" element={<ProtectedRoute><Sessions /></ProtectedRoute>} />
      <Route path="/services" element={<ProtectedRoute><Services /></ProtectedRoute>} />
      <Route path="/infrastructure" element={<ProtectedRoute><Infrastructure /></ProtectedRoute>} />
      <Route path="/history" element={<ProtectedRoute><CallHistory /></ProtectedRoute>} />
      <Route path="/operations" element={<ProtectedRoute><Operations /></ProtectedRoute>} />
      <Route path="/environment" element={<ProtectedRoute><Environment /></ProtectedRoute>} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <EnvProvider>
        <AuthProvider>
          <AppContent />
        </AuthProvider>
      </EnvProvider>
    </BrowserRouter>
  );
}
