#!/usr/bin/env ruby
# frozen_string_literal: true

require "json"
require "net/http"
require "uri"

BASE_URL = ENV.fetch("ARQO_URL", "http://127.0.0.1:8080")

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

def create_session(user_id:, intent:)
  code, body = request(:post, "/v1/sessions", { user_id: user_id, intent: intent })
  raise "create session failed: http=#{code} body=#{body.inspect}" unless code == 201

  body
end

def pull_task(worker_id)
  code, body = request(:post, "/v1/tasks/pull", { worker_id: worker_id })
  raise "pull failed: http=#{code} body=#{body.inspect}" unless code == 200

  body
end

def complete_task(task_id:, worker_id:, success:, summary:, error_code: "", error_message: "")
  request(:post, "/v1/tasks/#{task_id}/complete", {
    worker_id: worker_id,
    success: success,
    summary: summary,
    error_code: error_code,
    error_message: error_message
  })
end

def get_snapshot(session_id)
  code, body = request(:get, "/v1/sessions/#{session_id}")
  raise "get session failed: http=#{code} body=#{body.inspect}" unless code == 200

  body
end

def assert!(message, &block)
  return if block.call

  raise "assertion failed: #{message}"
end

puts "[fault] scenario_1 forced task failure => dag replanning"
created = create_session(user_id: "u_fault", intent: "fault injection")
session_id = created.fetch("session").fetch("session_id")
task = pull_task("fault-worker-1")
task_id = task.fetch("task_id")

code, _body = complete_task(
  task_id: task_id,
  worker_id: "fault-worker-1",
  success: false,
  summary: "injected failure",
  error_code: "INJECTED_ERROR",
  error_message: "synthetic injected failure"
)
assert!("forced failure completion should return 200") { code == 200 }

snapshot = get_snapshot(session_id)
dag = snapshot.fetch("dag")
assert!("dag should be replanning after failed task") { dag.fetch("status") == "REPLANNING" }
assert!("replan_count should be >= 1") { dag.fetch("replan_count").to_i >= 1 }
puts "[fault] scenario_1 passed session_id=#{session_id}"

puts "[fault] scenario_2 owner conflict should return 409"
created2 = create_session(user_id: "u_fault_conflict", intent: "owner conflict")
task2 = pull_task("owner-a")
task2_id = task2.fetch("task_id")

code2, body2 = complete_task(
  task_id: task2_id,
  worker_id: "owner-b",
  success: true,
  summary: "should conflict"
)
assert!("owner conflict should return 409, got #{code2}") { code2 == 409 }
assert!("error code should be task_completion_failed") { body2.is_a?(Hash) && body2["code"] == "task_completion_failed" }
puts "[fault] scenario_2 passed task_id=#{task2_id}"

puts "[fault] all scenarios passed"
