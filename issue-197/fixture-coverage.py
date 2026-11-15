import json, glob, os
used, kinds = {}, {}
for p in sorted(glob.glob('internal/state/testdata/journals/*.json')):
    fx = json.load(open(p)); n = os.path.basename(p)
    looks = sum(1 for s in fx['steps'] if s.get('look'))
    for s in fx['steps']:
        if s.get('screen'): used.setdefault(s['screen'], n)
        for e in s.get('events', []):
            if e['source'] != 'hook': kinds.setdefault(e['source'] + ' ' + e['kind'], set()).add(n[:-5])
    print(f"{n:26} {len(fx['steps']):3} steps, {looks:3} asserted")
screens = sorted(os.path.basename(p)[7:-4] for p in glob.glob('internal/state/testdata/screen-*.txt'))
print(f"screen fixtures in journals: {sum(s in used for s in screens)} of {len(screens)}; not in any: {[s for s in screens if s not in used]}")
for k in sorted(kinds): print(f"  {k:22} {', '.join(sorted(kinds[k]))}")
