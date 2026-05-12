import React, { createContext, useContext, useState, useEffect } from 'react';
import api from '../api/client';

const AuthContext = createContext(null);

export function AuthProvider({ children }) {
  const [isAuthenticated, setIsAuthenticated] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  useEffect(() => {
    // Check if we have an API key and if it's valid
    const checkAuth = async () => {
      const key = api.apiKey;
      if (!key) {
        setIsAuthenticated(false);
        setLoading(false);
        return;
      }

      try {
        // Ping orchestrator
        await api.getHealth();
        setIsAuthenticated(true);
      } catch (err) {
        console.error('Auth check failed:', err);
        // We might be offline, or key is wrong. 
        // For simplicity in P1, if we have a key we assume logged in until a 401/403 happens.
        // But if health fails with 401, clear auth.
        if (err.message && err.message.includes('401')) {
          setIsAuthenticated(false);
          api.setApiKey('');
        } else {
          // Keep true if just network error to avoid logging out on connection drop
          setIsAuthenticated(true);
        }
      } finally {
        setLoading(false);
      }
    };

    checkAuth();
  }, []);

  const login = async (apiKey) => {
    try {
      setError(null);
      api.setApiKey(apiKey);
      // Validate
      await api.getHealth();
      setIsAuthenticated(true);
      return true;
    } catch (err) {
      setError('인증 실패: API 키가 올바르지 않거나 서버에 연결할 수 없습니다.');
      api.setApiKey('');
      return false;
    }
  };

  const logout = () => {
    api.setApiKey('');
    setIsAuthenticated(false);
  };

  if (loading) {
    return <div className="loading-overlay"><div className="spinner" /> 인증 확인 중...</div>;
  }

  return (
    <AuthContext.Provider value={{ isAuthenticated, login, logout, error }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  return useContext(AuthContext);
}
