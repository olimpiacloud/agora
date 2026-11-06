#!/usr/bin/env bash
set -euo pipefail
WS=$1; T=$2; URL=${3:-}
[ -n "$URL" ] && carbon api POST /v1/workspaces/$WS/tabs/$T "$(python3 -c 'import json,sys;print(json.dumps({"action":"navigate","url":sys.argv[1]}))' "$URL")" >/dev/null && sleep 2
python3 - "$WS" "$T" <<'PY'
import json,subprocess,sys
ws,t=sys.argv[1],sys.argv[2]
js=open('/projects/carbon/scripts/ui-audit.js').read()
r=subprocess.run(['carbon','api','POST',f'/v1/workspaces/{ws}/tabs/{t}',json.dumps({"action":"eval","expression":js,"timeout_ms":30000})],capture_output=True,text=True)
if r.returncode: print(r.stderr); sys.exit(1)
print(json.dumps(json.loads(r.stdout)["value"],ensure_ascii=False,indent=1))
PY
