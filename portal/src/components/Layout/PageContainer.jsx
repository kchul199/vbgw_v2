import React from 'react';
import { Sidebar } from './Sidebar';
import { Header } from './Header';

export function PageContainer({ children, title, action }) {
  return (
    <div className="app-layout">
      <Sidebar />
      <div className="main-content">
        <Header />
        <main className="page-container">
          <div className="page-header">
            <h2 className="page-title">{title}</h2>
            {action && <div className="page-actions">{action}</div>}
          </div>
          {children}
        </main>
      </div>
    </div>
  );
}
