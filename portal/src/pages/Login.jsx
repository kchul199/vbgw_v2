import React, { useState } from 'react';
import { Navigate } from 'react-router-dom';
import { useAuth } from '../context/AuthContext';
import { useEnv } from '../context/EnvContext';

export function Login() {
  const { login, isAuthenticated } = useAuth();
  const { activeEnv, currentProfile, switchEnv, profiles } = useEnv();
  const [apiKey, setApiKey] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleSubmit = async (e) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    
    const success = await login(apiKey);
    if (!success) {
      setError('인증 실패: API 키가 올바르지 않거나 서버에 접속할 수 없습니다.');
    }
    setLoading(false);
  };

  if (isAuthenticated) {
    return <Navigate to="/" replace />;
  }

  return (
    <div className="login-page">
      <div className="login-card">
        <h1 className="login-title">VBGW Portal</h1>
        <p className="login-subtitle">통합 운영 포탈에 로그인하세요</p>
        
        {error && <div className="toast toast-error" style={{ position: 'relative', top: 0, right: 0, marginBottom: '20px' }}>{error}</div>}
        
        <form onSubmit={handleSubmit}>
          <div className="form-group">
            <label className="form-label">환경 선택</label>
            <select 
              className="select" 
              value={activeEnv} 
              onChange={(e) => switchEnv(e.target.value)}
              style={{ width: '100%', marginBottom: '8px' }}
            >
              {Object.entries(profiles).map(([key, p]) => (
                <option key={key} value={key}>{p.name} ({key})</option>
              ))}
            </select>
            <div style={{ fontSize: '12px', color: 'var(--text-muted)' }}>
              연결 대상: {currentProfile.apiUrl || '현재 호스트 (로컬)'}
            </div>
          </div>
          
          <div className="form-group">
            <label className="form-label">Admin API Key</label>
            <input 
              type="password" 
              className="input" 
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder="Orchestrator ADMIN_API_KEY 입력"
              required
            />
          </div>
          
          <button type="submit" className="btn btn-primary" style={{ width: '100%', justifyContent: 'center' }} disabled={loading}>
            {loading ? '인증 중...' : '로그인'}
          </button>
        </form>
      </div>
    </div>
  );
}
