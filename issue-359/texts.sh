#!/bin/sh
# QA of #359, checks 6 and 7: each client's line on watching in its instructions, and what its wait and send say of a loop and of the background
for c in claude-code claude-desktop sbx; do
  qa-mcp texts -- hq-mcp $c > ../out/texts-$c-after.txt
  echo "== hq mcp $c: the instructions' line on watching"
  grep -o -- "- To watch agents.*" ../out/texts-$c-after.txt | head -1 | fold -s -w 150
  for f in before after; do
    printf '   %-6s "loop": %s, "moved to the background": %s, wait "%s"\n' $f \
      "$(grep -o 'loop' ../out/texts-$c-$f.txt | wc -l)" "$(grep -o 'moved to the background' ../out/texts-$c-$f.txt | wc -l)" \
      "$(grep -o 'After timeout seconds ([^)]*)' ../out/texts-$c-$f.txt)"
  done
done
