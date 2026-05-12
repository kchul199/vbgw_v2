import React, { useState } from 'react';
import { PageContainer } from '../components/Layout/PageContainer';
import { usePolling } from '../hooks/usePolling';
import api from '../api/client';
import { RefreshCw, Play, Pause } from 'lucide-react';

export function Infrastructure() {
  const { data: gatewaysData, refresh: refreshGateways } = usePolling(() => api.getGateways(), 10000);
  const { data: clusterData, refresh: refreshCluster } = usePolling(() => api.getClusterNodes(), 10000);
  const { data: routingData } = usePolling(() => api.getRoutingConfig(), 10000);
  
  const gateways = gatewaysData?.gateways || [];
  const nodes = clusterData?.nodes || [];
  const routing = routingData?.data || null;

  const [reason, setReason] = useState('');
  const [selectedAction, setSelectedAction] = useState(null); // { type, target, label }

  const handleAction = async () => {
    if (!reason.trim()) {
      alert('작업 사유를 입력하세요');
      return;
    }
    
    try {
      const { type, target } = selectedAction;
      if (type === 'standby-enable') await api.setGatewayStandby(target, reason, true);
      else if (type === 'standby-disable') await api.setGatewayStandby(target, reason, false);
      else if (type === 'node-drain') await api.drainNode(target, reason);
      else if (type === 'node-resume') await api.resumeNode(target, reason);
      else if (type === 'routing-reload') await api.reloadRouting(reason);
      
      alert('작업이 완료되었습니다.');
      setReason('');
      setSelectedAction(null);
      refreshGateways();
      refreshCluster();
    } catch (err) {
      alert(`작업 실패: ${err.message}`);
    }
  };

  return (
    <PageContainer title="인프라 연동 설정">
      <div className="grid-2">
        <div className="card">
          <div className="card-header">
            <h3 className="card-title">게이트웨이 (PBX / SBC)</h3>
          </div>
          <p style={{ fontSize: '12px', color: 'var(--text-muted)', marginBottom: '16px' }}>
            연동 방식: <strong>레지스트리 (PBX_MAIN_REGISTER=true)</strong>
          </p>
          <div className="table-container">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Gateway ID</th>
                  <th>Health</th>
                  <th>Ping (ms)</th>
                  <th>Standby</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {gateways.map(gw => (
                  <tr key={gw.id}>
                    <td className="mono">{gw.id}</td>
                    <td>
                      <span className={`badge ${gw.health_class === 'healthy' ? 'badge-success' : 'badge-danger'}`}>
                        {gw.health_class}
                      </span>
                    </td>
                    <td>{gw.ping_latency_ms}</td>
                    <td>
                      <span className={`badge ${gw.is_standby ? 'badge-warning' : 'badge-muted'}`}>
                        {gw.is_standby ? 'ON' : 'OFF'}
                      </span>
                    </td>
                    <td>
                      {!gw.is_standby ? (
                        <button className="btn btn-ghost btn-sm" onClick={() => setSelectedAction({ type: 'standby-enable', target: gw.id, label: `Standby 전환 (${gw.id})` })}>
                          Set Standby
                        </button>
                      ) : (
                        <button className="btn btn-ghost btn-sm" onClick={() => setSelectedAction({ type: 'standby-disable', target: gw.id, label: `Standby 해제 (${gw.id})` })}>
                          Clear Standby
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        <div className="card">
          <div className="card-header">
            <h3 className="card-title">클러스터 노드</h3>
          </div>
          <div className="table-container">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Node ID</th>
                  <th>Status</th>
                  <th>Uptime (s)</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {nodes.map(n => (
                  <tr key={n.id}>
                    <td className="mono">{n.id}</td>
                    <td>
                      <span className={`badge ${n.status === 'active' ? 'badge-success' : 'badge-warning'}`}>
                        {n.status}
                      </span>
                    </td>
                    <td>{n.uptime_seconds}</td>
                    <td>
                      {n.status === 'active' ? (
                        <button className="btn btn-ghost btn-sm" onClick={() => setSelectedAction({ type: 'node-drain', target: n.id, label: `Node Drain (${n.id})` })}>
                          <Pause size={14} /> Drain
                        </button>
                      ) : (
                        <button className="btn btn-ghost btn-sm" onClick={() => setSelectedAction({ type: 'node-resume', target: n.id, label: `Node Resume (${n.id})` })}>
                          <Play size={14} /> Resume
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        <div className="card" style={{ gridColumn: '1 / -1' }}>
          <div className="card-header">
            <h3 className="card-title">라우팅 설정 (routing.yaml)</h3>
            <button className="btn btn-primary btn-sm" onClick={() => setSelectedAction({ type: 'routing-reload', target: null, label: '라우팅 리로드' })}>
              <RefreshCw size={14} /> 리로드
            </button>
          </div>
          <div style={{ background: 'var(--bg-input)', padding: '16px', borderRadius: 'var(--radius-sm)', overflowX: 'auto' }}>
            <pre className="mono" style={{ fontSize: '12px', margin: 0, color: 'var(--text-primary)' }}>
              {routing ? JSON.stringify(routing, null, 2) : 'Loading...'}
            </pre>
          </div>
        </div>
      </div>

      {selectedAction && (
        <div className="modal-overlay">
          <div className="modal-content">
            <h3 className="modal-title">인프라 조작 확인</h3>
            <p style={{ marginBottom: '16px' }}>작업: {selectedAction.label}</p>
            <div className="form-group">
              <label className="form-label">작업 사유 (필수)</label>
              <input 
                className="input" 
                value={reason} 
                onChange={e => setReason(e.target.value)} 
                placeholder="예: 게이트웨이 점검, 설정 갱신 등"
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
