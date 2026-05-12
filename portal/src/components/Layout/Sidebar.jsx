import React from 'react';
import { NavLink } from 'react-router-dom';
import { 
  LayoutDashboard, 
  PhoneCall, 
  Server, 
  Network, 
  History, 
  Settings, 
  TerminalSquare
} from 'lucide-react';

export function Sidebar() {
  return (
    <aside className="sidebar">
      <div className="sidebar-logo">
        <div>
          <h1>VBGW Portal</h1>
          <div className="subtitle">VoiceBot Gateway v2</div>
        </div>
      </div>
      
      <div className="sidebar-nav">
        <div className="nav-section-title">모니터링</div>
        <NavLink to="/" className={({isActive}) => `nav-item ${isActive ? 'active' : ''}`}>
          <LayoutDashboard /> 대시보드
        </NavLink>
        
        <div className="nav-section-title">관제 & 제어</div>
        <NavLink to="/sessions" className={({isActive}) => `nav-item ${isActive ? 'active' : ''}`}>
          <PhoneCall /> 통화 관리
        </NavLink>
        <NavLink to="/services" className={({isActive}) => `nav-item ${isActive ? 'active' : ''}`}>
          <Server /> 서비스 관리
        </NavLink>
        <NavLink to="/infrastructure" className={({isActive}) => `nav-item ${isActive ? 'active' : ''}`}>
          <Network /> 인프라 설정
        </NavLink>
        
        <div className="nav-section-title">분석 & 환경</div>
        <NavLink to="/history" className={({isActive}) => `nav-item ${isActive ? 'active' : ''}`}>
          <History /> 이력 & 트레이스
        </NavLink>
        <NavLink to="/operations" className={({isActive}) => `nav-item ${isActive ? 'active' : ''}`}>
          <TerminalSquare /> 운영 로그
        </NavLink>
        <NavLink to="/environment" className={({isActive}) => `nav-item ${isActive ? 'active' : ''}`}>
          <Settings /> 환경 설정
        </NavLink>
      </div>
    </aside>
  );
}
