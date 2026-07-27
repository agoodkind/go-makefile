#!/usr/bin/env bash
# go-mk-bootstrap.sh: provision every go-makefile asset into .make.
#
# bootstrap.mk delegates here so fetch policy lives in a fetched file rather
# than in the copy each consumer commits. A policy change therefore ships to
# every consumer on its next run, with no consumer pull request.
#
# Provisioning is staged: one tarball extracts into a temp directory, every
# required asset is verified there, and only then are the files under .make
# replaced. Nothing is deleted before its replacement exists, so a failed or
# partial download leaves the previous assets exactly as they were.

set -euo pipefail

GO_MK_API_REPO="${GO_MK_API_REPO:-agoodkind/go-makefile}"
GO_MK_API_REF="${GO_MK_API_REF:-main}"
# Internal override, in the same category as GO_MK_API_REPO and GO_MK_API_REF.
# Tests point it at a local server; consumers never set it.
GO_MK_CODELOAD_BASE="${GO_MK_CODELOAD_BASE:-https://codeload.github.com}"
GO_MK_DEV_DIR="${GO_MK_DEV_DIR:-}"
GO_MK_MODULES="${GO_MK_MODULES:-}"

MAKE_DIR=".make"
# FETCH_MAX_TIME is a backstop, not the thing that decides how long a stall
# takes to fail: FETCH_SPEED_LIMIT/FETCH_SPEED_TIME below abort a stalled
# transfer in FETCH_SPEED_TIME seconds regardless of this value. 15 is about
# 6x a real cold codeload download (measured at 2.06-2.4s), leaving headroom
# for a slow hotel or roaming link on a transfer that is progressing, just
# pathologically slowly; it is not inherited from any prior budget.
FETCH_MAX_TIME=15
# FETCH_SPEED_LIMIT/FETCH_SPEED_TIME give curl a progress-based abort:
# --speed-limit bytes/sec sustained for less than --speed-time seconds
# aborts the transfer, rather than waiting for FETCH_MAX_TIME to elapse.
# Measured locally: a connection that stalls entirely (either before or
# partway through the body) now dies in ~3s instead of riding FETCH_MAX_TIME
# out, while a genuinely slow but progressing transfer (2 KB/s, well under
# FETCH_MAX_TIME's old budget) is not aborted, since its throughput stays
# above FETCH_SPEED_LIMIT.
FETCH_SPEED_LIMIT=1024
FETCH_SPEED_TIME=3
# FETCH_RETRY_MAX_TIME bounds the total time curl spends on --retry
# (including the delays between attempts), on top of the progress-based
# abort above. Measured locally against a server that never responds:
# --retry 3 with no ceiling costs ~18s (4 attempts x ~3s, since curl treats
# a speed-limit abort as a retriable transient error the same way it
# treated a --max-time expiry); --retry 3 --retry-max-time 4 costs ~8s (2
# attempts); no --retry at all costs ~3s (1 attempt). 4 keeps retries (a
# transient 503 still recovers in a few seconds) while capping the cascade
# at two attempts instead of leaving it unbounded.
FETCH_RETRY_MAX_TIME=4
STATE_PATH="${MAKE_DIR}/.go-mk-fetch-state"
VALIDATION_CONNECT_TIMEOUT=2
VALIDATION_MAX_TIME=3
# REUSE_WINDOW_SECONDS bounds offline reuse to a fixed hour from the last
# completed download. It does not slide: a successful probe (a 304) writes no
# state, so it cannot extend the window, and only a real provision() run
# resets the clock.
REUSE_WINDOW_SECONDS=3600

required_assets() {
    printf '%s\n' "go.mk"
    printf '%s\n' "golangci.yml"
    printf '%s\n' "notices.txt"
    printf '%s\n' "scripts/go-mk-fetch-one.sh"
    printf '%s\n' "scripts/go-mk-bin.sh"
    printf '%s\n' "scripts/go-mk-sync.sh"
    local module_name
    for module_name in ${GO_MK_MODULES}; do
        printf '%s\n' "${module_name}"
    done
}

