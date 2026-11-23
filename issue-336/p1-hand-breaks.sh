#!/bin/sh
# Hand-made breaks, one at a time, each followed by the test of its rule;
# every file is restored after each. $1: a checkout of the commit.
cd "$1" || exit 1
brk() {
  name=$1; f=$2; from=$3; to=$4; pkg=$5; test=$6
  cp "$f" "$f.orig"
  python3 -c "import sys;p=sys.argv[1];s=open(p).read();assert s.count(sys.argv[2])==1;open(p,'w').write(s.replace(sys.argv[2],sys.argv[3]))" "$f" "$from" "$to"
  out=$(go test -count=1 -run "^$test\$" "$pkg" 2>&1)
  if echo "$out" | grep -q '^FAIL'; then v=failed; else v=PASSED; fi
  echo "$v  $name  ($test)"
  echo "$out" | grep -E '^\s+[a-z_]+_test.go:[0-9]+:' | head -2 | sed 's/^ */      /'
  mv "$f.orig" "$f"
}
L=internal/agent/looker.go
T=TestALookReadsTheCounterBeforeItListsTheWindows
brk "Docking reads the counter" $L 'Docking = Variant{sbx: sbxNotAsked}' 'Docking = Variant{sbx: sbxNotAsked, counts: true}' ./internal/agent $T
brk "Updating reads the counter" $L 'Updating = Variant{sbx: sbxRequired}' 'Updating = Variant{sbx: sbxRequired, counts: true}' ./internal/agent $T
brk "Explained reads no counter" $L 'Explained = Variant{sbx: sbxTolerated, again: true, counts: true}' 'Explained = Variant{sbx: sbxTolerated, again: true}' ./internal/agent $T
brk "Acting reads no counter" $L 'Acting = Variant{sbx: sbxAsked, counts: true}' 'Acting = Variant{sbx: sbxAsked}' ./internal/agent $T
brk "Shown reads no counter" $L 'Shown = Variant{sbx: sbxAsked, again: true, counts: true}' 'Shown = Variant{sbx: sbxAsked, again: true}' ./internal/agent $T
brk "every look reads the counter" $L '	if v.counts {
		n, err := k.Sources.Through()' '	if true {
		n, err := k.Sources.Through()' ./internal/agent $T
brk "the counter read after the windows" $L '	var c Counter
	if v.counts {
		n, err := k.Sources.Through()
		c = Counter{N: n, Read: err == nil}
	}
	ws, err := k.Windows()' '	ws, err := k.Windows()
	var c Counter
	if v.counts {
		n, err := k.Sources.Through()
		c = Counter{N: n, Read: err == nil}
	}' ./internal/agent $T
brk "S2: the counting sbx not put in place" internal/cli/look_variants_test.go '	d.sbx = askCounting{Sandboxes: f.sbx, f: f, asked: &asked}
	d.pollSandboxes = d.sbx.List' '	_ = askCounting{Sandboxes: f.sbx, f: f, asked: &asked}' ./internal/cli TestTheChordsAndDockingOnEntryAskSbxNothing
