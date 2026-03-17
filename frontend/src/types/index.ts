/** Core domain types for PayFlow frontend */

export interface User {
  user_id: string;
  email: string;
  full_name: string;
  role: string;
  token?: string;
}

export interface Account {
  account_id: string;
  user_id: string;
  name: string;
  type: 'checking' | 'savings' | 'wallet';
  currency: string;
  balance: number;
  status: string;
  created_at: string;
}

export interface Transaction {
  transaction_id: string;
  source_account_id: string;
  dest_account_id: string;
  amount: number;
  currency: string;
  status: 'pending' | 'processing' | 'completed' | 'failed' | 'reversed';
  description?: string;
  created_at: string;
}

export interface Payment {
  payment_id: string;
  account_id: string;
  provider: 'stripe' | 'wise' | 'bank_transfer';
  amount: number;
  currency: string;
  status: string;
  external_reference: string;
  payment_method: string;
  description?: string;
  created_at: string;
}

export interface LedgerEntry {
  entry_id: string;
  transaction_id: string;
  account_id: string;
  entry_type: 'debit' | 'credit';
  amount: string;
  currency: string;
  description?: string;
  created_at: string;
}

export interface KYCDocument {
  document_id: string;
  user_id: string;
  document_type: 'passport' | 'drivers_license' | 'utility_bill';
  status: 'pending' | 'approved' | 'rejected';
  file_reference: string;
  uploaded_at: string;
}

export interface AuditEntry {
  audit_id: string;
  event_type: string;
  service_name: string;
  user_id?: string;
  resource_id?: string;
  action: string;
  details: unknown;
  timestamp: string;
}

export interface FraudResult {
  transaction_id: string;
  risk_score: number;
  risk_level: 'low' | 'medium' | 'high' | 'critical';
  flags: string[];
  recommendation: 'allow' | 'review' | 'block';
}

export interface ServiceHealth {
  name: string;
  port: number;
  status: 'healthy' | 'unhealthy' | 'unknown';
  language: string;
}
