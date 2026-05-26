#!/usr/bin/env bash
set -eo pipefail

# go-mk-notice.sh announces go-makefile changes to a consumer on a real build,
# and, for a newly introduced gate or rule, auto-baselines only that slice so
# the consumer's existing code is grandfathered without a token. It then asks
# the consumer to review and commit the baseline.
#
# notices.txt format, one record per line, tab-separated:
#
#   id<TAB>directive<TAB>summary
#
# id        monotonic integer.
# directive "-" for an announcement only, or a space-separated set of KEY=VALUE
#           tokens describing an auto-baseline scope, for example
#           "GATE=golangci LINTER=revive RULE=file-length-limit".
# summary   human text printed once per consumer.
#
# Applied state lives in a committed file (.go-mk-applied-notices) so a fresh CI
# checkout knows a scope was already rolled out and does not re-grandfather new
# violations. The gitignored .make/.go-mk-notice-seen only dedupes the printed
# summary, which is cosmetic.

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "${SCRIPT_DIR}/go-mk-common.sh"

notices_file="${GO_MK_NOTICES_FILE:-.make/notices.txt}"
applied_file="${GO_MK_APPLIED_NOTICES:-.go-mk-applied-notices}"
seen_file=".make/.go-mk-notice-seen"
golangci_baseline="${GOLANGCI_LINT_BASELINE:-.golangci-lint-baseline.txt}"

applied_ids=""

notice_is_applied() {
    local notice_id

    notice_id="$1"
    [[ -n "${applied_ids}" ]] || return 1
    printf "%s\n" "${applied_ids}" | grep -qx "${notice_id}"
}

notice_run_auto_baseline() {
    local notice_id
    local directive
    local token
    local gate
    local linter
    local rule
    local pattern

    notice_id="$1"
    directive="$2"
    gate="golangci"
    linter=""
    rule=""
    pattern=""
    for token in ${directive}; do
        case "${token}" in
            GATE=*) gate="${token#GATE=}" ;;
            LINTER=*) linter="${token#LINTER=}" ;;
            RULE=*) rule="${token#RULE=}" ;;
            PATTERN=*) pattern="${token#PATTERN=}" ;;
        esac
    done

    if [[ "${gate}" != "golangci" ]]; then
        printf "go-makefile notice #%s: unsupported auto-baseline gate '%s'; skipping\n" "${notice_id}" "${gate}" >&2
        return 0
    fi

    printf "go-makefile notice #%s: auto-baselining existing findings for %s\n" "${notice_id}" "${directive}" >&2
    if LINTER="${linter}" RULE="${rule}" GOLANGCI_LINT_BASELINE_SCOPE_PATTERN="${pattern}" \
        bash "${SCRIPT_DIR}/go-mk-baseline.sh" auto-baseline-scope >&2; then
        printf "%s\n" "${notice_id}" >> "${applied_file}"
        applied_ids=$(printf "%s\n%s\n" "${applied_ids}" "${notice_id}")
        printf "go-makefile notice #%s: wrote %s. Review with 'git diff %s' and commit it together with %s.\n" \
            "${notice_id}" "${golangci_baseline}" "${golangci_baseline}" "${applied_file}" >&2
    else
        printf "go-makefile notice #%s: auto-baseline failed; run it manually with the scoped baseline target\n" "${notice_id}" >&2
    fi
}

if [[ ! -f "${notices_file}" ]]; then
    exit 0
fi

if [[ -f "${applied_file}" ]]; then
    applied_ids=$(cat "${applied_file}")
fi

last_seen=0
if [[ -f "${seen_file}" ]]; then
    last_seen=$(cat "${seen_file}" || printf "0")
fi
max_seen="${last_seen}"

while IFS=$'\t' read -r notice_id directive summary; do
    case "${notice_id}" in
        "" | \#*)
            continue
            ;;
    esac
    if [[ -n "${directive}" && "${directive}" != "-" ]] && ! notice_is_applied "${notice_id}"; then
        notice_run_auto_baseline "${notice_id}" "${directive}"
    fi
    if [[ "${notice_id}" -gt "${last_seen}" ]]; then
        printf "go-makefile notice #%s: %s\n" "${notice_id}" "${summary}" >&2
    fi
    if [[ "${notice_id}" -gt "${max_seen}" ]]; then
        max_seen="${notice_id}"
    fi
done < "${notices_file}"

mkdir -p .make
printf "%s\n" "${max_seen}" > "${seen_file}"
