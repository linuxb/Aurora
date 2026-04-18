import { AuroraSkillError } from "./types.ts";
import type { SkillResponse } from "./types.ts";

type SkillRunner = (taskID: string) => Promise<SkillResponse>;

const pause = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

const queryLog: SkillRunner = async (taskID) => {
  await pause(200);
  return {
    raw_data: {
      task_id: taskID,
      records: [
        "2026-04-18T10:21:00Z payment timeout",
        "2026-04-18T10:22:13Z lock wait timeout",
      ],
    },
    summary: "Log query completed. Found DB lock wait and timeout on payment flow.",
  };
};

const llmSummarize: SkillRunner = async (taskID) => {
  await pause(150);
  return {
    raw_data: {
      task_id: taskID,
      markdown: "- Root cause likely relates to deadlock retries under concurrent transactions.",
    },
    summary: "Summary completed: review hot SQL paths and retry policy.",
  };
};

const sendEmail: SkillRunner = async (taskID) => {
  await pause(120);
  return {
    raw_data: {
      task_id: taskID,
      message_id: `mail_${taskID}`,
      recipient: "backend-team-lead@example.com",
    },
    summary: "Report email has been sent to the backend team lead.",
  };
};

export const skills: Record<string, SkillRunner> = {
  QueryLog: queryLog,
  LLMSummarize: llmSummarize,
  SendEmail: sendEmail,
};

export async function runSkill(skillName: string, taskID: string): Promise<SkillResponse> {
  const skill = skills[skillName];
  if (!skill) {
    throw new AuroraSkillError(
      "UNKNOWN",
      `skill not found: ${skillName}`,
      `Missing skill registry for ${skillName}`,
    );
  }
  return skill(taskID);
}
