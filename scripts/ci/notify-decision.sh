#!/usr/bin/env bash
#
# Decides whether .github/workflows/notify.yml should post to Slack.
#
# Same reasoning as publish-plan.sh: notify.yml is `workflow_run`-triggered, so
# it reads its configuration from the default branch and cannot be exercised
# from the pull request that adds it. Keeping the entire decision here - rather
# than half in a YAML `if:` and half in a step - is what makes it testable before
# it is merged. internal/cicd/scripts_test.go covers it over a table.
#
# Contract: read env, write single-line `key=value` pairs to stdout.
#
# Inputs:
#   WORKFLOW_NAME  github.event.workflow_run.name  - "CI" or "Publish"
#   RUN_EVENT      github.event.workflow_run.event
#   RUN_CONCLUSION github.event.workflow_run.conclusion
#
# Outputs:
#   notify  true | false
#   reason  a single word, for the run log
set -euo pipefail

WORKFLOW_NAME="${WORKFLOW_NAME:-}"
RUN_EVENT="${RUN_EVENT:-}"
RUN_CONCLUSION="${RUN_CONCLUSION:-}"

notify=false
reason=

case "$RUN_CONCLUSION" in
failure | timed_out | startup_failure)
	# These three are the ways a workflow can be broken. Everything else -
	# success, cancelled, skipped, neutral - is silence.
	#
	# `cancelled` in particular must never page. The promote job runs in a
	# concurrency group, so a superseded publish can legitimately end up
	# cancelled; that is the design working, not an outage.
	if [[ "$WORKFLOW_NAME" == "Publish" ]]; then
		# Publish is exempt from the push requirement below, and this is the
		# case a naive "only notify on push" gate silently drops. A Publish run's
		# event is `workflow_run` when CI triggered it and `workflow_dispatch`
		# when dependabot-auto-merge.yml dispatched it - it is never `push`
		# except on the version-tag path. A push-only gate would therefore
		# silence publish failures entirely, including the one case that most
		# needs a human: a dependency update that self-merged and then failed to
		# publish, where nobody is watching because nobody opened the PR.
		#
		# Publish can only run from a main push, a version tag, or a dispatch, so
		# there is no pull-request path to exclude here.
		notify=true
		reason=publish-failed
	elif [[ "$RUN_EVENT" == "push" ]]; then
		# ci.yml triggers on `push: branches: [main]` and `pull_request`, so
		# requiring a push IS the "only main" check - and it is the one that
		# holds even for a pull request opened from a fork branch that happens to
		# be named `main`, where a head_branch comparison would page someone for
		# somebody else's broken fork.
		notify=true
		reason=main-broken
	else
		reason=not-a-push
	fi
	;;
*)
	reason=not-a-failure
	;;
esac

echo "notify=${notify}"
echo "reason=${reason}"
