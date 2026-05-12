import React, { useState } from 'react';
import { PageContainer } from '../components/Layout/PageContainer';
import { usePolling } from '../hooks/usePolling';
import api from '../api/client';
import { PhoneForwarded, Hash, Mic, Ear, Link2, Unlink } from 'lucide-react';

export function Sessions() {
  const { data, loading, refresh } = usePolling(() => api.getActiveSessions(), 5000);
  const sessions = data?.data || [];
  
  const [selectedSession, setSelectedSession] = useState(null);
  const [dtmfInput, setDtmfInput] = useState('');
  const [transferTarget, setTransferTarget] = useState('');
  
  const handleAction = async (action, id, payload = null) => {
    try {
      switch (action) {
        case 'dtmf': await api.sendDtmf(id, payload); break;
        case 'transfer': await api.transfer(id, payload); break;
        case 'recordStart': await api.recordStart(id); break;
        case 'recordStop': await api.recordStop(id); break;
        // add bridge/eavesdrop wrappers in client if needed
      }
      refresh();
      alert('Action successful');
    } catch (err) {
      alert(`Action failed: ${err.message}`);
    }
  };

  return (
    <PageContainer 
      title="통화 관리 (Active Sessions)" 
      action={<div className="badge badge-info">총 {sessions.length} 건</div>}
    >
      <div className="card" style={{ padding: 0 }}>
        <div className="table-container">
          <table className="data-table">
            <thead>
              <tr>
                <th>Session ID</th>
                <th>Caller</th>
                <th>Dest</th>
                <th>Service</th>
                <th>State</th>
                <th>Duration</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sessions.length === 0 ? (
                <tr><td colSpan="7" style={{ textAlign: 'center', padding: '20px' }}>현재 활성화된 통화가 없습니다.</td></tr>
              ) : sessions.map(s => (
                <tr key={s.session_id}>
                  <td className="mono truncate" style={{ maxWidth: '120px' }} title={s.session_id}>{s.session_id}</td>
                  <td className="mono">{s.caller_id}</td>
                  <td className="mono">{s.dest_num}</td>
                  <td>{s.service_name || '-'}</td>
                  <td><span className="badge badge-success">{s.state}</span></td>
                  <td>{s.answered_at ? Math.floor((new Date() - new Date(s.answered_at)) / 1000) + 's' : '-'}</td>
                  <td>
                    <div style={{ display: 'flex', gap: '4px' }}>
                      <button className="btn btn-ghost btn-sm" onClick={() => { setSelectedSession(s); setDtmfInput(''); }} title="DTMF 전송"><Hash size={14}/></button>
                      <button className="btn btn-ghost btn-sm" onClick={() => { setSelectedSession(s); setTransferTarget(''); }} title="호 전환"><PhoneForwarded size={14}/></button>
                      <button className="btn btn-ghost btn-sm" onClick={() => handleAction('recordStart', s.session_id)} title="녹음 시작"><Mic size={14}/></button>
                      <button className="btn btn-ghost btn-sm" onClick={() => alert('구현 예정')} title="감청"><Ear size={14}/></button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* Action Modals */}
      {selectedSession && (
        <div className="modal-overlay">
          <div className="modal-content">
            <h3 className="modal-title">세션 액션: {selectedSession.caller_id}</h3>
            
            <div className="form-group">
              <label className="form-label">DTMF 전송</label>
              <div style={{ display: 'flex', gap: '8px' }}>
                <input className="input" value={dtmfInput} onChange={e => setDtmfInput(e.target.value)} placeholder="e.g. 1234#" />
                <button className="btn btn-primary" onClick={() => handleAction('dtmf', selectedSession.session_id, dtmfInput)}>전송</button>
              </div>
            </div>
            
            <div className="form-group" style={{ marginTop: '16px' }}>
              <label className="form-label">호 전환 (Blind Transfer)</label>
              <div style={{ display: 'flex', gap: '8px' }}>
                <input className="input" value={transferTarget} onChange={e => setTransferTarget(e.target.value)} placeholder="e.g. 1000" />
                <button className="btn btn-warning" onClick={() => handleAction('transfer', selectedSession.session_id, transferTarget)}>전환</button>
              </div>
            </div>
            
            <div className="modal-actions">
              <button className="btn btn-ghost" onClick={() => setSelectedSession(null)}>닫기</button>
            </div>
          </div>
        </div>
      )}
    </PageContainer>
  );
}
