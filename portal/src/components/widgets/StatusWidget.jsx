import React from 'react';

export function StatusWidget({ title, health, metrics }) {
  // Safe extraction
  const eslConnected = metrics?.esl_connected === 1;
  const bridgeHealthy = metrics?.bridge_healthy === 1;
  const sipRegistered = metrics?.sip_registered === 1;
  const sipAlarm = metrics?.sip_registration_alarm === 1;

  const StatusItem = ({ label, isOk, errorMsg }) => (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '8px 0', borderBottom: '1px solid var(--border-subtle)' }}>
      <span style={{ fontSize: 'var(--font-size-sm)', color: 'var(--text-secondary)' }}>{label}</span>
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
        {!isOk && <span style={{ fontSize: '10px', color: 'var(--accent-danger)' }}>{errorMsg}</span>}
        <span className={`status-dot ${isOk ? 'connected' : 'disconnected'}`} />
      </div>
    </div>
  );

  return (
    <div style={{ padding: 'var(--space-md)', height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div className="card-title">{title}</div>
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center', marginTop: '12px' }}>
        <StatusItem label="FreeSWITCH ESL" isOk={eslConnected} errorMsg="Connection Lost" />
        <StatusItem label="Bridge Service" isOk={bridgeHealthy} errorMsg="Unhealthy" />
        <StatusItem label="SIP Registration" isOk={sipRegistered && !sipAlarm} errorMsg={sipAlarm ? "Alarm" : "Unregistered"} />
      </div>
    </div>
  );
}
