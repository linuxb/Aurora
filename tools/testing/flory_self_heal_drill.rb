#!/usr/bin/env ruby
# frozen_string_literal: true

require "json"
require "net/http"
require "uri"
require "time"

BASE_URL = ENV.fetch("FLORY_URL", "http://127.0.0.1:8080")
WAIT_SECONDS = Integer(ENV.fetch("SELF_HEAL_WAIT_SECONDS", "2"))

def request(method, path, payload = nil)
  uri = URI("#{BASE_URL}#{path}")
  http = Net::HTTP.new(uri.host, uri.port)
  req = case method
        when :get then Net::HTTP::Get.new(uri)
        when :post then Net::HTTP::Post.new(uri)
        else
          raise "unsupported method: #{method}"
        end
  req["content-type"] = "application/json"
  req.body = JSON.dump(payload) if payload

  res = http.request(req)
  body = res.body.to_s
  parsed = body.empty? ? nil : JSON.parse(body)
  [res.code.to_i, parsed]
end

def assert!(message, &block)
  return if block.call
  raise "assertion failed: #{message}"
end

def create_session
  code, body = request(:post, "/v1/sessions", {
    user_id: "u_self_heal",
    intent: "self heal crash sweep resume drill"
  })
  raise "create session failed: http=#{code} body=#{body.inspect}" unless code == 201
  body
end

def pull_task(worker_id)
  code, body = request(:post, "/v1/tasks/pull", { worker_id: worker_id })
  raise "pull failed: http=#{code} body=#{body.inspect}" unless code == 200
  body
end

def complete_task(task_id:, worker_id:, summary:)
  code, body = request(:post, "/v1/tasks/#{task_id}/complete", {
    worker_id: worker_id,
    success: true,
    summary: summary,
    raw_data: { done_at: Time.now.utc.iso8601 }
  })
  raise "complete task failed: http=#{code} body=#{body.inspect}" unless code == 200
  body
end

def get_snapshot(session_id)
  code, body = request(:get, "/v1/sessions/#{session_id}")
  raise "snapshot failed: http=#{code} body=#{body.inspect}" unless code == 200
  body
end

puts "[self-heal] start drill crash -> sweep -> replan patch -> resume"
created = create_session
session_id = created.fetch("session").fetch("session_id")
puts "[self-heal] session_id=#{session_id}"

# 1) Simulate worker crash by leasing task and not completing it.
leased = pull_task("crash-worker")
leased_id = leased.fetch("task_id")
puts "[self-heal] leased task=#{leased_id}, simulating crash (no complete)"
sleep WAIT_SECONDS

# 2) Manual sweep to move timed-out task into recovery path.
sweep_code, sweep_body = request(:post, "/v1/admin/sweep-expired", {})
assert!("sweep should return 200") { sweep_code == 200 }
assert!("sweep count should be >=1") { sweep_body.fetch("count").to_i >= 1 }
puts "[self-heal] sweep count=#{sweep_body.fetch("count")}"

snapshot = get_snapshot(session_id)
dag = snapshot.fetch("dag")
assert!("dag should enter REPLANNING after crash sweep") { dag.fetch("status") == "REPLANNING" }
puts "[self-heal] dag status after sweep=#{dag.fetch("status")}"

# 3) Apply replan patch.
patch_code, patch_body = request(:post, "/v1/sessions/#{session_id}/replan", {
  reason: "worker crashed on leased task",
  tasks: [
    {
      ref_id: "patch_root",
      node_type: "skill",
      skill_name: "QueryLog",
      dependencies: []
    },
    {
      ref_id: "patch_finish",
      node_type: "skill",
      skill_name: "SendEmail",
      dependencies: ["patch_root"]
    }
  ]
})
assert!("replan patch should return 200, got=#{patch_code}") { patch_code == 200 }
assert!("patched dag should return to RUNNING") { patch_body.fetch("dag").fetch("status") == "RUNNING" }
puts "[self-heal] patch applied, dag status=#{patch_body.fetch("dag").fetch("status")}"

# 4) Resume execution on patched tasks.
first = pull_task("resume-worker-1")
complete_task(task_id: first.fetch("task_id"), worker_id: "resume-worker-1", summary: "patched root done")
second = pull_task("resume-worker-2")
complete_task(task_id: second.fetch("task_id"), worker_id: "resume-worker-2", summary: "patched finish done")

final = get_snapshot(session_id)
final_dag = final.fetch("dag")
assert!("final dag should become SUCCESS") { final_dag.fetch("status") == "SUCCESS" }
puts "[self-heal] success: dag=#{final_dag.fetch("dag_id")} status=#{final_dag.fetch("status")}"
