import { runSkill } from "./skills.ts";

async function main(): Promise<void> {
  const r1 = await runSkill("QueryLog", "task_demo_1");
  const r2 = await runSkill("LLMSummarize", "task_demo_2");
  const r3 = await runSkill("SendEmail", "task_demo_3");

  console.log("worker-ts self-check ok", {
    summaries: [r1.summary, r2.summary, r3.summary],
  });
}

void main();
