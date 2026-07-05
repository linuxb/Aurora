#!/usr/bin/env ruby
# frozen_string_literal: true

require "net/http"
require "uri"

ROOT = File.expand_path("../..", __dir__)
BASE_URL = ENV.fetch("FLORY_URL", "http://127.0.0.1:8080")

def assert!(message)
  return if yield

  raise "assertion failed: #{message}"
end

def healthz_ok?
  uri = URI("#{BASE_URL}/healthz")
  res = Net::HTTP.get_response(uri)
  res.is_a?(Net::HTTPSuccess)
rescue StandardError
  false
end

def run_step(name, env, cmd, chdir: ROOT)
  puts "[suite] >>> #{name}"
  ok = system(env, cmd, chdir: chdir)
  raise "[suite] step failed: #{name}" unless ok

  puts "[suite] <<< #{name} passed"
end

def start_worker
  puts "[suite] starting worker-ts for smoke phase"
  pid = Process.spawn(
    { "FLORY_URL" => BASE_URL },
    "npm run dev",
    chdir: File.join(ROOT, "apps/worker-ts"),
    out: $stdout,
    err: $stderr
  )
  sleep 1.0
  pid
end

def stop_worker(pid)
  return if pid.nil?

  puts "[suite] stopping worker-ts pid=#{pid}"
  begin
    Process.kill("TERM", pid)
  rescue StandardError
    nil
  end
  begin
    Process.wait(pid)
  rescue StandardError
    nil
  end
end

puts "[suite] flory API regression suite start"
assert!("flory healthz should be reachable at #{BASE_URL}/healthz") { healthz_ok? }

worker_pid = nil
begin
  worker_pid = start_worker
  run_step(
    "smoke",
    { "FLORY_URL" => BASE_URL },
    "ruby tools/testing/flory_smoke.rb"
  )
ensure
  stop_worker(worker_pid)
end

run_step(
  "fault-injector",
  { "FLORY_URL" => BASE_URL },
  "ruby tools/testing/flory_fault_injector.rb"
)

run_step(
  "missing-skill-regression",
  { "FLORY_URL" => BASE_URL },
  "ruby tools/testing/flory_missing_skill_regression.rb"
)

puts "[suite] all regression steps passed"
