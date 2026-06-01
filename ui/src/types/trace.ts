export type TraceEventRecord = {
  name: string;
  timestamp: string;
  attributes?: Record<string, unknown>;
};

export type TraceSpanRecord = {
  trace_id: string;
  span_id: string;
  parent_span_id?: string;
  name: string;
  kind?: string;
  start_time?: string;
  end_time?: string;
  attributes?: Record<string, unknown>;
  status_code?: string;
  status_message?: string;
  events?: TraceEventRecord[];
};

export type TraceResponse = {
  object: string;
  data: {
    request_id: string;
    trace_id?: string;
    started_at?: string;
    spans?: TraceSpanRecord[];
    route?: {
      final_provider?: string;
      final_provider_kind?: string;
      final_model?: string;
      final_reason?: string;
      fallback_from?: string;
      candidates?: Array<{
        provider?: string;
        provider_kind?: string;
        model?: string;
        reason?: string;
        outcome?: string;
        skip_reason?: string;
        health_status?: string;
        policy_rule_id?: string;
        policy_action?: string;
        policy_reason?: string;
        estimated_micros_usd?: number;
        estimated_usd?: string;
        attempt?: number;
        retry_count?: number;
        retryable?: boolean;
        index?: number;
        latency_ms?: number;
        failover_from?: string;
        failover_to?: string;
        detail?: string;
        timestamp?: string;
      }>;
      failovers?: Array<{
        from_provider?: string;
        from_model?: string;
        to_provider?: string;
        to_model?: string;
        reason?: string;
        timestamp?: string;
      }>;
    };
  };
};

export type TraceListItem = {
  request_id: string;
  trace_id?: string;
  started_at?: string;
  span_count: number;
  duration_ms?: number;
  status_code?: string;
  status_message?: string;
  route?: {
    final_provider?: string;
    final_provider_kind?: string;
    final_model?: string;
    final_reason?: string;
    fallback_from?: string;
    candidates?: NonNullable<TraceResponse["data"]["route"]>["candidates"];
  };
};

export type TraceListResponse = {
  object: string;
  data: TraceListItem[];
};
