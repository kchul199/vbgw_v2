import React, { useState } from 'react';
import { PageContainer } from '../components/Layout/PageContainer';
import { usePolling } from '../hooks/usePolling';
import api from '../api/client';

export function Operations() {
  const { data: opsData } = usePolling(() => api.getOperations(), 5000);
  const operations = opsData?.operations || [];
  
  const [selectedOp, setSelectedOp] = useState(null);

  return (
    <PageContainer title="운영 로그">
      <div className="card" style={{ padding: 0 }}>
        <div className="table-container">
          <table className="data-table">
            <thead>
              <tr>
                <th>Time</th>
                <th>Op ID</th>
                <th>Type</th>
                <th>User / Node</th>
                <th>Reason</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {operations.length === 0 ? (
                <tr><td colSpan="6" style={{ textAlign: 'center', padding: '20px' }}>운영 기록이 없습니다.</td></tr>
              ) : operations.map(op => (
                <tr key={op.id} onClick={() => setSelectedOp(op)} style={{ cursor: 'pointer' }}>
                  <td>{new Date(op.created_at).toLocaleString()}</td>
                  <td className="mono truncate" style={{ maxWidth: '100px' }}>{op.id}</td>
                  <td><span className="badge badge-info">{op.operation_type}</span></td>
                  <td>{op.executed_by || op.node_id}</td>
                  <td>{op.reason}</td>
                  <td>
                    <span className={`badge ${op.status === 'completed' ? 'badge-success' : 'badge-warning'}`}>
                      {op.status}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {selectedOp && (
        <div className="modal-overlay" onClick={() => setSelectedOp(null)}>
          <div className="modal-content" onClick={e => e.stopPropagation()}>
            <h3 className="modal-title">작업 상세 정보</h3>
            <div style={{ background: 'var(--bg-input)', padding: '16px', borderRadius: 'var(--radius-sm)', overflowX: 'auto' }}>
              <pre className="mono" style={{ fontSize: '12px', margin: 0, color: 'var(--text-primary)' }}>
                {JSON.stringify(selectedOp, null, 2)}
              </pre>
            </div>
            <div className="modal-actions">
              <button className="btn btn-ghost" onClick={() => setSelectedOp(null)}>닫기</button>
            </div>
          </div>
        </div>
      )}
    </PageContainer>
  );
}
