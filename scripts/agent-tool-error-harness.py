#!/usr/bin/env python3
"""Opt-in smoke harness for Flow agent tool calls.

The API must be started separately with a disposable SQLite database and the
agent proxy settings from the task description.  This keeps the harness from
ever touching a developer database.  It sends sequential streaming chat
requests and writes one JSON record per observed tool call, including errors
and latency, so runs can be compared without retaining the full model output.

Example:
  FLOW_HARNESS_URL=http://127.0.0.1:8080 \
    scripts/agent-tool-error-harness.py --output /tmp/codex-agent-tool-errors/run.jsonl
"""

import argparse
import json
import os
import sys
import time
import urllib.request


PROMPTS = [
    "List active issues for the ENG team and find the high priority ones.",
    "Create an issue in ENG titled Harness child and make it a sub-issue of ENG-1.",
    "Move ENG-2 to the private team, assign it to me, set high priority, estimate 3, and add the bug label.",
    "Put ENG-3 in the current cycle, relate it to ENG-4, and comment in English that this is a harness check.",
    "Create a project called Harness roadmap with the ENG team and add a milestone named Beta.",
    "Create an initiative called Harness delivery and post a project status update that it is on track.",
    "Create a document called Harness notes, append a second paragraph, then patch the first paragraph.",
    "Show the members of ENG, including the Chinese-named member, and search for documents about release plans.",
    "Create a saved view for high priority issues due tomorrow in ENG.",
    "List cycles and teams, then tell me the next cycle for ENG.",
    "Find issues with labels, attachments, customer requests, and sub-issues; do not modify anything.",
    "Create a loop template for a weekly issue digest, but do not run it.",
    "Update ENG-5 to completed, set its due date to next week, and remove its current assignee.",
    "Use Chinese: 将 ENG-6 设置为高优先级并分配给我，截止日期设为明天。",
    "Summarize what you changed and include links; do not claim a failed tool call succeeded.",
]


def request(url, body, headers=None):
    raw = json.dumps(body, ensure_ascii=False).encode()
    request_headers = {"Content-Type": "application/json"}
    request_headers.update(headers or {})
    req = urllib.request.Request(url, raw, request_headers, method="POST")
    return urllib.request.urlopen(req, timeout=180)


def seed_issues(base_url, workspace, count):
    req = urllib.request.Request(base_url.rstrip("/") + "/api/bootstrap?workspace=" + workspace, method="GET")
    with urllib.request.urlopen(req, timeout=30) as response:
        bootstrap = json.load(response)
    teams = bootstrap.get("teams") or []
    if not teams:
        raise RuntimeError("bootstrap has no teams; seed a disposable workspace first")
    for index in range(count):
        team = teams[index % len(teams)]
        request(base_url.rstrip("/") + "/api/issues?workspace=" + workspace, {
            "title": ("Harness issue " + str(index + 1)) if index % 3 else "中文验证 issue " + str(index + 1),
            "teamId": team.get("id"),
            "description": "Seeded by the opt-in agent tool harness.",
        }, {"X-Workspace-Key": workspace}).close()


def parse_stream(response, prompt, index):
    started = {}
    records = []
    for line in response:
        if not line.startswith(b"data:"):
            continue
        try:
            event = json.loads(line[5:].strip())
        except json.JSONDecodeError:
            continue
        part = event.get("part") or {}
        call = part.get("toolCall") or event.get("toolCall") or {}
        name = call.get("name") or part.get("name")
        if not name:
            continue
        call_id = call.get("id") or part.get("id") or name
        now = time.monotonic()
        if event.get("type") in ("tool.started", "tool.call"):
            started[call_id] = now
            continue
        if event.get("type") not in ("tool.completed", "tool.result") and not call.get("result") and not call.get("error"):
            continue
        args = call.get("arguments", call.get("args", {}))
        result = call.get("result", part.get("result"))
        error = call.get("error", part.get("error"))
        records.append({
            "prompt": prompt,
            "promptIndex": index,
            "callId": call_id,
            "name": name,
            "arguments": args,
            "result": result,
            "isError": bool(call.get("isError") or error or part.get("status") in ("error", "failed")),
            "error": error,
            "latencyMs": round((now - started.get(call_id, now)) * 1000, 1),
        })
    return records


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default=os.environ.get("FLOW_HARNESS_URL", "http://127.0.0.1:8080"))
    parser.add_argument("--workspace", default=os.environ.get("FLOW_HARNESS_WORKSPACE", "test-workspace"))
    parser.add_argument("--output", default="/tmp/codex-agent-tool-errors/tool-calls.jsonl")
    parser.add_argument("--limit", type=int, default=15)
    parser.add_argument("--seed-issues", type=int, default=0, help="seed up to 60 issues through the disposable API")
    args = parser.parse_args()
    os.makedirs(os.path.dirname(args.output) or ".", exist_ok=True)
    if args.seed_issues:
        seed_issues(args.url, args.workspace, max(0, min(args.seed_issues, 60)))
    all_records = []
    endpoint = args.url.rstrip("/") + "/api/agent/sessions/stream?workspace=" + args.workspace
    for index, prompt in enumerate(PROMPTS[: max(1, min(args.limit, len(PROMPTS)))]):
        try:
            with request(endpoint, {"message": prompt, "location": "harness"}) as response:
                all_records.extend(parse_stream(response, prompt, index))
        except Exception as exc:  # keep the run useful when one stream/transport fails
            all_records.append({"prompt": prompt, "promptIndex": index, "name": "<transport>", "isError": True, "error": str(exc)})
    with open(args.output, "w", encoding="utf-8") as output:
        for record in all_records:
            output.write(json.dumps(record, ensure_ascii=False) + "\n")
    errors = sum(1 for record in all_records if record.get("isError"))
    print(json.dumps({"requests": min(args.limit, len(PROMPTS)), "toolCalls": len(all_records), "errors": errors, "output": args.output}, ensure_ascii=False))


if __name__ == "__main__":
    main()
