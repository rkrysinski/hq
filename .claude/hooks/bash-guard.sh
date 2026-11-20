#!/bin/sh
# Claude Code PreToolUse hook on the Bash tool, registered in
# .claude/settings.json: refuses a command shaped to keep a core of a shared
# machine busy, before it runs.
#
# Refuses
#   - a here-document whose delimiter is unquoted (<<EOF, <<-EOF) and whose
#     body holds a backtick or $( that no backslash escapes: the shell runs
#     each one as a command before the command starts, so text meant as data
#     (a pull request body, Markdown, a script for python3) runs instead
#   - a loop whose body is only `:` or `true` (do :; done): it runs its
#     condition again at once, without a pause, until something stops it
# The command is read as the shell reads it: a << or a loop inside quotes, a
# comment or a here-document's body is text; one inside $( ), backticks or the
# script given to sh -c or bash -c is shell code.
#
# Cases (tests/test_bash_guard.py):
#   python3 - <<EOF, a body with `date`                -> refused, names <<'EOF'
#   python3 - <<'EOF', the same body                   -> runs
#   git commit -m "$(cat <<EOF, a body with $(date)    -> refused
#   cat <<EOF, a body with $((a + b))                  -> runs: arithmetic runs no command
#   until grep -q Ready app.log; do :; done            -> refused, names a pause
#   until grep -q Ready app.log; do sleep 1; done      -> runs
#   gh pr comment 12 --body 'until x; do :; done'      -> runs
#
# Reads Claude Code's hook input (JSON) on stdin. Exits 2 with the reason and
# the safe form on stderr, which Claude Code shows the agent instead of running
# the command; exits 0 otherwise, also on input it cannot read and on an awk it
# cannot run on: a fault of the hook never blocks a command. .claude/settings.json
# runs it only when this file exists, since sh (dash) exits 2 on a missing
# script, which Claude Code would take for a refusal.
#
# awk is the one interpreter every stack has. It runs with LC_ALL=C and reads
# the command from an array of characters: in BWK awk (macOS) substr() measures
# the whole string on every call, which made a 200 KB command take 40 seconds.

