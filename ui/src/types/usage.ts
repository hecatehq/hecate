export type UsageEventRecord = {
  type: string;
  scope?: string;
  provider?: string;
  model?: string;
  request_id?: string;
  actor?: string;
  detail?: string;
  amount_micros_usd: number;
  amount_usd: string;
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
  timestamp?: string;
};

export type UsageSummaryRecord = {
  key: string;
  scope: string;
  provider?: string;
  backend: string;
  used_micros_usd: number;
  used_usd: string;
};

export type UsageSummaryResponse = {
  object: string;
  data: UsageSummaryRecord;
};

export type UsageEventsResponse = {
  object: string;
  data: UsageEventRecord[];
};