# assets_complete requires each required asset to be a regular file with
# nonzero size. -s alone is true for a directory too (its apparent size is
# nonzero), so -f is required alongside it to reject a directory standing in
# for an asset.
assets_complete() {
    local base_dir="$1"
    local asset_name
    while IFS= read -r asset_name; do
        if [[ ! -f "${base_dir}/${asset_name}" || ! -s "${base_dir}/${asset_name}" ]]; then
            return 1
        fi
    done < <(required_assets)
    return 0
}

# install_one_asset copies a single verified asset from source_dir into .make,
# checking every step explicitly rather than relying on set -e. provision()
# runs this from a subshell used as an if-condition, and bash disables
# errexit for the entire duration of a command (including anything a
# subshell it spawns does) while that command's exit status is being tested
# by if/while/&&/||. A failed mkdir, cp, or chmod partway through the loop
# would otherwise be silently ignored, leaving install_from_stage's overall
# exit status determined only by whichever command the last loop iteration
# happened to run.
install_one_asset() {
    local source_path="$1"
    local target_path="$2"
    local target_dir

    target_dir="$(dirname "${target_path}")"
    if ! mkdir -p "${target_dir}"; then
        printf 'error: could not create %s\n' "${target_dir}" >&2
        return 1
    fi
    if ! cp "${source_path}" "${target_path}"; then
        printf 'error: could not install %s into %s\n' "${source_path}" "${target_path}" >&2
        return 1
    fi
    case "${target_path}" in
        *.sh)
            if ! chmod +x "${target_path}"; then
                printf 'error: could not mark %s executable\n' "${target_path}" >&2
                return 1
            fi
            ;;
    esac
    return 0
}

# install_from_stage copies each verified asset out of the staging tree into
# .make. It runs only after assets_complete succeeded against the stage, so a
# copy here always overwrites a file with known-good content. It stops and
# fails on the first asset that does not install cleanly.
install_from_stage() {
    local stage_dir="$1"
    local asset_name
    while IFS= read -r asset_name; do
        if ! install_one_asset "${stage_dir}/${asset_name}" "${MAKE_DIR}/${asset_name}"; then
            return 1
        fi
    done < <(required_assets)
    return 0
}

# install_from_dev_dir copies whatever required assets GO_MK_DEV_DIR provides.
# It intentionally skips an asset the dev dir does not carry, so a developer
# tree only needs to override the files under active work; the caller is
# responsible for checking assets_complete against MAKE_DIR afterward, since
# a dev dir that skips an asset .make does not already have would otherwise
# leave .make incomplete.
install_from_dev_dir() {
    local asset_name
    while IFS= read -r asset_name; do
        if [[ ! -f "${GO_MK_DEV_DIR}/${asset_name}" ]]; then
            continue
        fi
        if ! install_one_asset "${GO_MK_DEV_DIR}/${asset_name}" "${MAKE_DIR}/${asset_name}"; then
            return 1
        fi
    done < <(required_assets)
    return 0
}

current_epoch_seconds() {
    # EPOCHSECONDS is a bash 5 builtin, so the common path spawns no process.
    if [[ -n "${EPOCHSECONDS:-}" ]]; then
        printf '%s' "${EPOCHSECONDS}"
        return 0
    fi
    date +%s
}

# read_state_field looks up a single key from STATE_PATH. It returns 1 (with
# no output) when the state file is absent, empty, or does not carry the
# requested key, so a caller can tell "no prior state" apart from "state
# carried an empty value" without parsing errors of its own.
read_state_field() {
    local field_name="$1"
    local line
    if [[ ! -s "${STATE_PATH}" ]]; then
        return 1
    fi
    while IFS= read -r line; do
        if [[ "${line}" == "${field_name}="* ]]; then
            printf '%s' "${line#"${field_name}="}"
            return 0
        fi
    done < "${STATE_PATH}"
    return 1
}

