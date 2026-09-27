# 0003 - no row numbers, selection by name

Under attention sort rows reshuffle on every state change, so `1-9` shortcuts would fire on the wrong agent. Rows are selected with arrows or `/name` (prefix match on the same names as `hq go`), which is stable across re-sorts. `/` is required so names do not collide with single-letter keys.
