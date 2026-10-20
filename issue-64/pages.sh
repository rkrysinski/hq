# Follow hq read's "next" hints: each page's header, its first and last line.
cmd="hq read big"
while [ -n "$cmd" ]; do
  echo "\$ $cmd"
  out=$($cmd)
  echo "$out" | grep -E '^reply'
  echo "$out" | sed -n '/^reply/{n;p}'
  echo "  ..."
  echo "$out" | grep -v '^next' | grep -v '^$' | tail -1
  cmd=$(echo "$out" | sed -n 's/^next  *//p')
  echo
done