# write_state records the ref, ETag, and current time after a completed
# download. It is only ever called from a successful provision, never from a
# 304 validation, so the reuse window a later task builds on this state runs
# from the last real transfer rather than sliding forward on every check.
write_state() {
    local etag_value="$1"
    {
        printf 'ref=%s\n' "${GO_MK_API_REF}"
        printf 'etag=%s\n' "${etag_value}"
        printf 'timestamp=%s\n' "$(current_epoch_seconds)"
    } > "${STATE_PATH}"
}

# validate_upstream sends one conditional HEAD request bounded by
# VALIDATION_CONNECT_TIMEOUT/VALIDATION_MAX_TIME rather than FETCH_MAX_TIME's
# full download budget. HEAD carries no body, so this budget is honest
# regardless of tarball size: a GET probe would otherwise download and
# discard the full tarball just to learn its status, doubling the transfer
# on every run whose upstream moved and risking a timeout on a slow link
# that a body-less HEAD would not hit. provision()'s own GET remains the
# only request that actually downloads. This prints the HTTP status code on
# stdout on success. On failure it returns one of two distinct codes rather
# than a single generic one, because the caller's reuse decision treats them
# differently:
#   1 - curl itself ran and failed to complete (a real network-level
#       failure: connection refused, timed out, or aborted for lack of
#       progress). The network was genuinely consulted and found wanting,
#       so this is eligible for bounded offline reuse.
#   2 - validate_upstream failed before any request was attempted (a local
#       setup failure, e.g. an unwritable or full TMPDIR). The network was
#       never consulted, so treating this the same as code 1 would let a
#       local environment problem masquerade as an upstream outage and
#       serve stale assets under a false pretense; the caller skips reuse
#       for this code and falls through to provision(), which fails loudly
#       with the real reason if the same local condition blocks it too.
# Either way, a curl failure here is not treated as fatal on its own: the
# caller falls back to a full provision(), which captures and reports its
# own curl error if that also fails, so the reason is never silently lost.
#
# header_args is expanded as ${header_args[@]+"${header_args[@]}"} rather
# than plain "${header_args[@]}" because bash 3.2 (still /bin/bash on stock
# macOS) raises "unbound variable" under set -u when a declared-but-empty
# array is expanded that way; the "+" alternate-value form only expands the
# array when it is set, which is empty-array-safe on every bash this script
# might run under.
validate_upstream() {
    local known_etag="$1"
    local status_code
    local curl_exit
    local stderr_path
    local -a header_args=()

    if [[ -n "${known_etag}" ]]; then
        header_args=(-H "If-None-Match: ${known_etag}")
    fi

    if ! stderr_path=$(mktemp "${TMPDIR:-/tmp}/go-mk-validate.XXXXXXXX"); then
        printf 'validate_upstream: could not create a temp file to capture curl stderr (TMPDIR=%s); no request was attempted\n' \
            "${TMPDIR:-/tmp}" >&2
        return 2
    fi

    status_code=$(curl -sS --head \
        --connect-timeout "${VALIDATION_CONNECT_TIMEOUT}" \
        --max-time "${VALIDATION_MAX_TIME}" \
        ${header_args[@]+"${header_args[@]}"} \
        -o /dev/null -w '%{http_code}' \
        "${GO_MK_CODELOAD_BASE}/${GO_MK_API_REPO}/tar.gz/${GO_MK_API_REF}" \
        2>"${stderr_path}")
    curl_exit=$?
    if [[ "${curl_exit}" -ne 0 ]]; then
        printf 'validate_upstream: curl exited %s, falling back to a full fetch: %s\n' \
            "${curl_exit}" "$(cat "${stderr_path}")" >&2
        rm -f "${stderr_path}"
        return 1
    fi
    rm -f "${stderr_path}"
    printf '%s' "${status_code}"
}

