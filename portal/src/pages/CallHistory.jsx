import React, { useState, useEffect } from 'react';
import { PageContainer } from '../components/Layout/PageContainer';
import api from '../api/client';
import { useEnv } from '../context/EnvContext';
import { Search, ExternalLink } from 'lucide-react';

export function CallHistory() {
  const { currentProfile } = useEnv();
  const [data, setData] = useState({ records: [], total: 0, has_more: false });
  const [loading, setLoading] = useState(false);
  const [filter, setFilter] = useState({ caller_id: '', service_name: '', limit: 50, offset: 0 });
  const [selectedRecord, setSelectedRecord] = useState(null);

  const fetchCDR = async () => {
    setLoading(true);
    try {
      const res = await api.getCDR(filter);
      setData(res);
    } catch (err) {
      alert(`조회 실패: ${err.message}`);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchCDR();
  }, [filter.offset]);

  const handleSearch = (e) => {
    e.preventDefault();
    setFilter({ ...filter, offset: 0 });
    fetchCDR();
  };

  const getJaegerLink = (sessionId) => {
    if (!currentProfile?.jaegerUrl) return '#';
    // Simplified Jaeger query URL format
    return `${currentProfile.jaegerUrl}/search?service=vbgw-orchestrator&tags={"session_id":"${sessionId}"}`;
  };

  return (
    <PageContainer title="통화 이력 & 트레이스">
      <div className="card" style={{ marginBottom: '16px' }}>
        <form onSubmit={handleSearch} style={{ display: 'flex', gap: '16px', alignItems: 'flex-end' }}>
          <div style={{ flex: 1 }}>
            <label className="form-label">발신번호 (Caller ID)</label>
            <input className="input" value={filter.caller_id} onChange={e => setFilter({...filter, caller_id: e.target.value})} placeholder="검색할 번호 일부 입력" />
          </div>
          <div style={{ flex: 1 }}>
            <label className="form-label">서비스명</label>
            <input className="input" value={filter.service_name} onChange={e => setFilter({...filter, service_name: e.target.value})} placeholder="예: voicebot-main" />
          </div>
          <button type="submit" className="btn btn-primary" disabled={loading}>
            <Search size={16} /> 검색
          </button>
        </form>
      </div>

      <div className="card" style={{ padding: 0 }}>
        <div className="table-container">
          <table className="data-table">
            <thead>
              <tr>
                <th>Start Time</th>
                <th>Session ID</th>
                <th>Caller ID</th>
                <th>Dest Num</th>
                <th>Service</th>
                <th>Duration</th>
                <th>Cause</th>
                <th>Trace</th>
              </tr>
            </thead>
            <tbody>
              {data.records?.length === 0 ? (
                <tr><td colSpan="8" style={{ textAlign: 'center', padding: '20px' }}>{loading ? '로딩 중...' : '검색 결과가 없습니다.'}</td></tr>
              ) : data.records?.map(r => (
                <tr key={r.session_id} onClick={() => setSelectedRecord(r)} style={{ cursor: 'pointer' }}>
                  <td>{new Date(r.start_time).toLocaleString()}</td>
                  <td className="mono truncate" style={{ maxWidth: '100px' }}>{r.session_id}</td>
                  <td className="mono">{r.caller_id}</td>
                  <td className="mono">{r.dest_num}</td>
                  <td>{r.service_name}</td>
                  <td>{r.duration_sec}s</td>
                  <td><span className={`badge ${r.hangup_code === 'NORMAL_CLEARING' ? 'badge-success' : 'badge-warning'}`}>{r.hangup_code}</span></td>
                  <td>
                    <a href={getJaegerLink(r.session_id)} target="_blank" rel="noreferrer" onClick={e => e.stopPropagation()} className="btn btn-ghost btn-sm">
                      <ExternalLink size={12} /> OTel
                    </a>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div style={{ padding: '16px', display: 'flex', justifyContent: 'space-between', borderTop: '1px solid var(--border-subtle)' }}>
          <span style={{ fontSize: '12px', color: 'var(--text-muted)' }}>총 {data.total || 0}건</span>
          <div style={{ display: 'flex', gap: '8px' }}>
            <button className="btn btn-ghost btn-sm" disabled={filter.offset === 0} onClick={() => setFilter({...filter, offset: Math.max(0, filter.offset - filter.limit)})}>이전</button>
            <button className="btn btn-ghost btn-sm" disabled={!data.has_more} onClick={() => setFilter({...filter, offset: filter.offset + filter.limit})}>다음</button>
          </div>
        </div>
      </div>

      {selectedRecord && (
        <div className="modal-overlay" onClick={() => setSelectedRecord(null)}>
          <div className="modal-content" style={{ maxWidth: '700px' }} onClick={e => e.stopPropagation()}>
            <h3 className="modal-title">세션 상세 분석</h3>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px', marginBottom: '24px' }}>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)' }}>Session ID</div>
                <div className="mono" style={{ fontSize: '14px' }}>{selectedRecord.session_id}</div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)' }}>FS UUID</div>
                <div className="mono" style={{ fontSize: '14px' }}>{selectedRecord.fs_uuid}</div>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)' }}>통화 연결 타임라인</div>
                <ul style={{ fontSize: '13px', paddingLeft: '16px', marginTop: '4px' }}>
                  <li>Start: {new Date(selectedRecord.start_time).toLocaleTimeString()}</li>
                  <li>Answer: {selectedRecord.answer_time ? new Date(selectedRecord.answer_time).toLocaleTimeString() : 'N/A'}</li>
                  <li>Hangup: {new Date(selectedRecord.end_time).toLocaleTimeString()}</li>
                </ul>
              </div>
              <div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)' }}>라우팅 정보</div>
                <ul style={{ fontSize: '13px', paddingLeft: '16px', marginTop: '4px' }}>
                  <li>Service: {selectedRecord.service_name}</li>
                  <li>Entry: {selectedRecord.entry_number}</li>
                  <li>Gateway: {selectedRecord.source_gateway}</li>
                  <li>Bridged: {selectedRecord.bridged_with || 'N/A'}</li>
                </ul>
              </div>
            </div>
            
            <div style={{ background: 'var(--bg-input)', padding: '16px', borderRadius: 'var(--radius-sm)', overflowX: 'auto' }}>
              <pre className="mono" style={{ fontSize: '12px', margin: 0 }}>
                {JSON.stringify(selectedRecord, null, 2)}
              </pre>
            </div>

            <div className="modal-actions">
              <a href={getJaegerLink(selectedRecord.session_id)} target="_blank" rel="noreferrer" className="btn btn-primary">
                <ExternalLink size={16} /> 전체 트레이스 열기
              </a>
              <button className="btn btn-ghost" onClick={() => setSelectedRecord(null)}>닫기</button>
            </div>
          </div>
        </div>
      )}
    </PageContainer>
  );
}
