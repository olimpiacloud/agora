import json,sys,glob,os,collections
def analyze(path):
    turns=0; inp=out=cw=cr=0; tools=collections.Counter(); res_chars=collections.Counter(); imgs=0; first=None; model=None
    sys_tools=0
    seen_ids=set()
    for line in open(path):
        try: e=json.loads(line)
        except: continue
        m=e.get("message") or {}
        if e.get("type")=="assistant" and isinstance(m,dict) and m.get("usage"):
            mid=m.get("id") or e.get("requestId") or id(e)
            if mid not in seen_ids:
                seen_ids.add(mid)
                u=m["usage"]; turns+=1; model=m.get("model",model)
                inp+=u.get("input_tokens",0); out+=u.get("output_tokens",0); cw+=u.get("cache_creation_input_tokens",0); cr+=u.get("cache_read_input_tokens",0)
                if first is None: first=u
            for c in m.get("content",[]):
                if c.get("type")=="tool_use": tools[c["name"].replace("mcp__agora__","")]+=1
        if e.get("type")=="user" and isinstance(m,dict):
            for c in m.get("content",[]) if isinstance(m.get("content"),list) else []:
                if c.get("type")=="tool_result":
                    cc=c.get("content")
                    if isinstance(cc,list):
                        for x in cc:
                            if x.get("type")=="image": imgs+=1
                            elif x.get("type")=="text": res_chars["text"]+=len(x.get("text",""))
                    elif isinstance(cc,str): res_chars["text"]+=len(cc)
    ctx0=(first or {}).get("input_tokens",0)+(first or {}).get("cache_creation_input_tokens",0)+(first or {}).get("cache_read_input_tokens",0)
    return dict(turns=turns,model=model,input=inp,cache_write=cw,cache_read=cr,output=out,first_ctx=ctx0,images=imgs,result_chars=res_chars["text"],tools=dict(tools.most_common(8)))
PRICES={"sonnet":(3,3.75,0.30,15),"haiku":(1,1.25,0.10,5),"opus":(5,6.25,0.50,25)}
def cost(r):
    k=next((k for k in PRICES if k in (r["model"] or "")),"sonnet"); p=PRICES[k]
    return (r["input"]*p[0]+r["cache_write"]*p[1]+r["cache_read"]*p[2]+r["output"]*p[3])/1e6
if __name__=="__main__":
    tot=collections.Counter()
    for d in sys.argv[1:]:
        for f in sorted(glob.glob(os.path.join(d,"*.jsonl"))):
            r=analyze(f); c=cost(r)
            print(f"{os.path.basename(d)[-12:]:12} turns={r['turns']:3} ctx0={r['first_ctx']:6} in={r['input']:6} cw={r['cache_write']:7} cr={r['cache_read']:8} out={r['output']:6} imgs={r['images']:2} txt={r['result_chars']:6} ~${c:.3f} {r['model']} {r['tools']}")
            for k in ("turns","input","cache_write","cache_read","output","images","result_chars"): tot[k]+=r[k]
            tot["cost"]+=c
    print("TOTAL",dict(tot))
