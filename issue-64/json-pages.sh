# Read big's reply through hq read --json pages and compare it with the state file's.
args=""; n=0; : > pages.txt
while :; do
  n=$((n+1)); hq read big --json $args > page.json
  python3 -I -c 'import json,sys;v=json.load(open("page.json"));print("page %d: %d bytes, reply_size %d, next_offset %s" % (int(sys.argv[1]),len(v["reply"].encode()),v["reply_size"],v["next_offset"]))' $n
  python3 -I -c 'import json;v=json.load(open("page.json"));open("pages.txt","a").write(v["reply"])'
  args=$(python3 -I -c 'import json;v=json.load(open("page.json"));print("" if v["next_offset"] is None else "--offset %d --since %s" % (v["next_offset"],v["next_since"]))')
  [ -z "$args" ] && break
done
python3 -I -c 'import json,glob;f=[p for p in glob.glob(".git/hq/agents/*.stop")][0];m=json.loads(open(f,"rb").read().split(b"\n",1)[1])["last_assistant_message"];r=open("pages.txt").read();print("state file reply %d characters; joined pages %d characters; identical: %s" % (len(m),len(r),m==r))'
rm -f page.json pages.txt
