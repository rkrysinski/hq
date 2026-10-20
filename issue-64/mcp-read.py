# Drive hq mcp over stdio as Claude Desktop would: read big's reply page by page.
import glob, json, subprocess, sys
p = subprocess.Popen(["hq", "mcp"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
n = 0
def rpc(method, params, notify=False):
    global n
    msg = {"jsonrpc": "2.0", "method": method, "params": params}
    if not notify:
        n += 1
        msg["id"] = n
    p.stdin.write(json.dumps(msg) + "\n"); p.stdin.flush()
    if notify:
        return None
    while True:
        r = json.loads(p.stdout.readline())
        if r.get("id") == n:
            return r["result"]
rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "qa", "version": "0"}})
rpc("notifications/initialized", {}, notify=True)
args, joined, page = {"name": "big"}, "", 0
while True:
    page += 1
    res = rpc("tools/call", {"name": "read", "arguments": args})
    v = json.loads(res["content"][0]["text"])
    print("read %s -> page %d: %d bytes, reply_size %d, next_offset %s" % (json.dumps(args), page, len(v["reply"].encode()), v["reply_size"], json.dumps(v["next_offset"])))
    joined += v["reply"]
    if v["next_offset"] is None:
        break
    args = {"name": "big", "offset": v["next_offset"], "since": v["next_since"]}
f = glob.glob(sys.argv[1] + "/.git/hq/agents/*.stop")[0]
m = json.loads(open(f, "rb").read().split(b"\n", 1)[1])["last_assistant_message"]
print("joined pages %d characters, state file reply %d characters, identical: %s" % (len(joined), len(m), joined == m))
res = rpc("tools/call", {"name": "read", "arguments": {"name": "big", "offset": 32768}})
print("read {offset without since} -> isError %s: %s" % (res.get("isError"), res["content"][0]["text"].strip()))
p.stdin.close(); p.wait()
