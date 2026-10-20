# Give agent huge's state files a Stop report of 17 MiB, as the hook would write it.
import glob, json
for f in glob.glob(".git/hq/agents/*.stop"):
    head, body = open(f, "rb").read().split(b"\n", 1)
    p = json.loads(body)
    if p["last_assistant_message"] == "Done: hello":
        p["last_assistant_message"] = "x" * (17 << 20)
        data = head + b"\n" + json.dumps(p).encode()
        for path in (f, f[:-len(".stop")]):
            open(path, "wb").write(data)
        print("wrote %d bytes to %s and its .stop" % (len(data), f[:-len(".stop")]))
