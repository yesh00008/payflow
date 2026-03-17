import axios from 'axios';

const api = axios.create({
  baseURL: '/api',
  timeout: 10000,
  headers: { 'Content-Type': 'application/json' },
});

// Attach JWT token to requests
api.interceptors.request.use((config) => {
  const token = localStorage.getItem('payflow_token');
  if (token) config.headers.Authorization = `Bearer ${token}`;
  return config;
});

// Handle 401 - redirect to login
api.interceptors.response.use(
  (res) => res,
  (err) => {
    if (err.response?.status === 401) {
      localStorage.removeItem('payflow_token');
      window.location.href = '/login';
    }
    return Promise.reject(err);
  },
);

// ─── Auth ──────────────────────────────────────────────────────────
export const authAPI = {
  login: (email: string, password: string) =>
    api.post('/auth/login', { email, password }),
  register: (email: string, password: string, fullName: string) =>
    api.post('/auth/register', { email, password, full_name: fullName }),
  refresh: () => api.post('/auth/refresh'),
  logout: () => api.post('/auth/logout'),
};

// ─── Accounts ──────────────────────────────────────────────────────
export const accountsAPI = {
  create: (data: { name: string; type: string; currency: string }) =>
    api.post('/v1/accounts', data),
  get: (id: string) => api.get(`/v1/accounts/${id}`),
  list: () => api.get('/v1/accounts'),
};

// ─── Transfers ─────────────────────────────────────────────────────
export const transfersAPI = {
  create: (data: {
    source_account_id: string;
    dest_account_id: string;
    amount: number;
    currency: string;
    description?: string;
  }) => api.post('/v1/transfers', data),
  get: (id: string) => api.get(`/v1/transfers/${id}`),
  list: () => api.get('/v1/transfers'),
};

// ─── Payments ──────────────────────────────────────────────────────
export const paymentsAPI = {
  initiate: (data: {
    account_id: string;
    provider: string;
    amount: number;
    currency: string;
    payment_method: string;
    description?: string;
  }) => api.post('/v1/payments', data),
  get: (id: string) => api.get(`/v1/payments/${id}`),
  list: (accountId: string) =>
    api.get('/v1/payments', { params: { account_id: accountId } }),
};

// ─── Users / KYC ──────────────────────────────────────────────────
export const usersAPI = {
  createProfile: (data: {
    email: string;
    full_name: string;
    phone_number?: string;
    date_of_birth?: string;
    address?: string;
  }) => api.post('/v1/users', data),
  getProfile: (id: string) => api.get(`/v1/users/${id}`),
  submitKYC: (userId: string, data: { document_type: string; file_reference: string }) =>
    api.post(`/v1/users/${userId}/kyc/documents`, data),
  getKYCStatus: (userId: string) => api.get(`/v1/users/${userId}/kyc`),
};

// ─── Ledger ────────────────────────────────────────────────────────
export const ledgerAPI = {
  getBalance: (accountId: string) =>
    api.get(`/v1/ledger/accounts/${accountId}/balance`),
  getStatement: (accountId: string) =>
    api.get(`/v1/ledger/accounts/${accountId}/statement`),
};

// ─── Audit ─────────────────────────────────────────────────────────
export const auditAPI = {
  getLogs: (params?: Record<string, string>) =>
    api.get('/v1/audit', { params }),
  getCompliance: () => api.get('/v1/audit/compliance'),
};

// ─── Notifications ─────────────────────────────────────────────────
export const notificationsAPI = {
  send: (data: {
    user_id: string;
    channel: string;
    subject: string;
    body: string;
  }) => api.post('/v1/notifications', data),
  get: (id: string) => api.get(`/v1/notifications/${id}`),
};

// ─── Fraud Detection (Python) ──────────────────────────────────────
export const fraudAPI = {
  checkTransaction: (data: {
    transaction_id: string;
    amount: number;
    source_account: string;
    dest_account: string;
  }) => api.post('/v1/fraud/check', data),
  getReport: (txnId: string) => api.get(`/v1/fraud/report/${txnId}`),
};

// ─── Health ────────────────────────────────────────────────────────
export const healthAPI = {
  checkAll: () =>
    Promise.allSettled([
      api.get('http://localhost:8080/health'),
      api.get('http://localhost:8081/health'),
      api.get('http://localhost:8082/health'),
      api.get('http://localhost:8083/health'),
      api.get('http://localhost:8084/health'),
      api.get('http://localhost:8085/health'),
      api.get('http://localhost:8086/health'),
      api.get('http://localhost:8087/health'),
      api.get('http://localhost:8088/health'),
    ]),
};

export default api;
