import React, { useState } from 'react';
import { PageContainer } from '../components/Layout/PageContainer';
import { useEnv } from '../context/EnvContext';

export function Environment() {
  const { activeEnv, profiles, currentProfile, switchEnv, updateProfile } = useEnv();
  const [editMode, setEditMode] = useState(false);
  const [formData, setFormData] = useState({ ...currentProfile });

  const handleSave = (e) => {
    e.preventDefault();
    updateProfile(activeEnv, formData);
    setEditMode(false);
    alert('설정이 저장되었습니다.');
  };

  const handleCancel = () => {
    setFormData({ ...currentProfile });
    setEditMode(false);
  };

  return (
    <PageContainer title="환경 설정">
      <div className="grid-2">
        <div className="card">
          <div className="card-header">
            <h3 className="card-title">프로파일 관리</h3>
            {!editMode && (
              <button className="btn btn-ghost btn-sm" onClick={() => setEditMode(true)}>
                수정
              </button>
            )}
          </div>

          <div className="form-group" style={{ marginBottom: '24px' }}>
            <label className="form-label">현재 환경 프로파일</label>
            <div style={{ display: 'flex', gap: '8px' }}>
              {Object.keys(profiles).map(key => (
                <button 
                  key={key}
                  className={`btn ${activeEnv === key ? 'btn-primary' : 'btn-ghost'}`}
                  onClick={() => {
                    if (!editMode) switchEnv(key);
                  }}
                  disabled={editMode}
                >
                  {profiles[key].name}
                </button>
              ))}
            </div>
          </div>

          <form onSubmit={handleSave}>
            <div className="form-group">
              <label className="form-label">환경 이름</label>
              <input className="input" value={formData.name} onChange={e => setFormData({...formData, name: e.target.value})} disabled={!editMode} />
            </div>
            <div className="form-group">
              <label className="form-label">Orchestrator API URL</label>
              <input className="input" value={formData.apiUrl} onChange={e => setFormData({...formData, apiUrl: e.target.value})} disabled={!editMode} placeholder="비워두면 현재 호스트(Vite Proxy) 사용" />
            </div>
            <div className="form-group">
              <label className="form-label">Admin API Key</label>
              <input type="password" className="input" value={formData.apiKey} onChange={e => setFormData({...formData, apiKey: e.target.value})} disabled={!editMode} />
            </div>
            <div className="form-group">
              <label className="form-label">Grafana URL</label>
              <input className="input" value={formData.grafanaUrl} onChange={e => setFormData({...formData, grafanaUrl: e.target.value})} disabled={!editMode} />
            </div>
            <div className="form-group">
              <label className="form-label">Jaeger URL</label>
              <input className="input" value={formData.jaegerUrl} onChange={e => setFormData({...formData, jaegerUrl: e.target.value})} disabled={!editMode} />
            </div>

            {editMode && (
              <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px', marginTop: '24px' }}>
                <button type="button" className="btn btn-ghost" onClick={handleCancel}>취소</button>
                <button type="submit" className="btn btn-primary">저장</button>
              </div>
            )}
          </form>
        </div>

        <div className="card">
          <div className="card-header">
            <h3 className="card-title">환경 변수 가이드</h3>
          </div>
          <p style={{ fontSize: '12px', color: 'var(--text-muted)', marginBottom: '16px' }}>
            docker-compose에 설정된 주요 인프라 연동 변수 목록 (참조용)
          </p>
          
          <div style={{ background: 'var(--bg-input)', padding: '16px', borderRadius: 'var(--radius-sm)' }}>
            <h4 style={{ fontSize: '12px', color: 'var(--text-secondary)', marginBottom: '8px' }}>PBX 게이트웨이 연동</h4>
            <ul style={{ fontSize: '13px', color: 'var(--text-primary)', marginBottom: '16px', paddingLeft: '20px' }}>
              <li><code>PBX_MAIN_REGISTER=true</code> : 레지스트리 방식</li>
              <li><code>PBX_MAIN_PROXY</code> : PBX SIP 도메인/IP</li>
              <li><code>PBX_MAIN_USERNAME</code> : 인증 계정</li>
              <li><code>PBX_MAIN_EXTENSION</code> : Inbound 내선 번호</li>
            </ul>

            <h4 style={{ fontSize: '12px', color: 'var(--text-secondary)', marginBottom: '8px' }}>AI 엔진 설정</h4>
            <ul style={{ fontSize: '13px', color: 'var(--text-primary)', marginBottom: '16px', paddingLeft: '20px' }}>
              <li><code>AI_GRPC_ADDR=vbgw-ai:8091</code> : 엔진 엔드포인트</li>
              <li><code>AI_ROUTE_NUMBERS=9196</code> : AI Inbound 목적지</li>
            </ul>

            <h4 style={{ fontSize: '12px', color: 'var(--text-secondary)', marginBottom: '8px' }}>백엔드 연동</h4>
            <ul style={{ fontSize: '13px', color: 'var(--text-primary)', paddingLeft: '20px' }}>
              <li><code>REDIS_ADDR</code> : 세션/CDR 스토어</li>
              <li><code>CDR_WEBHOOK_URL</code> : 써드파티 콜백</li>
            </ul>
          </div>
        </div>
      </div>
    </PageContainer>
  );
}
