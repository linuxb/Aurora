#!/usr/bin/env ruby
# frozen_string_literal: true

require "json"
require "net/http"
require "uri"
require "time"

BASE_URL = ENV.fetch("ARQO_URL", "http://127.0.0.1:8080")
ITERATIONS = Integer(ENV.fetch("SELF_HEAL_PERSIST_ITERATIONS", "3"))
WAIT_SECONDS = Integer(ENV.fetch("SELF_HEAL_PERSIST_WAIT_SECONDS", "65"))
PASS_RATE_THRESHOLD = Float(ENV.fetch("SELF_HEAL_PERSIST_PASS_RATE_THRESHOLD", "1.0"))

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

def create_session(iteration)
  code, body = request(:post, "/v1/sessions", {
    user_id: "u_self_heal_persist_#{iteration}",
    intent: "persistent self-heal drill iteration=#{iteration}"
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

def apply_replan(session_id)
  code, body = request(:post, "/v1/sessions/#{session_id}/replan", {
    reason: "persistent drill crash recovery",
    tasks: [
      {
        ref_id: "patch_root",
        node_type: "SKILL_SINK",
        skill_name: "QueryLog",
        dependencies: []
      },
      {
        ref_id: "patch_finish",
        node_type: "SKILL_SINK",
        skill_name: "SendEmail",
        dependencies: ["patch_root"]
      }
    ]
  })
  raise "replan failed: http=#{code} body=#{body.inspect}" unless code == 200
  body
end

def percentile(values, ratio)
  return 0.0 if values.empty?
  sorted = values.sort
  index = [(sorted.length * ratio).ceil - 1, 0].max
  sorted[index]
end

def run_one(iteration)
  started_at = Time.now
  created = create_session(iteration)
  session_id = created.fetch("session").fetch("session_id")
  leased = pull_task("persist-crash-worker-#{iteration}")
  leased_id = leased.fetch("task_id")

  sleep WAIT_SECONDS

  sweep_code, sweep_body = request(:post, "/v1/admin/sweep-expired", {})
  raise "sweep failed: http=#{sweep_code} body=#{sweep_body.inspect}" unless sweep_code == 200
  manual_sweep_hit = sweep_body.fetch("expired_task_ids").include?(leased_id)

  snapshot = get_snapshot(session_id)
  raise "dag not replanning after lease expiry: dag=#{snapshot.fetch("dag").inspect}" unless snapshot.fetch("dag").fetch("status") == "REPLANNING"

  patched = apply_replan(session_id)
  raise "dag not running after patch: dag=#{patched.fetch("dag").inspect}" unless patched.fetch("dag").fetch("status") == "RUNNING"

  first = pull_task("persist-resume-worker-a-#{iteration}")
  complete_task(task_id: first.fetch("task_id"), worker_id: "persist-resume-worker-a-#{iteration}", summary: "patched root done")
  second = pull_task("persist-resume-worker-b-#{iteration}")
  complete_task(task_id: second.fetch("task_id"), worker_id: "persist-resume-worker-b-#{iteration}", summary: "patched finish done")

  final = get_snapshot(session_id)
  dag = final.fetch("dag")
  raise "final dag not success: dag=#{dag.inspect}" unless dag.fetch("status") == "SUCCESS"
  raise "replan_count should be >=1, got=#{dag.fetch("replan_count")}" unless dag.fetch("replan_count").to_i >= 1

  duration = Time.now - started_at
  {
    ok: true,
    session_id: session_id,
    duration_seconds: duration,
    replan_count: dag.fetch("replan_count").to_i,
    manual_sweep_hit: manual_sweep_hit
  }
end

puts "[self-heal-persistent] start iterations=#{ITERATIONS} wait_seconds=#{WAIT_SECONDS} threshold=#{PASS_RATE_THRESHOLD}"
results = []

1.upto(ITERATIONS) do |i|
  begin
    item = run_one(i)
    results << item
    puts "[self-heal-persistent] iteration=#{i} ok session_id=#{item[:session_id]} duration=#{item[:duration_seconds].round(2)}s replan_count=#{item[:replan_count]} manual_sweep_hit=#{item[:manual_sweep_hit]}"
  rescue StandardError => e
    results << { ok: false, error: e.message, duration_seconds: 0.0, replan_count: 0, manual_sweep_hit: false }
    warn "[self-heal-persistent] iteration=#{i} failed error=#{e.message}"
  end
end

ok_items = results.select { |r| r[:ok] }
durations = ok_items.map { |r| r[:duration_seconds] }
replan_total = ok_items.sum { |r| r[:replan_count] }
manual_sweep_hits = ok_items.count { |r| r[:manual_sweep_hit] }
pass_rate = ITERATIONS.zero? ? 0.0 : (ok_items.length.to_f / ITERATIONS.to_f)

summary = {
  iterations: ITERATIONS,
  passed: ok_items.length,
  failed: ITERATIONS - ok_items.length,
  pass_rate: pass_rate.round(4),
  wait_seconds: WAIT_SECONDS,
  replan_total: replan_total,
  manual_sweep_hit_count: manual_sweep_hits,
  avg_duration_seconds: (durations.empty? ? 0.0 : (durations.sum / durations.length)).round(3),
  p95_duration_seconds: percentile(durations, 0.95).round(3)
}

puts "[self-heal-persistent] summary=#{JSON.dump(summary)}"

exit 1 if pass_rate < PASS_RATE_THRESHOLD