# state_is_recent reports whether the last completed download is inside
# REUSE_WINDOW_SECONDS. The window is fixed from that recorded timestamp
# rather than sliding, so it returns 1 (not recent) once the age exceeds the
# window, with no allowance for a successful probe to push it back out. A
# timestamp in the future, which a backwards clock produces, is not recent
# either: treating it as recent would let a clock fault grant unbounded
# offline reuse instead of forcing a real fetch.
state_is_recent() {
    local recorded
    local now
    local age

    if ! recorded=$(read_state_field "timestamp"); then
        return 1
    fi
    if [[ ! "${recorded}" =~ ^[0-9]+$ ]]; then
        return 1
    fi
    now=$(current_epoch_seconds)
    if (( recorded > now )); then
        return 1
    fi
    age=$(( now - recorded ))
    (( age <= REUSE_WINDOW_SECONDS ))
}

# format_age renders a whole number of seconds as a short, human-readable
# age for the warning serve_from_disk_with_warning prints.
format_age() {
    local seconds="$1"
    if (( seconds < 60 )); then
        printf '%ds' "${seconds}"
        return 0
    fi
    printf '%dm' "$(( seconds / 60 ))"
}

# serve_from_disk_with_warning announces the bounded-offline-reuse decision
# on stderr, naming how long ago the served assets were validated and which
# etag they were validated against, so a consumer can tell this run apart
# from a normal validated or freshly provisioned one without inspecting
# STATE_PATH directly. The timestamp read here carries its own fallback
# (unlike state_is_recent's read of the same field moments earlier) because
# it is not itself a control-flow condition: read_state_field failing here
# under set -e (e.g. the state file vanishing between that check and this
# call) would otherwise abort the whole run with no message, right as the
# script is trying to explain why it is degrading gracefully.
serve_from_disk_with_warning() {
    local recorded
    local now
    local etag_value
    local age_display

    recorded=$(read_state_field "timestamp" || printf '')
    etag_value=$(read_state_field "etag" || printf 'unknown')
    now=$(current_epoch_seconds)
    if [[ "${recorded}" =~ ^[0-9]+$ ]]; then
        age_display=$(format_age $(( now - recorded )))
    else
        age_display="an unknown time"
    fi
    printf 'go-makefile: upstream unreachable; serving .make assets validated %s ago (etag %s). Set GO_MK_SKIP_FETCH=1 to silence, or check network access to %s\n' \
        "${age_display}" "${etag_value}" "${GO_MK_CODELOAD_BASE}" >&2
}

