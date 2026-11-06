import json, subprocess, sys, time, os, glob, re
sys.path.insert(0, "/projects/carbon/scripts")
import importlib.util
spec = importlib.util.spec_from_file_location("at", "/projects/carbon/scripts/agent-tokens.py")
at = importlib.util.module_from_spec(spec)
spec.loader.exec_module(at)

TASKS = {
    "shop": {
        "prompt": "En http://127.0.0.1:8899 buscá el producto 'termo', agregalo al carrito, completá Nombre 'Ana Test', Email 'ana@test.com', elegí envío Express y confirmá el pedido. Terminá con cursor_complete y result {\"codigo\": <código del pedido>, \"total\": <total mostrado>}.",
        "check": lambda r: "CONF-96-EXP" in json.dumps(r, ensure_ascii=False) and "34.500" in json.dumps(r, ensure_ascii=False),
    },
    "lookup": {
        "prompt": "En https://olimpia.dev encontrá el precio mensual del plan Pro y qué incluye. Solo lectura. Terminá con cursor_complete y result {\"precio\": ..., \"incluye\": [...]}.",
        "check": lambda r: "20" in json.dumps(r, ensure_ascii=False),
    },
    "visual": {
        "prompt": "Abrí http://127.0.0.1:8899, mirala visualmente con una captura y detectá 3 problemas concretos de diseño visual. Terminá con cursor_complete y result {\"issues\": [3 strings cortos]}.",
        "check": lambda r: isinstance(r, dict) and len(r.get("issues", [])) >= 3,
    },
}


def api(method, path, body=None):
    args = ["carbon", "api", method, path] + ([json.dumps(body)] if body is not None else [])
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
    d = "/root/.claude/projects/-var-lib-carbon-agents-" + c["id"].replace("_", "-")
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
    os.makedirs("/projects/carbon/bench/results", exist_ok=True)
    json.dump(out, open(f"/projects/carbon/bench/results/agents-{label}.json", "w"), indent=1, ensure_ascii=False)
