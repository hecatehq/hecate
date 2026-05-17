export type RetentionRunResultRecord = {
  name: string;
  deleted: number;
  max_age?: string;
  max_count: number;
  error?: string;
  skipped?: boolean;
};

export type RetentionRunData = {
  started_at: string;
  finished_at: string;
  trigger: string;
  actor?: string;
  request_id?: string;
  results: RetentionRunResultRecord[];
};

export type RetentionRunResponse = {
  object: string;
  data: RetentionRunData;
};

export type RetentionRunsResponse = {
  object: string;
  data: RetentionRunData[];
};
