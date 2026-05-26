/**
 * TypeScript interfaces for PocketBase collections.
 *
 * These intentionally do NOT extend PocketBase's `RecordModel` because its
 * base `[key: string]: any` index signature defeats type safety. Cast
 * PocketBase responses to these types at the data boundary (hooks/queries).
 */

// ── Base ─────────────────────────────────────────────────────────────

export interface BaseRecord {
  id: string
  created: string
  updated: string
}

// ── Auth ─────────────────────────────────────────────────────────────

export interface User extends BaseRecord {
  email: string
  emailVisibility: boolean
  verified: boolean
  full_name: string
  is_superuser: boolean
  is_active: boolean
}

// ── Devices ──────────────────────────────────────────────────────────

export type DeviceType = "android" | "modem" | "smpp"

export interface SMPPConfig extends BaseRecord {
  device: string
  host: string
  port: number
  system_id: string
  /** Hidden — write-only */
  password?: string
  system_type: string
  bind_mode: "tx" | "rx" | "trx"
  source_ton: number
  source_npi: number
  dest_ton: number
  dest_npi: number
  use_tls: boolean
  enquire_link_seconds: number
  default_data_coding: number
  submit_throttle_tps: number
}

export interface Device extends BaseRecord {
  name: string
  phone_number: string
  device_type: DeviceType
  user: string
  /** Hidden field — only visible in certain contexts */
  api_key?: string
  /** Hidden field */
  fcm_token?: string
}

// ── SMS Messages ─────────────────────────────────────────────────────

export type MessageStatus =
  | "pending"
  | "assigned"
  | "sending"
  | "sent"
  | "delivered"
  | "failed"
  | "received"

export type MessageType = "outgoing" | "incoming"

export interface SMSMessage extends BaseRecord {
  to: string
  from_number: string
  body: string
  body_hash: string
  status: MessageStatus
  message_type: MessageType
  batch_id: string
  device: string
  user: string
  webhook_sent: boolean
  error_message: string
  sent_at: string
  delivered_at: string
  retry_count: number
  last_retry_at: string
}

// ── Webhook Configs ──────────────────────────────────────────────────

export interface WebhookConfig extends BaseRecord {
  url: string
  secret_key: string
  events: string[]
  active: boolean
  include_body: boolean
  user: string
}

// ── Webhook Delivery Logs ────────────────────────────────────────────

export type DeliveryStatus = "success" | "failed"

export interface WebhookDeliveryLog extends BaseRecord {
  webhook: string
  event: string
  url: string
  request_body: Record<string, unknown>
  response_status: number
  response_body: string
  delivery_status: DeliveryStatus
  error_message: string
  duration_ms: number
  retry_count: number
  next_retry_at: string
  original_log: string
}

// ── API Keys ─────────────────────────────────────────────────────────

export interface ApiKey extends BaseRecord {
  name: string
  /** Hidden — only shown once on creation */
  key?: string
  /** Server-computed prefix of the key, always visible */
  key_prefix: string
  is_active: boolean
  user: string
  last_used_at: string
  expires_at: string
}

// ── Templates ────────────────────────────────────────────────────────

export interface SMSTemplate extends BaseRecord {
  name: string
  body: string
  user: string
}

// ── Scheduled SMS ────────────────────────────────────────────────────

export type ScheduleType = "one_time" | "recurring"
export type ScheduleStatus = "active" | "paused" | "completed"

export interface ScheduledSMS extends BaseRecord {
  name: string
  recipients: string[]
  body: string
  device_id: string
  user: string
  schedule_type: ScheduleType
  scheduled_at: string
  cron_expression: string
  timezone: string
  next_run_at: string
  last_run_at: string
  status: ScheduleStatus
}

// ── Plans & Billing ──────────────────────────────────────────────────

export interface Plan extends BaseRecord {
  name: string
  max_sms_per_month: number
  max_devices: number
  price: number
  price_yearly: number
  is_public: boolean
}

export interface UserQuota extends BaseRecord {
  sms_sent_this_month: number
  devices_registered: number
  last_reset_date: string
  user: string
  plan: string
}

export type SubscriptionStatus =
  | "pending"
  | "active"
  | "past_due"
  | "canceled"
  | "expired"

export type BillingCycle = "monthly" | "yearly"
export type PaymentMethod = "invoice" | "authorized" | "balance"

export interface Subscription extends BaseRecord {
  user: string
  plan: string
  billing_cycle: BillingCycle
  status: SubscriptionStatus
  payment_method: PaymentMethod
  current_period_start: string
  current_period_end: string
  provider_user_uuid: string
  provider: string
  cancel_at_period_end: boolean
  canceled_at: string
}

export type PaymentStatus = "pending" | "completed" | "failed" | "refunded"

export interface Payment extends BaseRecord {
  subscription: string
  amount: number
  currency: string
  status: PaymentStatus
  provider: string
  provider_transaction_id: string
  provider_invoice_id: string
  provider_invoice_url: string
  period_start: string
  period_end: string
  paid_at: string
}

// ── User Balances ───────────────────────────────────────────────────

export interface UserBalance {
  balance: number
  currency: string
  wallet_address: string
  wallet_id: string
}

// ── Contacts ────────────────────────────────────────────────────────

export interface Contact extends BaseRecord {
  name: string
  phone_number: string
  groups: string[]
  notes: string
  user: string
}

export interface ContactGroup extends BaseRecord {
  name: string
  user: string
}

// ── System Config ────────────────────────────────────────────────────

export interface SystemConfig extends BaseRecord {
  key: string
  value: string
  description: string
}
