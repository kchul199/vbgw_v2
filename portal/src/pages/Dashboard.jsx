import React, { useState, useEffect } from 'react';
import GridLayout from 'react-grid-layout';
import 'react-grid-layout/css/styles.css';
import 'react-resizable/css/styles.css';
import { PageContainer } from '../components/Layout/PageContainer';
import { usePolling } from '../hooks/usePolling';
import api from '../api/client';
import { useEnv } from '../context/EnvContext';

// Widgets
import { MetricGaugeWidget } from '../components/widgets/MetricGaugeWidget';
import { StatusWidget } from '../components/widgets/StatusWidget';
import { GrafanaIframeWidget } from '../components/widgets/GrafanaIframeWidget';
import { GatewayStatusWidget } from '../components/widgets/GatewayStatusWidget';
import { ServiceCapacityWidget } from '../components/widgets/ServiceCapacityWidget';

const DEFAULT_LAYOUT = [
  { i: 'active_calls', x: 0, y: 0, w: 3, h: 2 },
  { i: 'components', x: 3, y: 0, w: 3, h: 2 },
  { i: 'gateways', x: 6, y: 0, w: 6, h: 2 },
  { i: 'service_capacity', x: 0, y: 2, w: 6, h: 3 },
  { i: 'grafana_pdd', x: 6, y: 2, w: 6, h: 3 }
];

export function Dashboard() {
  const { currentProfile } = useEnv();
  const [layout, setLayout] = useState(() => {
    const saved = localStorage.getItem('vbgw_dashboard_layout');
    return saved ? JSON.parse(saved) : DEFAULT_LAYOUT;
  });
  
  const [isEditing, setIsEditing] = useState(false);

  const { data: metrics } = usePolling(() => api.getMetricsSummary(), 5000);
  const { data: health } = usePolling(() => api.getHealth(), 5000);
  const { data: services } = usePolling(() => api.getServices(), 10000);
  const { data: gateways } = usePolling(() => api.getGateways(), 10000);

  const onLayoutChange = (newLayout) => {
    setLayout(newLayout);
    localStorage.setItem('vbgw_dashboard_layout', JSON.stringify(newLayout));
  };

  const renderWidget = (item) => {
    switch (item.i) {
      case 'active_calls':
        return (
          <MetricGaugeWidget 
            title="활성 통화 수" 
            value={metrics?.data?.active_calls || 0} 
            max={200} // This should ideally be dynamic
          />
        );
      case 'components':
        return <StatusWidget title="시스템 상태" health={health} metrics={metrics?.data} />;
      case 'gateways':
        return <GatewayStatusWidget title="게이트웨이 현황" gateways={gateways?.gateways} />;
      case 'service_capacity':
        return <ServiceCapacityWidget title="서비스별 용량" services={services?.services} />;
      case 'grafana_pdd':
        // Assuming panelId=15 is PDD in grafana
        return <GrafanaIframeWidget title="Call Setup Time (PDD)" panelId={15} />;
      default:
        return <div>Unknown Widget</div>;
    }
  };

  return (
    <PageContainer 
      title="통합 관제 대시보드" 
      action={
        <button className="btn btn-ghost" onClick={() => setIsEditing(!isEditing)}>
          {isEditing ? '편집 완료' : '레이아웃 편집'}
        </button>
      }
    >
      <div style={{ position: 'relative', minHeight: '800px', margin: '-10px' }}>
        <GridLayout
          className="layout"
          layout={layout}
          cols={12}
          rowHeight={100}
          width={1200}
          onLayoutChange={onLayoutChange}
          isDraggable={isEditing}
          isResizable={isEditing}
          margin={[16, 16]}
        >
          {layout.map((item) => (
            <div key={item.i} className="card" style={{ padding: 0, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
              {isEditing && (
                <div style={{ position: 'absolute', top: 0, right: 0, padding: '4px 8px', background: 'var(--bg-card-hover)', fontSize: '10px', borderBottomLeftRadius: '4px', zIndex: 10 }}>
                  Drag to move
                </div>
              )}
              {renderWidget(item)}
            </div>
          ))}
        </GridLayout>
      </div>
    </PageContainer>
  );
}
