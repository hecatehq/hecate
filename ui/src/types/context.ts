export type ContextPacketRefsRecord = {
  session_id?: string;
  turn_id?: string;
  message_id?: string;
  task_id?: string;
  run_id?: string;
  project_id?: string;
  work_item_id?: string;
  assignment_id?: string;
  role_id?: string;
};

export type ContextPacketSourceRecord = {
  kind: string;
  label: string;
  detail?: string;
  trust?: string;
};

export type ContextPacketItemRecord = {
  section?: string;
  kind: string;
  trust_level: string;
  origin: string;
  title: string;
  body?: string;
  body_ref?: string;
  included: boolean;
  inclusion_reason?: string;
  metadata?: Record<string, string>;
};

export type ContextPacketRecord = {
  id?: string;
  version?: string;
  execution_mode?: string;
  provider?: string;
  model?: string;
  execution_profile?: string;
  workspace?: string;
  system_prompt_included?: boolean;
  message_count?: number;
  refs?: ContextPacketRefsRecord;
  sources?: ContextPacketSourceRecord[];
  items?: ContextPacketItemRecord[];
};

export type ContextPacketResponse = {
  object: string;
  data: ContextPacketRecord;
};
