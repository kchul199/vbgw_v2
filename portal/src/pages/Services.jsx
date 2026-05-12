import React, { useState } from 'react';
import { PageContainer } from '../components/Layout/PageContainer';
import { usePolling } from '../hooks/usePolling';
import api from '../api/client';
import { Play, Pause, Trash2, ShieldAlert } from 'lucide-react';

export function Services() {
  const { data: servicesData, refresh: refreshServices } = usePolling(() => api.getServices(), 5000);
  const { data: queuesData, refresh: refreshQueues } = usePolling(() => api.getQueues(), 5000);
  
  const services = servicesData?.services || [];
  const queues = queuesData?.queues || [];
  
  const [reason, setReason] = useState('');
  const [selectedAction, setSelectedAction] = useState(null); // { type, name }

  const handleAction = async () => {
    if (!reason.trim()) {
      alert('작업 사유를 입력하세요');
      return;
    }
    
    try {
      const { type, name } = selectedAction;
      if (type === 'pause') await api.pauseService(name, reason);
      else if (type === 'resume') await api.resumeService(name, reason);
      else if (type === 'drain') await api.drainService(name, reason);
      else if (type === 'flush') await api.flushQueue(name, reason);
      
      alert(`${type} 작업이 완료되었습니다.`);
      setReason('');
      setSelectedAction(null);
      refreshServices();
      refreshQueues();
    } catch (err) {
      alert(`작업 실패: ${err.message}`);
    }
  };

  return (
    <PageContainer title="서비스 관리">
      <div className="grid-2">
        <div className="card">
          <div className="card-header">
            <h3 className="card-title">서비스 상태</h3>
          </div>
          <div className="table-container">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Service</th>
                  <th>Status</th>
                  <th>Capacity</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {services.map(s => (
                  <tr key={s.name}>
                    <td className="mono">{s.name}</td>
                    <td>
                      <span className={`badge ${s.status === 'active' ? 'badge-success' : s.status === 'paused' ? 'badge-warning' : 'badge-danger'}`}>
                        {s.status}
                      </span>
                    </td>
                    <td>{s.capacity.active_sessions} / {s.capacity.max_sessions}</td>
                    <td>
                      <div style={{ display: 'flex', gap: '4px' }}>
                        {s.status !== 'active' && (
                          <button className="btn btn-ghost btn-sm" onClick={() => setSelectedAction({ type: 'resume', name: s.name })} title="Resume">
                            <Play size={14} />
                          </button>
                        )}
                        {s.status === 'active' && (
                          <button className="btn btn-ghost btn-sm" onClick={() => setSelectedAction({ type: 'pause', name: s.name })} title="Pause (Block new)">
                            <Pause size={14} />
                          </button>
                        )}
                        {s.status !== 'drained' && (
                          <button className="btn btn-ghost btn-sm" onClick={() => setSelectedAction({ type: 'drain', name: s.name })} title="Drain (Force end)">
                            <ShieldAlert size={14} />
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        <div className="card">
          <div className="card-header">
            <h3 className="card-title">서비스 큐 (Overflow)</h3>
          </div>
          <div className="table-container">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Queue</th>
                  <th>Depth</th>
                  <th>Oldest Wait</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {queues.length === 0 ? (
                  <tr><td colSpan="4" style={{ textAlign: 'center', padding: '20px' }}>활성화된 큐가 없습니다.</td></tr>
                ) : queues.map(q => (
                  <tr key={q.name}>
                    <td className="mono">{q.name}</td>
                    <td>{q.depth}</td>
                    <td>{q.oldest_wait_seconds}s</td>
                    <td>
                      <button className="btn btn-danger btn-sm" onClick={() => setSelectedAction({ type: 'flush', name: q.name })} title="Flush Queue">
                        <Trash2 size={14} /> Flush
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      {selectedAction && (
        <div className="modal-overlay">
          <div className="modal-content">
            <h3 className="modal-title">작업 확인: {selectedAction.type.toUpperCase()}</h3>
            <p style={{ marginBottom: '16px' }}>대상: {selectedAction.name}</p>
            <div className="form-group">
              <label className="form-label">작업 사유 (필수)</label>
              <input 
                className="input" 
                value={reason} 
                onChange={e => setReason(e.target.value)} 
                placeholder="예: 긴급 점검, 배포 등"
                autoFocus
              />
            </div>
            <div className="modal-actions">
              <button className="btn btn-ghost" onClick={() => { setSelectedAction(null); setReason(''); }}>취소</button>
              <button className="btn btn-primary" onClick={handleAction}>실행</button>
            </div>
          </div>
        </div>
      )}
    </PageContainer>
  );
}
