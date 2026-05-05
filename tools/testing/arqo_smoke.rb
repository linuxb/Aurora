#!/usr/bin/env ruby
# frozen_string_literal: true

require "json"
require "net/http"
require "uri"
require "time"

BASE_URL = ENV.fetch("ARQO_URL", "http://127.0.0.1:8080")
USER_ID = ENV.fetch("SMOKE_USER_ID", "u_smoke")
INTENT = ENV.fetch("SMOKE_INTENT", "summarize logs and email report")
TIMEOUT_SECONDS = Integer(ENV.fetch("SMOKE_TIMEOUT_SECONDS", "30"))
POLL_INTERVAL_SECONDS = Float(ENV.fetch("SMOKE_POLL_INTERVAL_SECONDS", "0.5"))

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

def create_session
  code, body = request(:post, "/v1/sessions", {
    user_id: USER_ID,
    intent: INTENT
  })
  raise "create session failed: http=#{code} body=#{body.inspect}" unless code == 201

  body
end

def get_snapshot(session_id)
  code, body = request(:get, "/v1/sessions/#{session_id}")
  raise "get session failed: http=#{code} body=#{body.inspect}" unless code == 200

  body
end

def print_snapshot(snapshot)
  dag = snapshot.fetch("dag")
  tasks = snapshot.fetch("tasks")
  puts "dag_id=#{dag.fetch("dag_id")} status=#{dag.fetch("status")} replan_count=#{dag.fetch("replan_count")}"
  tasks.each do |task|
    puts "task=#{task.fetch("task_id")} skill=#{task.fetch("skill_name")} status=#{task.fetch("status")} pending=#{task.fetch("pending_dependencies_count")}"
  end
end

start_at = Time.now
created = create_session
session_id = created.fetch("session").fetch("session_id")
puts "[smoke] created session_id=#{session_id}"
print_snapshot(created)

loop do
  snapshot = get_snapshot(session_id)
  dag_status = snapshot.fetch("dag").fetch("status")

  if dag_status == "SUCCESS"
    puts "[smoke] success in #{(Time.now - start_at).round(2)}s"
    print_snapshot(snapshot)
    exit 0
  end

  if dag_status == "FAILED" || dag_status == "REPLANNING"
    warn "[smoke] unexpected dag status=#{dag_status}"
    print_snapshot(snapshot)
    exit 2
  end

  if Time.now - start_at > TIMEOUT_SECONDS
    warn "[smoke] timeout after #{TIMEOUT_SECONDS}s"
    print_snapshot(snapshot)
    exit 3
  end

  sleep POLL_INTERVAL_SECONDS
end
