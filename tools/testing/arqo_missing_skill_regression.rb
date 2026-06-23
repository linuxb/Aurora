#!/usr/bin/env ruby
# frozen_string_literal: true

require "json"
require "net/http"
require "securerandom"
require "uri"

BASE_URL = ENV.fetch("ARQO_URL", "http://127.0.0.1:8080")
USER_ID = ENV.fetch("REGRESSION_USER_ID", "u_missing_skill_#{SecureRandom.hex(3)}")
WORKER_ID = ENV.fetch("REGRESSION_WORKER_ID", "worker-missing-skill")
INTENT = ENV.fetch("REGRESSION_INTENT", "unknown_skill: please do a capability we do not have yet")

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

def assert!(message)
  return if yield

  raise "assertion failed: #{message}"
end

def create_jit_session
  code, body = request(:post, "/v1/sessions", {
    user_id: USER_ID,
    intent: INTENT,
    planning_mode: "jit"
  })
  raise "create session failed: http=#{code} body=#{body.inspect}" unless code == 201

  body
end

def pull_task
  code, body = request(:post, "/v1/tasks/pull", { worker_id: WORKER_ID })
  raise "pull failed: http=#{code} body=#{body.inspect}" unless code == 200

  body
end

def complete_success(task_id, summary:, expansion_payload: nil)
  status = expansion_payload.nil? ? "SUCCESS" : "SUCCESS_AND_EXPAND"
  request(:post, "/v1/tasks/#{task_id}/complete", {
    worker_id: WORKER_ID,
    status: status,
    success: true,
    summary: summary,
    raw_data: { task_id: task_id, source: "regression" },
    expansion_payload: expansion_payload
  })
end

def default_unmapped_expansion(task_id, node_id)
  {
    reasoning: "cannot map to concrete skills yet",
    mapping_status: "unmapped",
    new_nodes: [
      {
        node_id: node_id,
        node_type: "planner",
        skill_name: "ReActPlanner",
        goal: "continue planning for an unmapped capability",
        mem_hint: {
          version: "1.0",
          strategy: "NONE"
        },
        dependencies: [task_id]
      }
    ],
    downstream_wiring: {
      redirect_from: task_id,
      redirect_to: [node_id]
    }
  }
end

puts "[regression] scenario_1 missing_skill from consecutive unmapped expansion"
created = create_jit_session
session_id = created.fetch("session").fetch("session_id")
planner = pull_task
assert!("first leased task should be ReActPlanner") { planner["skill_name"] == "ReActPlanner" }

code1, body1 = complete_success(
  planner.fetch("task_id"),
  summary: "first unmapped expansion",
  expansion_payload: default_unmapped_expansion(planner.fetch("task_id"), "#{planner.fetch("task_id")}_dyn_next")
)
assert!("first unmapped expansion should succeed, got #{code1} #{body1.inspect}") { code1 == 200 }

max_attempts = Integer(ENV.fetch("MISSING_SKILL_MAX_ATTEMPTS", "6"))
got_missing_skill = false
last_code = nil
last_body = nil

attempt = 1
while attempt <= max_attempts
  next_planner = pull_task
  assert!("next planner should be planner") { next_planner["node_type"] == "planner" }

  code_n, body_n = complete_success(
    next_planner.fetch("task_id"),
    summary: "unmapped expansion attempt=#{attempt + 1}",
    expansion_payload: default_unmapped_expansion(next_planner.fetch("task_id"), "#{next_planner.fetch("task_id")}_dyn_next")
  )
  last_code = code_n
  last_body = body_n

  if code_n == 422
    got_missing_skill = true
    break
  end
  assert!("intermediate unmapped attempt should still succeed, got #{code_n} body=#{body_n.inspect}") { code_n == 200 }
  attempt += 1
end

assert!("missing_skill should be returned within #{max_attempts} attempts, got code=#{last_code} body=#{last_body.inspect}") do
  got_missing_skill
end
assert!("error code should be missing_skill, got #{last_body.inspect}") { last_body.is_a?(Hash) && last_body["code"] == "missing_skill" }

code3, snap = request(:get, "/v1/sessions/#{session_id}")
assert!("session snapshot should be readable") { code3 == 200 && snap.is_a?(Hash) }
dag = snap.fetch("dag")
assert!("dag should leave RUNNING after missing skill, got=#{dag.fetch("status")}") do
  %w[REPLANNING FAILED].include?(dag.fetch("status"))
end
assert!("jit_unmapped_streak should be >= 1") { dag.fetch("jit_unmapped_streak").to_i >= 1 }

puts "[regression] scenario_1 passed session_id=#{session_id}"
puts "[regression] all scenarios passed"
