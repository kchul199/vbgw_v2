const DEFAULT_PROFILES = {
  dev: {
    name: '개발 환경',
    apiUrl: '',
    apiKey: '',
    grafanaUrl: 'http://localhost:3001',
    jaegerUrl: 'http://localhost:16686',
    description: '로컬 Docker Compose 개발 환경',
  },
  staging: {
    name: '검수 환경',
    apiUrl: 'https://staging-orch.vbgw.internal:8080',
    apiKey: '',
    grafanaUrl: 'https://staging-grafana.vbgw.internal',
    jaegerUrl: 'https://staging-jaeger.vbgw.internal',
    description: '사내 검수 환경',
  },
  prod: {
    name: '상용 환경',
    apiUrl: 'https://orch.vbgw.company.com:8080',
    apiKey: '',
    grafanaUrl: 'https://grafana.vbgw.company.com',
    jaegerUrl: 'https://jaeger.vbgw.company.com',
    description: '프로덕션 운영 환경',
  },
};

function loadProfiles() {
  try {
    const raw = localStorage.getItem('vbgw_profiles');
    if (raw) return JSON.parse(raw);
  } catch { /* ignore */ }
  return DEFAULT_PROFILES;
}

function saveProfiles(profiles) {
  localStorage.setItem('vbgw_profiles', JSON.stringify(profiles));
}

function getActiveEnv() {
  return localStorage.getItem('vbgw_active_env') || 'dev';
}

function setActiveEnv(env) {
  localStorage.setItem('vbgw_active_env', env);
}

/**
 * API client for VBGW Orchestrator.
 * Injects Bearer token, resolves base URL from active environment profile.
 */
class ApiClient {
  constructor() {
    this.profiles = loadProfiles();
    this.activeEnv = getActiveEnv();
  }

  get profile() {
    return this.profiles[this.activeEnv] || this.profiles.dev;
  }

  get baseUrl() {
    // If apiUrl is empty, use relative path (Vite proxy handles it)
    return this.profile.apiUrl || '';
  }

  get apiKey() {
    return this.profile.apiKey || localStorage.getItem('vbgw_api_key') || '';
  }

  setApiKey(key) {
    localStorage.setItem('vbgw_api_key', key);
  }

  switchEnv(env) {
    this.activeEnv = env;
    setActiveEnv(env);
  }

  getProfiles() {
    return this.profiles;
  }

  updateProfile(env, data) {
    this.profiles[env] = { ...this.profiles[env], ...data };
    saveProfiles(this.profiles);
  }

  async fetch(path, options = {}) {
    const url = `${this.baseUrl}${path}`;
    const headers = {
      'Content-Type': 'application/json',
      ...options.headers,
    };

    const key = this.apiKey;
    if (key) {
      headers['Authorization'] = `Bearer ${key}`;
    }

    const response = await fetch(url, { ...options, headers });

    if (response.status === 401) {
      throw new Error('인증 실패 — API Key를 확인하세요');
    }
    if (response.status === 403) {
      throw new Error('권한 없음 — 접근이 거부되었습니다');
    }
    if (!response.ok) {
      const body = await response.text();
      throw new Error(`HTTP ${response.status}: ${body}`);
    }

    return response.json();
  }

  // Admin API shortcuts
  getHealth() { return this.fetch('/health'); }
  getMetricsSummary() { return this.fetch('/api/v1/admin/metrics/summary'); }
  getActiveSessions() { return this.fetch('/api/v1/admin/sessions/active'); }
  getServices() { return this.fetch('/api/v1/admin/services'); }
  getService(name) { return this.fetch(`/api/v1/admin/services/${name}`); }
  getSlots() { return this.fetch('/api/v1/admin/slots'); }
  getQueues() { return this.fetch('/api/v1/admin/queues'); }
  getGateways() { return this.fetch('/api/v1/admin/gateways'); }
  getRoutingConfig() { return this.fetch('/api/v1/admin/routing/config'); }
  getClusterNodes() { return this.fetch('/api/v1/admin/cluster/nodes'); }
  getClusterLeases() { return this.fetch('/api/v1/admin/cluster/leases'); }
  getClusterCompatibility() { return this.fetch('/api/v1/admin/cluster/compatibility'); }
  getOperations() { return this.fetch('/api/v1/admin/operations'); }
  getOperation(id) { return this.fetch(`/api/v1/admin/operations/${id}`); }

  // CDR
  getCDR(params = {}) {
    const qs = new URLSearchParams();
    Object.entries(params).forEach(([k, v]) => { if (v) qs.set(k, v); });
    return this.fetch(`/api/v1/admin/cdr?${qs.toString()}`);
  }

  // Control API (requires ADMIN_CONTROL_KEY)
  postWithReason(path, reason, body = {}) {
    return this.fetch(path, {
      method: 'POST',
      body: JSON.stringify({ reason, ...body }),
    });
  }

  pauseService(name, reason) { return this.postWithReason(`/api/v1/admin/services/${name}/pause`, reason); }
  resumeService(name, reason) { return this.postWithReason(`/api/v1/admin/services/${name}/resume`, reason); }
  drainService(name, reason) { return this.postWithReason(`/api/v1/admin/services/${name}/drain`, reason); }
  flushQueue(name, reason) { return this.postWithReason(`/api/v1/admin/queues/${name}/flush`, reason); }
  forceReleaseSlot(slotID, reason) { return this.postWithReason(`/api/v1/admin/slots/${slotID}/force-release`, reason); }
  setGatewayStandby(name, reason, enabled) { return this.postWithReason(`/api/v1/admin/gateways/${name}/standby`, reason, { enabled }); }
  reloadRouting(reason) { return this.postWithReason('/api/v1/admin/routing/reload', reason); }
  drainNode(id, reason) { return this.postWithReason(`/api/v1/admin/cluster/nodes/${id}/drain`, reason); }
  resumeNode(id, reason) { return this.postWithReason(`/api/v1/admin/cluster/nodes/${id}/resume`, reason); }

  // Call control
  sendDtmf(id, digits) { return this.fetch(`/api/v1/calls/${id}/dtmf`, { method: 'POST', body: JSON.stringify({ digits }) }); }
  transfer(id, target) { return this.fetch(`/api/v1/calls/${id}/transfer`, { method: 'POST', body: JSON.stringify({ target }) }); }
  recordStart(id) { return this.fetch(`/api/v1/calls/${id}/record/start`, { method: 'POST' }); }
  recordStop(id) { return this.fetch(`/api/v1/calls/${id}/record/stop`, { method: 'POST' }); }
}

export const api = new ApiClient();
export default api;
