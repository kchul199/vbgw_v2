import React, { createContext, useContext, useState, useEffect } from 'react';
import api from '../api/client';

const EnvContext = createContext(null);

export function EnvProvider({ children }) {
  const [activeEnv, setActiveEnv] = useState(api.activeEnv);
  const [profiles, setProfiles] = useState(api.getProfiles());

  const switchEnv = (env) => {
    api.switchEnv(env);
    setActiveEnv(env);
    // Reload page to reset all states and re-fetch for new env
    window.location.reload();
  };

  const updateProfile = (env, data) => {
    api.updateProfile(env, data);
    setProfiles({ ...api.getProfiles() });
  };

  return (
    <EnvContext.Provider value={{ 
      activeEnv, 
      profiles, 
      currentProfile: profiles[activeEnv],
      switchEnv,
      updateProfile
    }}>
      {children}
    </EnvContext.Provider>
  );
}

export function useEnv() {
  return useContext(EnvContext);
}
