import React from 'react';

export function ServiceCapacityWidget({ title, services }) {
  return (
    <div style={{ padding: 'var(--space-md)', height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div className="card-title">{title}</div>
      <div style={{ flex: 1, marginTop: '12px', overflowY: 'auto' }}>
        {(!services || services.length === 0) ? (
          <div style={{ color: 'var(--text-muted)', fontSize: '12px' }}>데이터 없음</div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
            {services.map(svc => {
              const cap = svc.capacity;
              const active = cap.active_sessions;
              const max = cap.max_sessions;
              const pct = max > 0 ? (active / max) * 100 : 0;
              
              let barColor = 'var(--accent-info)';
              if (pct > 70) barColor = 'var(--accent-warning)';
              if (pct > 90) barColor = 'var(--accent-danger)';

              return (
                <div key={svc.name}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '4px', fontSize: '12px' }}>
                    <span style={{ fontWeight: 500 }}>{svc.name} {svc.status === 'paused' && '(Paused)'}</span>
                    <span style={{ color: 'var(--text-muted)' }}>{active} / {max}</span>
                  </div>
                  <div style={{ width: '100%', height: '6px', background: 'var(--bg-input)', borderRadius: '3px', overflow: 'hidden' }}>
                    <div style={{ height: '100%', width: `${pct}%`, background: barColor, transition: 'width 0.3s' }} />
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
