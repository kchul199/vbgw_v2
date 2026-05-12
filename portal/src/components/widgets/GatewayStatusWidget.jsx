import React from 'react';

export function GatewayStatusWidget({ title, gateways }) {
  return (
    <div style={{ padding: 'var(--space-md)', height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div className="card-title">{title}</div>
      <div style={{ flex: 1, marginTop: '12px', overflowY: 'auto' }}>
        {(!gateways || gateways.length === 0) ? (
          <div style={{ color: 'var(--text-muted)', fontSize: '12px' }}>데이터 없음</div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
            {gateways.map(gw => {
              let badgeClass = 'badge-success';
              if (gw.health_class === 'degraded') badgeClass = 'badge-warning';
              if (gw.health_class === 'down') badgeClass = 'badge-danger';
              
              return (
                <div key={gw.id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '8px', background: 'var(--bg-input)', borderRadius: 'var(--radius-sm)' }}>
                  <div>
                    <div style={{ fontSize: '14px', fontWeight: 500 }}>{gw.id}</div>
                    <div style={{ fontSize: '12px', color: 'var(--text-muted)' }}>Ping: {gw.ping_latency_ms}ms | Score: {gw.composite_score}</div>
                  </div>
                  <div className={`badge ${badgeClass}`}>{gw.health_class}</div>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
