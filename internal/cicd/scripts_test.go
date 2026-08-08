package cicd_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The scripts under scripts/ci decide what publish.yml publishes and whether
// notify.yml pages anyone. Neither workflow can be exercised from the pull
// request that introduces it - `workflow_run` reads its trigger configuration
// from the copy of the file on the default branch - so this table is the only
// pre-merge evidence that either decision is right. Treat a failure here as a
// failure of the pipeline, not of a test.

const (
	// A plausible full commit SHA, and a different one. The scripts compare
	// these for equality; nothing parses them.
	shaA = "6c1f0e6b1e0c4a2e8f3d9b7a5c4e2d1f0a9b8c7d"
	shaB = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"
)

// run executes a script with exactly the environment given (plus PATH, which
// bash itself needs) and returns its key=value output as a map.
func run(t *testing.T, script string, env map[string]string) map[string]string {
	t.Helper()

	path := filepath.Join("..", "..", "scripts", "ci", script)

	// Read the script before running it, and throw the contents away. This is
	// not redundant: `go test` caches results, and it decides whether a cached
	// result is still valid from the files the test binary itself opened. A
	// script executed by a child process is invisible to that, so without this
	// read, editing a script and re-running `go test ./...` reports a cached
	// PASS from before the edit. I hit exactly that while checking these tests
	// could fail at all.
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	cmd := exec.Command("bash", path)

	// Start from an empty environment rather than the test process's, so a
	// stray GITHUB_REF in a developer's shell - or in the Actions runner that
	// runs this very test - cannot change the answer.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", script, err, out)
	}

	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("%s emitted a line that is not key=value: %q", script, line)
		}
		// $GITHUB_OUTPUT needs heredoc syntax for multiline values, so a value
		// containing a newline would silently corrupt the file the workflow
		// appends this to. Cut() already proves there is no newline inside a
		// line; this proves no value smuggled one in some other way.
		if strings.ContainsAny(v, "\n\r") {
			t.Fatalf("%s emitted a multiline value for %q", script, k)
		}
		got[k] = v
	}
	return got
}

func assertOutputs(t *testing.T, script string, got, want map[string]string) {
	t.Helper()
	for k, w := range want {
		if g, ok := got[k]; !ok {
			t.Errorf("%s: no output named %q (got %v)", script, k, got)
		} else if g != w {
			t.Errorf("%s: %s = %q, want %q", script, k, g, w)
		}
	}
}

