export interface SkillResponse {
  raw_data: string | Record<string, unknown>;
  summary: string;
  expansion_payload?: ExpansionPayload;
}

export interface ExpansionPayload {
  reasoning: string;
  mapping_status: "mapped" | "unmapped";
  new_nodes: ExpansionNode[];
  downstream_wiring: DownstreamWiring;
}

export interface ExpansionNode {
  node_id: string;
  node_type: "skill" | "planner";
  skill_name?: string;
  goal?: string;
  mem_hint?: MemHint;
  parameters?: Record<string, unknown>;
  dependencies: string[];
}

export interface MemHint {
  version: "1.0";
  target_system?: "AUTO" | "MEM3_KV" | "PLATO_GRAPH";
  strategy: "KV_POINT_GET" | "GRAPH_LOCAL_TRAVERSAL" | "GRAPH_GLOBAL_SUMMARY" | "NONE";
  query?: {
    text?: string;
    keywords?: string[];
    target_task_id?: string;
  };
}

export interface DownstreamWiring {
  redirect_from: string;
  redirect_to: string[];
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
  sequence: number;
  node_type: "skill" | "planner";
  skill_name?: string;
  goal?: string;
  mem_hint: MemHint;
  status: "PENDING" | "READY" | "RUNNING" | "SUCCESS" | "FAILED";
  pending_dependencies_count: number;
  parameters?: Record<string, unknown>;
}