tab=$(printf '\t')
findings=$(LC_ALL=C awk '
BEGIN {
    BACKSLASH = "\001"; SQ = "\047"; DQ = "\""
    PRINTABLE = " !\"#$%&\047()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"
    # The body of a loop that only runs `:` or `true`: do :; done.
    SPIN = "do[ \t\n;]+(:|true)[ \t]*[;\n][ \t\n;]*done([^A-Za-z0-9_]|$)"
    # What precedes the script of a shell: sh -c, bash -ec, /bin/bash -x -c.
    SHELL_C = "(^|[^A-Za-z0-9_./-])([A-Za-z0-9_./-]*/)?(ba|da|z|k)?sh([ \t]+-[A-Za-z]+)*[ \t]+-[A-Za-z]*c[ \t]*$"
    # How far each look reaches, in characters: a delimiter word (one longer is
    # an unclosed quote), a loop from its do to its done, and back from a quote
    # to the shell before it.
    LONGEST_WORD = 256
    LOOP_REACH = 256
    SHELL_REACH = 128
}
{ json = json sep $0; sep = "\n" }
END {
    s = command_of(json)
    n = split(s, ch, "")
    scan()
    if (spin) print "loop"
}

# The value of "command" in the hook input, its JSON escapes decoded; a
# backslash of the command becomes BACKSLASH, so no awk escaping can touch it.
function command_of(json,    rest) {
    if (!match(json, /"command"[ \t\r\n]*:[ \t\r\n]*"/)) return ""
    rest = substr(json, RSTART + RLENGTH)
    if (!match(rest, /^([^"\\]|\\.)*"/)) return ""
    rest = substr(rest, 1, RLENGTH - 1)
    gsub(/\\\\/, BACKSLASH, rest)
    while (match(rest, /\\u[0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f]/))
        rest = substr(rest, 1, RSTART - 1) escaped_char(substr(rest, RSTART + 2, 4)) substr(rest, RSTART + 6)
    gsub(/\\n/, "\n", rest)
    gsub(/\\t/, "\t", rest)
    gsub(/\\r/, "\r", rest)
    gsub(/\\[bf]/, "_", rest)
    gsub(/\\"/, "\"", rest)
    gsub(/\\\//, "/", rest)
    return rest
}

# The character of a \u escape as far as the shell cares: ASCII as itself,
# anything else as a letter.
function escaped_char(hex,    k, v) {
    hex = tolower(hex)
    for (k = 1; k <= 4; k++) v = v * 16 + index("0123456789abcdef", substr(hex, k, 1)) - 1
    if (v == 92) return BACKSLASH
    if (v == 9) return "\t"
    if (v == 10) return "\n"
    if (v >= 32 && v < 127) return substr(PRINTABLE, v - 31, 1)
    return "_"
}

# The command read as the shell reads it, one context on top of another: shell
# code ("code" at the top, "subst" inside $( ), "backtick"), a double-quoted
# string ("dq") and arithmetic ("arith"). Single quotes and comments are passed
# over, so only a << of shell code starts a here-document, and its body starts
# after the next newline of shell code.
function scan(    i, c, c2, t, q) {
    depth = 1
    ctx[1] = "code"
    i = 1
    while (i <= n) {
        c = ch[i]
        c2 = ch[i + 1]
        t = ctx[depth]
        if (c == BACKSLASH) i += 2
        else if (t == "arith") {
            if (c == "(") parens[depth]++
            else if (c == ")" && parens[depth] > 0) parens[depth]--
            else if (c == ")") { depth--; if (c2 == ")") i++ }
            i++
        }
        else if (c == "$" && c2 == "(") {
            if (ch[i + 2] == "(") { push("arith"); i += 3 }
            else { push("subst"); i += 2 }
        }
        else if (c == "`") {
            if (t == "backtick") depth--
            else push("backtick")
            i++
        }
        else if (t == "dq") {
            if (c == DQ) {
                if (spins_in_script(opened[depth], i)) spin = 1
                depth--
            }
            i++
        }
        else if (c == SQ) {
            q = i
            for (i++; i <= n && ch[i] != SQ; i++) ;
            if (spins_in_script(q, i)) spin = 1
            i++
        }
        else if (c == DQ) { push("dq"); opened[depth] = i; i++ }
        else if (c == "(" && c2 == "(") { push("arith"); i += 2 }
        else if (c == "(") { parens[depth]++; i++ }
        else if (c == ")") {
            if (parens[depth] > 0) parens[depth]--
            else if (t == "subst") depth--
            i++
        }
        else if (c == "#" && word_start(i)) { while (i <= n && ch[i] != "\n") i++ }
        else if (c == "<" && c2 == "<") i = (ch[i + 2] == "<") ? i + 3 : heredoc_operator(i + 2)
        else if (c == "\n" && pending) i = heredoc_bodies(i + 1)
        else {
            if (c == "d" && c2 == "o" && word_start(i) && spins_at(i)) spin = 1
            i++
        }
    }
}

function push(kind) {
    ctx[++depth] = kind
    parens[depth] = 0
}

function word_start(i) {
    return i == 1 || index(" \t\n;&|()", ch[i - 1]) > 0
}

# Whether the do at i begins the body of a loop that only runs `:` or `true`.
function spins_at(i,    k, window) {
    for (k = i; k <= n && k < i + LOOP_REACH; k++) window = window ch[k]
    return window ~ ("^" SPIN)
}

# Whether the quoted string with its quotes at a and b is the script of a shell
# (sh -c '...') holding such a loop: that script is shell code too.
function spins_in_script(a, b,    k, before) {
    for (k = a - 1; k > 0 && (ch[k] == " " || ch[k] == "\t"); k--) ;
    if (ch[k] != "c") return 0
    for (k = a > SHELL_REACH ? a - SHELL_REACH : 1; k < a; k++) before = before ch[k]
    return before ~ SHELL_C && substr(s, a + 1, b - a - 1) ~ ("(^|[ \t\n;&|(){}])" SPIN)
}

# The delimiter word after <<: any quoting in it (\x, a quoted part) keeps the
# body as written, and the quotes are not part of the delimiter. A word longer
# than any delimiter (an unclosed quote) starts nothing.
function heredoc_operator(j,    c, word, size, quoted, tabs) {
    if (ch[j] == "-") { tabs = 1; j++ }
    while (ch[j] == " " || ch[j] == "\t") j++
    for (size = 0; j <= n && size < LONGEST_WORD && !index(" \t\n;&|<>()", ch[j]); size++) {
        c = ch[j]
        if (c == BACKSLASH) {
            quoted = 1
            word = word ch[j + 1]
            j += 2
        } else if (c == SQ || c == DQ) {
            quoted = 1
            for (j++; j <= n && ch[j] != c && size < LONGEST_WORD; j++) { word = word ch[j]; size++ }
            j++
        } else {
            word = word c
            j++
        }
    }
    if (word != "" && size < LONGEST_WORD) {
        pending++
        delimiter[pending] = word
        literal[pending] = quoted
        strip_tabs[pending] = tabs
    }
    return j
}

# The bodies of the here-documents begun on the line just ended, one after
# another. An unquoted body runs each backtick and $( no backslash escapes, not
# $(( arithmetic )); a backslash before the newline joins the next line to it,
# as in the shell.
function heredoc_bodies(j,    k, end, runs) {
    for (k = 1; k <= pending; k++) {
        runs = 0
        while (j <= n) {
            end = terminator(j, k)
            if (end) { j = end; break }
            for (; j <= n && ch[j] != "\n"; j++) {
                if (literal[k]) continue
                if (ch[j] == BACKSLASH) j++
                else if (ch[j] == "`" || (ch[j] == "$" && ch[j + 1] == "(" && ch[j + 2] != "(")) runs = 1
            }
            j++
        }
        if (runs) print "heredoc\t" delimiter[k]
    }
    pending = 0
    return j
}

# Where the line at j ends, when it holds only the delimiter of here-document k
# (after its leading tabs, for <<-); 0 when it holds anything else.
function terminator(j, k,    m, size) {
    if (strip_tabs[k]) while (ch[j] == "\t") j++
    size = length(delimiter[k])
    for (m = 1; m <= size; m++) if (ch[j + m - 1] != substr(delimiter[k], m, 1)) return 0
    j += size
    if (j <= n && ch[j] != "\n") return 0
    return j + 1
}
') || exit 0
[ -n "$findings" ] || exit 0

printf '%s\n' "$findings" | while IFS=$tab read -r shape delimiter; do
    case $shape in
        heredoc)
            printf 'bash-guard: refused. The here-document <<%s has an unquoted delimiter, so the shell runs each backtick and $( in its body as a command before this command starts: text meant as data (a pull request body, Markdown, a script for python3) runs instead.\n' "$delimiter"
            printf "Quote the delimiter, <<'%s', and the body stays as written. A value the body needs is computed before the command and reaches it through the environment (head=\$(git rev-parse HEAD) python3 - <<'%s', then os.environ[\"head\"]), or as \$head in an unquoted body that holds no backtick or \$(.\n" "$delimiter" "$delimiter"
            ;;
        loop)
            printf 'bash-guard: refused. A loop whose body is only `:` or `true` (do :; done) runs its condition again at once, without a pause, and keeps a core of the machine busy until something stops it.\n'
            printf "Pause between tries and bound the wait, for example: timeout 120 sh -c 'until grep -q Ready app.log; do sleep 1; done'\n"
            ;;
    esac
done >&2
exit 2