func TestPublishPlan(t *testing.T) {
	// Every case sets REPOSITORY, because the image path is computed on every
	// path including the ones that publish nothing.
	const repo = "robert-crandall/3d-library"
	const image = "ghcr.io/robert-crandall/3d-library"

	cases := []struct {
		name string
		env  map[string]string
		want map[string]string
	}{
		{
			// The ordinary case: someone merged, CI went green, main has not
			// moved since.
			name: "workflow_run/fresh main publishes and promotes",
			env: map[string]string{
				"EVENT_NAME": "workflow_run", "REPOSITORY": repo,
				"RUN_EVENT": "push", "RUN_CONCLUSION": "success",
				"RUN_HEAD_SHA": shaA, "BRANCH_TIP": shaA,
			},
			want: map[string]string{
				"publish": "true", "promote_main": "true",
				"ref": shaA, "image": image, "sha_tag": "sha-" + shaA, "version": "",
			},
		},
		{
			// Issue #8's first hazard. Two merges land back to back; this run
			// belongs to the older one. Publishing it would leave older code
			// sitting on :main with nothing to correct it.
			name: "workflow_run/stale main refuses",
			env: map[string]string{
				"EVENT_NAME": "workflow_run", "REPOSITORY": repo,
				"RUN_EVENT": "push", "RUN_CONCLUSION": "success",
				"RUN_HEAD_SHA": shaA, "BRANCH_TIP": shaB,
			},
			want: map[string]string{"publish": "false", "promote_main": "false", "ref": ""},
		},
		{
			name: "workflow_run/failed CI refuses",
			env: map[string]string{
				"EVENT_NAME": "workflow_run", "REPOSITORY": repo,
				"RUN_EVENT": "push", "RUN_CONCLUSION": "failure",
				"RUN_HEAD_SHA": shaA, "BRANCH_TIP": shaA,
			},
			want: map[string]string{"publish": "false", "promote_main": "false"},
		},
		{
			name: "workflow_run/cancelled CI refuses",
			env: map[string]string{
				"EVENT_NAME": "workflow_run", "REPOSITORY": repo,
				"RUN_EVENT": "push", "RUN_CONCLUSION": "cancelled",
				"RUN_HEAD_SHA": shaA, "BRANCH_TIP": shaA,
			},
			want: map[string]string{"publish": "false", "promote_main": "false"},
		},
		{
			// A pull_request run's head_sha is the PR branch's tip commit, not
			// a merge commit - so it CAN equal the branch tip, which is why
			// this case deliberately sets them equal. The event check is the
			// only thing refusing here, and that is the point: open a PR from
			// a branch pointing at main and everything else passes.
			name: "workflow_run/pull_request CI refuses",
			env: map[string]string{
				"EVENT_NAME": "workflow_run", "REPOSITORY": repo,
				"RUN_EVENT": "pull_request", "RUN_CONCLUSION": "success",
				"RUN_HEAD_SHA": shaA, "BRANCH_TIP": shaA,
			},
			want: map[string]string{"publish": "false", "promote_main": "false"},
		},
		{
			// If the branch-tip lookup fails and yields an empty string, an
			// equality check alone would compare "" to "" and happily publish
			// nothing-in-particular. The -n guard is what stops that.
			name: "workflow_run/empty tip and head refuses",
			env: map[string]string{
				"EVENT_NAME": "workflow_run", "REPOSITORY": repo,
				"RUN_EVENT": "push", "RUN_CONCLUSION": "success",
				"RUN_HEAD_SHA": "", "BRANCH_TIP": "",
			},
			want: map[string]string{"publish": "false", "promote_main": "false"},
		},
		{
			// A release tag publishes its own immutable tags and deliberately
			// leaves :main alone - the mutable tag did not move, so there is
			// nothing new for the homelab to pull.
			name: "push/version tag publishes without promoting",
			env: map[string]string{
				"EVENT_NAME": "push", "REPOSITORY": repo,
				"GITHUB_REF": "refs/tags/v1.2.3", "SHA": shaA,
			},
			want: map[string]string{
				"publish": "true", "promote_main": "false",
				"ref": shaA, "sha_tag": "", "version": "v1.2.3",
			},
		},
		{
			name: "push/non-tag ref refuses",
			env: map[string]string{
				"EVENT_NAME": "push", "REPOSITORY": repo,
				"GITHUB_REF": "refs/heads/main", "SHA": shaA,
			},
			want: map[string]string{"publish": "false", "promote_main": "false", "version": ""},
		},
		{
			// The Dependabot path. A GITHUB_TOKEN merge creates no workflow run,
			// so auto-merge dispatches this workflow explicitly.
			name: "workflow_dispatch/on main publishes and promotes",
			env: map[string]string{
				"EVENT_NAME": "workflow_dispatch", "REPOSITORY": repo,
				"GITHUB_REF": "refs/heads/main", "SHA": shaA,
			},
			want: map[string]string{
				"publish": "true", "promote_main": "true",
				"ref": shaA, "sha_tag": "sha-" + shaA,
			},
		},
		{
			// Anyone with write access can dispatch any workflow against any ref
			// from the Actions UI. Without this refusal, that would move :main
			// to unreviewed code.
			name: "workflow_dispatch/on a feature branch refuses",
			env: map[string]string{
				"EVENT_NAME": "workflow_dispatch", "REPOSITORY": repo,
				"GITHUB_REF": "refs/heads/some-feature", "SHA": shaA,
			},
			want: map[string]string{"publish": "false", "promote_main": "false", "ref": ""},
		},
		{
			// github.repository preserves the case the owner typed and GHCR
			// rejects uppercase references. For a template this is the first
			// thing a fork would hit, in a workflow nobody has read yet.
			name: "image path is lowercased",
			env: map[string]string{
				"EVENT_NAME": "workflow_run", "REPOSITORY": "Bob/MyApp",
				"RUN_EVENT": "push", "RUN_CONCLUSION": "success",
				"RUN_HEAD_SHA": shaA, "BRANCH_TIP": shaA,
			},
			want: map[string]string{"publish": "true", "image": "ghcr.io/bob/myapp"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, "publish-plan.sh", tc.env)
			assertOutputs(t, "publish-plan.sh", got, tc.want)
			assertPlanInvariants(t, got)
		})
	}
}

// assertPlanInvariants runs against the output of every publish-plan *test case*,
// so an arm that has a test case has to satisfy the workflow's contract and not
// merely its own expected outputs. It binds cases, not arms: an arm added with no
// test case is covered by nothing here, same as any other untested code. Two
// booleans gating three jobs is more state than the `if:` expressions make
// obvious, and each of these four properties is load-bearing for a specific line
// of publish.yml.
func assertPlanInvariants(t *testing.T, got map[string]string) {
	t.Helper()

	// `promote` moves the mutable tag onto an image `build` pushed. Today it is
	// also protected by `needs: [guard, build]` with no status function in its
	// `if:`, which means a skipped or failed build already skips it. But that is
	// a property of the YAML, not of the plan, and it would evaporate the moment
	// someone wrote `if: always() && ...`. Refuse to emit the combination at all.
	if got["promote_main"] == "true" && got["publish"] != "true" {
		t.Errorf("promote_main=true with publish=%q: would move :main to an image no job pushed", got["publish"])
	}

	// `build` checks out `needs.guard.outputs.ref`. actions/checkout treats an
	// empty `ref` as "whatever triggered the run" - and under workflow_run that
	// is the default branch tip, not the commit CI validated. An empty ref here
	// does not fail; it silently builds and publishes the wrong commit.
	if got["publish"] == "true" && got["ref"] == "" {
		t.Error("publish=true with an empty ref: checkout would silently fall back to the default branch")
	}

	// The tag is composed as `image:${{ version || sha_tag }}`. With both empty
	// that renders `ghcr.io/owner/repo:`, an invalid reference.
	if got["publish"] == "true" && got["version"] == "" && got["sha_tag"] == "" {
		t.Error("publish=true with neither version nor sha_tag: the computed tag would be `image:`")
	}

	// `promote` copies from `$IMAGE:$SHA_TAG`, so the mutable-tag path needs the
	// immutable tag by name - the version path never enters that job.
	if got["promote_main"] == "true" && got["sha_tag"] == "" {
		t.Error("promote_main=true with an empty sha_tag: promote has no source image to copy from")
	}
}

