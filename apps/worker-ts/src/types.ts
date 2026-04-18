export interface SkillResponse {
  raw_data: string | Record<string, unknown>;
  summary: string;
}

export class AuroraSkillError extends Error {
  public code: "NETWORK_TIMEOUT" | "AUTH_FAILED" | "RATE_LIMIT" | "API_DEPRECATED" | "UNKNOWN";
  public human_readable_msg: string;
  public raw_stack: string;

  constructor(
    code: "NETWORK_TIMEOUT" | "AUTH_FAILED" | "RATE_LIMIT" | "API_DEPRECATED" | "UNKNOWN",
    human_readable_msg: string,
    raw_stack: string,
  ) {
    super(human_readable_msg);
    this.name = "AuroraSkillError";
    this.code = code;
    this.human_readable_msg = human_readable_msg;
    this.raw_stack = raw_stack;
  }
}

export type TelemetryEventType =
  | "NODE_START"
  | "NODE_PROGRESS"
  | "NODE_FINISH"
  | "TOKEN_STREAM";

export interface Task {
  task_id: string;
  dag_id: string;
  skill_name: string;
  status: "PENDING" | "READY" | "RUNNING" | "SUCCESS" | "FAILED";
  pending_dependencies_count: number;
}
