import json, subprocess, sys, time, os, glob, re
import importlib.util
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
spec = importlib.util.spec_from_file_location("at", os.path.join(ROOT, "scripts", "agent-tokens.py"))
at = importlib.util.module_from_spec(spec)
spec.loader.exec_module(at)

TASKS = {
    "shop": {
        "prompt": "At http://127.0.0.1:8899 search for the product 'thermos', add it to the cart, fill in Name 'Ana Test' and Email 'ana@test.com', choose Express shipping and place the order. Finish with cursor_complete and result {\"code\": <order code>, \"total\": <total shown>}.",
        "check": lambda r: "CONF-96-EXP" in json.dumps(r, ensure_ascii=False) and "34,500" in json.dumps(r, ensure_ascii=False),
    },
    "lookup": {
        "prompt": "At https://olimpia.dev find the monthly price of the Pro plan and what it includes. Read only. Finish with cursor_complete and result {\"price\": ..., \"includes\": [...]}.",
        "check": lambda r: "20" in json.dumps(r, ensure_ascii=False),
    },
    "visual": {
        "prompt": "Open http://127.0.0.1:8899, inspect it visually with a screenshot and find 3 concrete visual design problems. Finish with cursor_complete and result {\"issues\": [3 short strings]}.",
        "check": lambda r: isinstance(r, dict) and len(r.get("issues", [])) >= 3,
    },
}


def api(method, path, body=None):
    args = ["agora", "api", method, path] + ([json.dumps(body)] if body is not None else [])
    r = subprocess.run(args, capture_output=True, text=True)
    if r.returncode:
        raise RuntimeError(r.stderr)
    return json.loads(r.stdout) if r.stdout.strip() else None


def run(task, model, label, effort=""):
    ws = api("POST", "/v1/workspaces", {"name": f"bench-{label}-{task}", "start": True})["id"]
    root = api("POST", "/v1/cursors", {"name": "bench-root", "workspace": ws})["cursor"]["id"]
    t0 = time.time()
    c = api("POST", "/v1/cursors", {"name": task, "parent": root, "workspace": ws, "goal": task, "launch": {"adapter": "claude", "model": model, "prompt": TASKS[task]["prompt"], **({"effort": effort} if effort else {})}})["cursor"]
    w = api("POST", "/v1/wait", {"ids": [c["id"]], "timeout_ms": 600000})
    dt = time.time() - t0
    cur = w["cursors"][0]
    result = cur.get("result")
    d = "/root/.claude/projects/-var-lib-agora-agents-" + c["id"].replace("_", "-")
    files = sorted(glob.glob(d + "/*.jsonl"))
    tok = at.analyze(files[-1]) if files else {}
    cost = at.cost(tok) if files else 0
    ok = cur["status"] == "done" and TASKS[task]["check"](result)
    api("DELETE", f"/v1/workspaces/{ws}?purge=1")
    return {"task": task, "label": label, "model": model, "ok": ok, "status": cur["status"], "seconds": round(dt, 1), "cost": round(cost, 4), **{k: tok.get(k) for k in ("turns", "first_ctx", "input", "cache_write", "cache_read", "output", "images", "result_chars", "tools")}, "result": result}


if __name__ == "__main__":
    label = sys.argv[1]
    model = sys.argv[2] if len(sys.argv) > 2 else "claude-sonnet-5-5"
    tasks = sys.argv[3].split(",") if len(sys.argv) > 3 else list(TASKS)
    reps = int(sys.argv[4]) if len(sys.argv) > 4 else 1
    effort = sys.argv[5] if len(sys.argv) > 5 else ""
    out = []
    for t in tasks:
        for i in range(reps):
            r = run(t, model, label, effort)
            out.append(r)
            print(json.dumps({k: r[k] for k in r if k != "result"}, ensure_ascii=False), flush=True)
    os.makedirs(os.path.join(ROOT, "bench", "results"), exist_ok=True)
    json.dump(out, open(os.path.join(ROOT, "bench", "results", f"agents-{label}.json"), "w"), indent=1, ensure_ascii=False)
