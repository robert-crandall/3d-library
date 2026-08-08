# Copilot instructions

**Read [`AGENTS.md`](../AGENTS.md) at the repo root first.** It has the setup,
the verified build/test/lint commands, the project layout, the conventions with
their exemplar files, and the gotchas. No command table is repeated here,
because two copies of one drift. The bullets below are Copilot-specific
emphasis, and the first deliberately restates the one rule that is most
expensive to break in a suggestion.

- **`docs/openapi.json` and `web/src/lib/api/schema.d.ts` are generated.** Never
  edit them by hand or suggest an edit to them. Change the route in
  `internal/app/routes.go`, run `make spec`, commit both.
- **Prefer deleting to adding.** This repo is a template someone will read
  end to end, so a smaller correct change beats a more defensive one. Add
  retries, caching, or fallback machinery only for a failure mode that is
  actually reachable here - and if you add one, say in the PR which scenario it
  is for.
- **Reviews: report bugs, regressions, security issues, and broken behaviour in
  the changed code.** Skip style, naming, comment wording, and "what if X"
  about failure modes the design has deliberately assumed away - those are
  documented in `docs/tech-stack.md`, so check there before flagging one.
- **PR descriptions say how the change was verified** - which command, which
  test, what was measured - not just what changed. A PR with nothing under that
  heading is not ready for review.
