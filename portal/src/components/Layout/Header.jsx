import React from 'react';
import { useAuth } from '../../context/AuthContext';
import { useEnv } from '../../context/EnvContext';
import { LogOut } from 'lucide-react';

export function Header() {
  const { logout } = useAuth();
  const { activeEnv, currentProfile } = useEnv();

  return (
    <header className="header">
      <div className="header-left">
        {/* Breadcrumbs or other left items could go here */}
      </div>
      
      <div className="header-right">
        <div className={`header-env-badge ${activeEnv}`}>
          {currentProfile?.name || activeEnv}
        </div>
        
        <button className="btn btn-ghost btn-sm" onClick={logout} title="로그아웃">
          <LogOut size={16} /> 로그아웃
        </button>
      </div>
    </header>
  );
}