func TestPublishPlanUsesTheFullSHA(t *testing.T) {
	// An abbreviated SHA can collide, and a colliding "immutable" tag is worse
	// than none: it is one that lies. Assert the whole thing survives, so a
	// future "tidier" short tag has to argue with a test.
	got := run(t, "publish-plan.sh", map[string]string{
		"EVENT_NAME": "workflow_run", "REPOSITORY": "o/r",
		"RUN_EVENT": "push", "RUN_CONCLUSION": "success",
		"RUN_HEAD_SHA": shaA, "BRANCH_TIP": shaA,
	})
	if want := "sha-" + shaA; got["sha_tag"] != want {
		t.Errorf("sha_tag = %q, want %q (all 40 characters)", got["sha_tag"], want)
	}
}

func TestNotifyDecision(t *testing.T) {
	cases := []struct {
		name     string
		workflow string
		event    string
		concl    string
		notify   string
	}{
		// CI on a push. ci.yml triggers on `push: branches: [main]`, so
		// "was a push" is the "was main" check.
		{"CI/push/success", "CI", "push", "success", "false"},
		{"CI/push/failure", "CI", "push", "failure", "true"},
		{"CI/push/timed_out", "CI", "push", "timed_out", "true"},
		{"CI/push/startup_failure", "CI", "push", "startup_failure", "true"},
		// A cancelled run is somebody pressing the button, or a superseded run
		// in a concurrency group. Never an outage, never a page.
		{"CI/push/cancelled", "CI", "push", "cancelled", "false"},

		// CI on a pull request. Criterion 4 says pull requests page nobody, and
		// this is also what keeps a fork PR opened from a branch literally named
		// `main` from waking someone up.
		{"CI/pull_request/success", "CI", "pull_request", "success", "false"},
		{"CI/pull_request/failure", "CI", "pull_request", "failure", "false"},
		{"CI/pull_request/timed_out", "CI", "pull_request", "timed_out", "false"},
		{"CI/pull_request/startup_failure", "CI", "pull_request", "startup_failure", "false"},
		{"CI/pull_request/cancelled", "CI", "pull_request", "cancelled", "false"},

		// Publish triggered by CI. Its event is `workflow_run`, not `push` - a
		// push-only gate would silence every publish failure on main.
		{"Publish/workflow_run/success", "Publish", "workflow_run", "success", "false"},
		{"Publish/workflow_run/failure", "Publish", "workflow_run", "failure", "true"},
		// Measured on robert-crandall-org/peptide-tracker run 29518897699: a
		// guarded-out publish leaves the wrapper workflow green, so a publish
		// this repo deliberately declined to make is silent by construction.
		{"Publish/workflow_run/skipped", "Publish", "workflow_run", "skipped", "false"},
		{"Publish/workflow_run/cancelled", "Publish", "workflow_run", "cancelled", "false"},

		// Publish dispatched by dependabot-auto-merge.yml. This is the row a
		// push-only gate drops silently, and it is the worst one to drop: a
		// dependency update that merged itself and then failed to publish, with
		// nobody watching because nobody opened the pull request.
		{"Publish/workflow_dispatch/success", "Publish", "workflow_dispatch", "success", "false"},
		{"Publish/workflow_dispatch/failure", "Publish", "workflow_dispatch", "failure", "true"},
		{"Publish/workflow_dispatch/skipped", "Publish", "workflow_dispatch", "skipped", "false"},
		{"Publish/workflow_dispatch/cancelled", "Publish", "workflow_dispatch", "cancelled", "false"},

		// Publish from a version tag.
		{"Publish/push/success", "Publish", "push", "success", "false"},
		{"Publish/push/failure", "Publish", "push", "failure", "true"},
		{"Publish/push/skipped", "Publish", "push", "skipped", "false"},
		{"Publish/push/cancelled", "Publish", "push", "cancelled", "false"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, "notify-decision.sh", map[string]string{
				"WORKFLOW_NAME": tc.workflow, "RUN_EVENT": tc.event, "RUN_CONCLUSION": tc.concl,
			})
			assertOutputs(t, "notify-decision.sh", got, map[string]string{"notify": tc.notify})
		})
	}
}
