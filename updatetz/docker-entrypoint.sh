#!/bin/sh
set -eu
log() {
	echo "$(date -Iseconds 2>/dev/null) $1"
}

retry() {
	local MAX_RETRIES=${EXPBACKOFF_MAX_RETRIES:-1000} # Max number of retries
	local BASE=${EXPBACKOFF_BASE:-1} # Base value for backoff calculation
	local MAX=${EXPBACKOFF_MAX:-300} # Max value [s] for backoff calculation
	local FAILURES=0
	while ! "$@"; do
		FAILURES=$(( FAILURES + 1 ))
		if [ "$FAILURES" -lt "$MAX_RETRIES" ]; then
			local SECONDS=$(( BASE * 2 ** (FAILURES - 1) ))
			if [ "$SECONDS" -gt "$MAX" ]; then
				SECONDS=$MAX
			fi
			log "$@" >&2
			log "$FAILURES failure(s), retrying in $SECONDS second(s)" >&2
			sleep "$SECONDS"
		else
			log "$@" >&2
			log "Failed, max retries exceeded" >&2
			return 1
		fi
	done
}

retry updatetz.sh
while true; do
	sleep "${REFRESH_INTERVAL:=7d}"
	retry updatetz.sh
done
