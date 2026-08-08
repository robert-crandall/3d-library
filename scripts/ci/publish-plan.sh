#!/usr/bin/env bash
#
# Decides what .github/workflows/publish.yml should publish, if anything.
#
# This is a shell script rather than a pile of YAML `if:` expressions for one
# reason: `workflow_run` reads its trigger configuration from the workflow file
# on the DEFAULT branch, so a publish workflow added in a pull request never
# fires from that pull request. None of this logic can be exercised before it is
# merged. Pulling it out here makes it a pure function of its environment, which
# internal/cicd/scripts_test.go covers with a table.
#
# Contract: read env, write single-line `key=value` pairs to stdout. No network,
# no git, no gh - the caller resolves the branch tip and passes it in as
# BRANCH_TIP. The workflow appends this stdout to $GITHUB_OUTPUT, which is why
# every value must be a single line: multiline values need heredoc syntax there,
# and a stray newline silently corrupts the whole file.
#
# Inputs:
#   EVENT_NAME     github.event_name: workflow_run | push | workflow_dispatch
#   REPOSITORY     github.repository, e.g. Bob/MyApp
#   GITHUB_REF     github.ref (push and workflow_dispatch paths)
#   SHA            github.sha (push and workflow_dispatch paths)
#   RUN_EVENT      github.event.workflow_run.event
#   RUN_CONCLUSION github.event.workflow_run.conclusion
#   RUN_HEAD_SHA   github.event.workflow_run.head_sha
#   BRANCH_TIP     the current tip of the default branch
#
# Outputs:
#   publish       true | false - build and push the immutable tag
#   promote_main  true | false - move the mutable :main tag afterwards
#   ref           the commit to check out and build
#   image         the lowercased GHCR path
#   sha_tag       the immutable tag
#   version       the version tag, or empty
set -euo pipefail

EVENT_NAME="${EVENT_NAME:-}"
REPOSITORY="${REPOSITORY:-}"
GITHUB_REF="${GITHUB_REF:-}"
SHA="${SHA:-}"
RUN_EVENT="${RUN_EVENT:-}"
RUN_CONCLUSION="${RUN_CONCLUSION:-}"
RUN_HEAD_SHA="${RUN_HEAD_SHA:-}"
BRANCH_TIP="${BRANCH_TIP:-}"

# ${{ github.repository }} preserves the case the owner typed, and GHCR rejects
# uppercase references outright. For a template that is not a hypothetical: the
# first person to fork this into `Bob/MyApp` would get `invalid reference
# format` on their very first merge, in a workflow they have never read.
image=$(printf '%s' "ghcr.io/${REPOSITORY}" | tr '[:upper:]' '[:lower:]')

publish=false
promote_main=false
ref=
sha_tag=
version=

case "$EVENT_NAME" in
push)
	# Version tags only. The workflow's `push` trigger is filtered to `v*`, so
	# anything else arriving here is a misconfiguration rather than a case to
	# handle.
	if [[ "$GITHUB_REF" == refs/tags/v* ]]; then
		version="${GITHUB_REF#refs/tags/}"
		publish=true
		# github.sha, not github.event.after. On an ANNOTATED tag push those
		# differ: `after` is the tag *object* sha, which is not a commit and
		# which actions/checkout cannot resolve. github.sha is dereferenced to
		# the commit. Measured, not assumed - an annotated probe tag reported
		# github.sha=cf4d877 (the commit) while payload.after=a139835 (the tag
		# object), and checking out github.sha worked. So don't "fix" this to
		# use `after`, and there's no need to switch to github.ref either.
		ref="$SHA"
		# No sha_tag: the release publishes :v1.2.3 and nothing else. That tag
		# is already immutable, so a second immutable tag for the same digest
		# would just be more surface for someone to have to keep in sync.
		#
		# No :main move and no Watchtower ping either: tagging a release does
		# not change what the mutable tag should point at, so there is nothing
		# new for the homelab to pull.
		promote_main=false
	fi
	;;

workflow_dispatch)
	# This trigger exists so dependabot-auto-merge.yml can kick off a publish
	# after it self-merges - a merge performed with GITHUB_TOKEN does not create
	# a workflow run, so the usual workflow_run chain never fires and the update
	# would sit on main forever without reaching the homelab. workflow_dispatch
	# is one of the two documented exceptions to that rule.
	#
	# But anyone with write access can dispatch any workflow against any ref from
	# the Actions UI, and a dispatch off a feature branch would otherwise move
	# :main to unreviewed code. Restrict it here rather than trusting the caller.
	if [[ "$GITHUB_REF" == "refs/heads/main" ]]; then
		publish=true
		promote_main=true
		ref="$SHA"
		sha_tag="sha-${SHA}"
	fi
	;;

workflow_run)
	# CI finished on main. Publish only if it finished green, only if it was a
	# real push, and only if main has not moved since.
	#
	# The event check is doing real work. A pull_request run's head_sha is the
	# PR branch's tip commit - measured: PR #24's CI run reported head_sha
	# cfb2e35, which is exactly the PR's headRefOid, single parent, sitting on
	# the branch. It is NOT a merge commit, so it can perfectly well equal
	# main's tip: open a PR from a branch that points at main and the tip
	# comparison below passes on its own. Without the event check that green PR
	# run would publish.
	#
	# The tip check is the other half. Two merges in quick succession start two
	# publish runs, and without it the older one can finish last and quietly
	# drag :main backwards onto older code.
	if [[ "$RUN_EVENT" == "push" && "$RUN_CONCLUSION" == "success" && "$RUN_HEAD_SHA" == "$BRANCH_TIP" && -n "$RUN_HEAD_SHA" ]]; then
		publish=true
		promote_main=true
		ref="$RUN_HEAD_SHA"
		sha_tag="sha-${RUN_HEAD_SHA}"
	fi
	;;
esac

# The tag carries the full 40-character SHA, not an abbreviation. A short SHA can
# collide, and a colliding "immutable" tag is worse than no immutable tag at all
# - it is one that lies. Nothing but machines read it, so the length costs
# nothing.

echo "publish=${publish}"
echo "promote_main=${promote_main}"
echo "ref=${ref}"
echo "image=${image}"
echo "sha_tag=${sha_tag}"
echo "version=${version}"
