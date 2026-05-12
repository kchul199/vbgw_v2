import React from 'react';
import { useEnv } from '../../context/EnvContext';

export function GrafanaIframeWidget({ title, panelId }) {
  const { currentProfile } = useEnv();
  const grafanaUrl = currentProfile?.grafanaUrl || 'http://localhost:3001';
  
  // Construct URL for embedding single panel
  // e.g. http://localhost:3001/d/vbgw-main-dashboard/vbgw-voicebot-gateway?orgId=1&viewPanel=15&kiosk
  // We assume the dashboard UID is vbgw-main-dashboard based on JSON
  const embedUrl = `${grafanaUrl}/d-solo/vbgw-main-dashboard?orgId=1&panelId=${panelId}&theme=dark`;

  return (
    <div style={{ padding: 'var(--space-md)', height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div className="card-title" style={{ marginBottom: '8px' }}>{title}</div>
      <div style={{ flex: 1, borderRadius: 'var(--radius-sm)', overflow: 'hidden', background: 'var(--bg-secondary)' }}>
        <iframe 
          src={embedUrl} 
          width="100%" 
          height="100%" 
          frameBorder="0" 
          title={title}
          style={{ display: 'block' }}
        />
      </div>
    </div>
  );
}
