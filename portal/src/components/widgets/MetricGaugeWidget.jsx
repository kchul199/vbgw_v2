import React from 'react';

export function MetricGaugeWidget({ title, value, max }) {
  const percentage = Math.min(100, Math.max(0, (value / max) * 100));
  
  // Color based on percentage
  let color = 'var(--accent-success)';
  if (percentage > 70) color = 'var(--accent-warning)';
  if (percentage > 90) color = 'var(--accent-danger)';

  return (
    <div style={{ padding: 'var(--space-md)', height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div className="card-title">{title}</div>
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center' }}>
        <div className="gauge-value">{value}</div>
        <div style={{ fontSize: 'var(--font-size-sm)', color: 'var(--text-muted)', marginTop: '8px' }}>
          Max: {max}
        </div>
        
        {/* Simple progress bar instead of arc for now */}
        <div style={{ width: '100%', height: '8px', background: 'var(--bg-input)', borderRadius: '4px', marginTop: '16px', overflow: 'hidden' }}>
          <div style={{ height: '100%', width: `${percentage}%`, background: color, transition: 'width 0.5s ease-out' }} />
        </div>
      </div>
    </div>
  );
}
