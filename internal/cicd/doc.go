// Package cicd holds no code. It exists so the shell scripts under scripts/ci
// can be table-tested by `go test ./...`, which is what `make test` and CI's
// `go` job already run.
//
// Those scripts carry the decisions that .github/workflows/publish.yml and
// notify.yml make. They live in shell rather than in YAML `if:` expressions
// because a `workflow_run`-triggered workflow reads its configuration from the
// file on the default branch: a publish workflow added in a pull request never
// fires from that pull request, so none of this logic can be demonstrated
// end-to-end before it is merged. Pure functions of their environment can at
// least be demonstrated here.
package cicd