# provision downloads, extracts into a stage, verifies, then installs.
#
# The staging work runs in a subshell rather than the function body itself. A
# RETURN trap set inside a shell function is not scoped to that function: it
# stays armed and fires again on the next function return anywhere in the
# script, by which point stage_root is out of scope and set -u aborts the
# whole run. A subshell's own EXIT trap only ever fires once, when that
# subshell exits, so cleanup cannot leak into unrelated control flow.
provision() {
    local stage_root
    local stage_dir
    local status_code
    local curl_exit
    local tar_exit
    local subshell_status
    local etag_value

    if ! stage_root=$(mktemp -d "${TMPDIR:-/tmp}/go-mk-stage.XXXXXXXX"); then
        printf 'error: could not create a staging directory (TMPDIR=%s): a local setup problem, not necessarily a network one\n' \
            "${TMPDIR:-/tmp}" >&2
        return 1
    fi

    (
        trap 'rm -rf "${stage_root}"' EXIT

        # Each probe's own stderr and exit code are captured and surfaced
        # rather than discarded, so a corrupt tarball, an HTTP error, and an
        # unreachable host are distinguishable instead of collapsing into one
        # generic message. Headers land in their own file (-D) so the ETag
        # comes from the same response that carried the body, rather than a
        # second request that could race a moving upstream.
        #
        # --speed-limit/--speed-time abort on lack of progress rather than
        # elapsed time: a connection that stalls (before or partway through
        # the body) dies in FETCH_SPEED_TIME seconds instead of riding
        # FETCH_MAX_TIME out, while a transfer that keeps moving above
        # FETCH_SPEED_LIMIT is never aborted no matter how long it takes.
        # FETCH_MAX_TIME is now only a backstop for a transfer that is
        # progressing but pathologically slowly. --retry stays (a transient
        # 503 still recovers in a few seconds), but curl treats a
        # speed-limit abort as a retriable transient error exactly the way
        # it treated a --max-time expiry, so --retry-max-time still caps how
        # many times a genuinely hung upstream gets retried instead of
        # leaving that cascade unbounded.
        #
        # --connect-timeout is 5 here, not the 2 the validation probe below
        # uses: connect time (DNS, TCP, TLS setup) has nothing to do with
        # stall detection, which --speed-limit/--speed-time already handle
        # once a connection exists. Squeezing it buys nothing and risks a
        # hard failure on a slow but working link (a roaming or hotel
        # connection can measure hundreds of milliseconds on DNS alone, and
        # a full TCP+TLS handshake can exceed 2s on a bad one) before it
        # ever gets the chance to retry into a working connection. The HEAD
        # probe can afford the tighter 2s because it is a cheap check with
        # its own fallback to a real fetch; this is the fetch itself.
        curl -sS --connect-timeout 5 --max-time "${FETCH_MAX_TIME}" \
            --speed-limit "${FETCH_SPEED_LIMIT}" --speed-time "${FETCH_SPEED_TIME}" \
            --retry 3 --retry-delay 2 --retry-max-time "${FETCH_RETRY_MAX_TIME}" \
            -D "${stage_root}/headers" \
            -o "${stage_root}/snapshot.tar.gz" -w '%{http_code}' \
            "${GO_MK_CODELOAD_BASE}/${GO_MK_API_REPO}/tar.gz/${GO_MK_API_REF}" \
            >"${stage_root}/status_code" 2>"${stage_root}/curl.stderr"
        curl_exit=$?
        if [[ "${curl_exit}" -ne 0 ]]; then
            printf 'error: curl exited %s fetching the tarball: %s\n' \
                "${curl_exit}" "$(cat "${stage_root}/curl.stderr")" >&2
            exit 1
        fi
        status_code=$(cat "${stage_root}/status_code")
        if [[ "${status_code}" != "200" ]]; then
            printf 'error: tarball fetch returned HTTP %s from %s\n' \
                "${status_code}" "${GO_MK_CODELOAD_BASE}" >&2
            exit 1
        fi

        stage_dir="${stage_root}/tree"
        mkdir -p "${stage_dir}"
        tar -xzf "${stage_root}/snapshot.tar.gz" -C "${stage_dir}" --strip-components 1 \
            2>"${stage_root}/tar.stderr"
        tar_exit=$?
        if [[ "${tar_exit}" -ne 0 ]]; then
            printf 'error: tar extract exited %s: %s\n' \
                "${tar_exit}" "$(cat "${stage_root}/tar.stderr")" >&2
            exit 1
        fi
        if ! assets_complete "${stage_dir}"; then
            printf 'error: staged tree is missing a required asset\n' >&2
            exit 1
        fi
        if ! install_from_stage "${stage_dir}"; then
            exit 1
        fi
        # Re-verify what actually landed in .make rather than trusting that
        # every install step reported success, as a second, independent check
        # alongside install_one_asset's own per-step error handling.
        if ! assets_complete "${MAKE_DIR}"; then
            printf 'error: .make is incomplete after install\n' >&2
            exit 1
        fi

        # A missing ETag header is degraded operation, not a provisioning
        # failure: the tree above this point is already verified and
        # installed, so failing here would trade a working (if unpinned)
        # engine for none at all on every consumer, the moment codeload ever
        # stops sending ETag on archive responses. Any stale state from a
        # previous run that did have an ETag is removed rather than left in
        # place, so a later run's known_etag genuinely reads empty (not a
        # now-unverifiable leftover) and goes straight to a fresh provision
        # rather than validating against content this run never confirmed.
        # The loud warning is what actually replaces the old hard failure:
        # every affected run says so, rather than silently reverting to
        # today's always-download behavior.
        etag_value=$(awk 'tolower($1) == "etag:" { print $2 }' "${stage_root}/headers" | tr -d '\r' | tail -n 1)
        if [[ -z "${etag_value}" ]]; then
            rm -f "${STATE_PATH}"
            printf 'go-makefile: upstream served no ETag header; installed the fetched tree but could not record validation state, so every run will download unconditionally until ETag returns. Check upstream (%s).\n' \
                "${GO_MK_CODELOAD_BASE}" >&2
            exit 0
        fi
        write_state "${etag_value}"
    )
    subshell_status=$?
    return "${subshell_status}"
}

