# QA artifacts

QA evidence (screenshots, exported files, command output) recorded by agents for pull requests.
This branch never merges into `dev` or `main`. Evidence for an issue lives under `issue-<n>/`; PR bodies link to it
by commit SHA. See `docs/agents/qa-evidence.md` on `dev`.

The branch was rebuilt as a single commit on 2026-10-20 (#127): details of the private environments QA ran in
(local paths, Claude session ids, host, organisation and repository names) are replaced by placeholders in the
text files, and screenshots that showed them are removed. Links to the branch's earlier commits, and to the
removed screenshots, no longer resolve. Before publishing evidence, run `testgate private` on it (`docs/agents/testing.md`).
