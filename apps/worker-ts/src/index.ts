import { runSkill } from "./skills.ts";
import { AuroraSkillError } from "./types.ts";
import type { Task, TelemetryEventType } from "./types.ts";

const gatewayURL = process.env.ARQO_URL ?? "http://127.0.0.1:8080";
const workerID = process.env.WORKER_ID ?? "worker-ts-1";
const loopIntervalMS = Number(process.env.WORKER_LOOP_INTERVAL_MS ?? "800");

async function emitTelemetry(
  eventType: TelemetryEventType,
  taskID: string,
  message: string,
): Promise<void> {
  const event = {
    event_type: eventType,
    task_id: taskID,
    message,
    source: workerID,
    at: new Date().toISOString(),
  };

  console.log(JSON.stringify(event));

  try {
    const res = await fetch(`${gatewayURL}/v1/telemetry`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(event),
      signal: AbortSignal.timeout(1500),
    });
    if (!res.ok) {
      console.warn(`[worker-ts] telemetry rejected: ${res.status}`);
    }
  } catch (error) {
    console.warn("[worker-ts] telemetry post failed", error);
  }
}

async function pullTask(): Promise<Task | null> {
  const res = await fetch(`${gatewayURL}/v1/tasks/pull`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ worker_id: workerID }),
  });

  if (res.status === 204) {
    return null;
  }
  if (!res.ok) {
    throw new Error(`pull failed: ${res.status} ${await res.text()}`);
  }
  return (await res.json()) as Task;
}

async function completeTask(taskID: string, payload: Record<string, unknown>): Promise<void> {
  const res = await fetch(`${gatewayURL}/v1/tasks/${taskID}/complete`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ worker_id: workerID, ...payload }),
  });

  if (!res.ok) {
    throw new Error(`complete failed: ${res.status} ${await res.text()}`);
  }
}

async function runOnce(): Promise<boolean> {
  const task = await pullTask();
  if (!task) {
    return false;
  }

  await emitTelemetry("NODE_START", task.task_id, `Start executing skill=${task.skill_name}`);

  try {
    const response = await runSkill(task.skill_name, task.task_id);
    await emitTelemetry("NODE_FINISH", task.task_id, response.summary);
    await completeTask(task.task_id, {
      success: true,
      summary: response.summary,
      raw_data: response.raw_data,
    });
    return true;
  } catch (error) {
    const skillError =
      error instanceof AuroraSkillError
        ? error
        : new AuroraSkillError("UNKNOWN", "worker unexpected error", String(error));

    await emitTelemetry("NODE_FINISH", task.task_id, `Failed: ${skillError.human_readable_msg}`);
    await completeTask(task.task_id, {
      success: false,
      summary: skillError.human_readable_msg,
      error_code: skillError.code,
      error_message: skillError.human_readable_msg,
      raw_data: { raw_stack: skillError.raw_stack },
    });
    return true;
  }
}

async function main(): Promise<void> {
  console.log(`[worker-ts] start worker_id=${workerID} arqo=${gatewayURL}`);

  while (true) {
    try {
      const executed = await runOnce();
      if (!executed) {
        await new Promise((resolve) => setTimeout(resolve, loopIntervalMS));
      }
    } catch (error) {
      console.error("[worker-ts] loop error", error);
      await new Promise((resolve) => setTimeout(resolve, loopIntervalMS));
    }
  }
}

void main();