main() {
    local known_etag=""
    local known_ref=""
    local status_code=""
    local probe_exit=0

    mkdir -p "${MAKE_DIR}"

    if [[ -n "${GO_MK_DEV_DIR}" ]]; then
        if ! install_from_dev_dir; then
            printf '%s\n' "error: could not install from GO_MK_DEV_DIR=${GO_MK_DEV_DIR}" >&2
            return 1
        fi
        if ! assets_complete "${MAKE_DIR}"; then
            printf '%s\n' "error: GO_MK_DEV_DIR=${GO_MK_DEV_DIR} does not provide every required asset" >&2
            return 1
        fi
        return 0
    fi

    if [[ "${GO_MK_SKIP_FETCH:-}" == "1" ]]; then
        if assets_complete "${MAKE_DIR}"; then
            return 0
        fi
        printf '%s\n' "error: GO_MK_SKIP_FETCH=1 but .make is missing a required asset" >&2
        return 1
    fi

    if assets_complete "${MAKE_DIR}"; then
        known_etag=$(read_state_field "etag" || printf '')
        known_ref=$(read_state_field "ref" || printf '')
        # A stored etag only means "nothing changed" for the ref it was
        # recorded against. A consumer that switches GO_MK_API_REF must not
        # reuse the previous ref's etag: a matching etag there would report
        # 304 without ever having fetched the new ref's content.
        if [[ "${known_ref}" != "${GO_MK_API_REF}" ]]; then
            known_etag=""
        fi
    fi

    if [[ -n "${known_etag}" ]]; then
        # Capturing both the substitution's stdout and validate_upstream's
        # exact exit code (rather than collapsing every failure to empty
        # status_code via `|| printf ''`) is what lets the branch below tell
        # a real network failure (code 1) apart from a local setup failure
        # (code 2, e.g. an unwritable TMPDIR) that never reached the network
        # at all.
        status_code=$(validate_upstream "${known_etag}") && probe_exit=0 || probe_exit=$?
        if [[ "${status_code}" == "304" ]]; then
            # Deliberately no state write. The reuse window a later task adds
            # runs from the last completed download, not from the last
            # successful check, so a 304 here leaves both the assets and the
            # recorded state exactly as they were.
            return 0
        fi
        # status_code is empty only when validate_upstream itself could not
        # complete, never for a real response: a completed non-304 response
        # means upstream is reachable, so that case falls through to
        # provision() below rather than reusing disk. Bounded offline reuse
        # applies only when the network was actually consulted and found
        # unreachable (probe_exit 1); a local setup failure (probe_exit 2)
        # never consulted the network, so it is not eligible for reuse and
        # falls through to provision() instead, which fails loudly with the
        # real reason if the same local condition blocks it too.
        if [[ -z "${status_code}" && "${probe_exit}" -eq 1 ]] && state_is_recent; then
            serve_from_disk_with_warning
            return 0
        fi
    fi

    if provision; then
        return 0
    fi

    printf '%s\n' "error: could not provision go-makefile assets. Set GO_MK_DEV_DIR, or check network access to ${GO_MK_CODELOAD_BASE}" >&2
    return 1
}

main "$@"
