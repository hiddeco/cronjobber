#!/bin/sh
# This script updates the time zone files using the content of the Alpine tzdata apk
set -euo pipefail

log() {
	echo "$(date -Iseconds 2>/dev/null) $1"
}


TZPV=${TZPV:="/tmp/zoneinfo"}
mkdir -p -m 755 "$TZPV"


UPSTREAM_VERSION=$(apk -q --no-cache --no-progress info tzdata -d | awk -F- 'NR==1{print $2}')

if [ ! -f ${TZPV}/version ] || [ "$UPSTREAM_VERSION" != "$(cat ${TZPV}/version 2>/dev/null)" ]; then
	SCRATCH=$(mktemp -d)
	cd "$SCRATCH"
	apk -q --no-cache --no-progress fetch tzdata
	tar xzf tzdata*.apk
	cd usr/share/zoneinfo/
	tar cf - ./* | tar moxf - -C "$TZPV" # Overwrites existing files
	"${SCRATCH}/usr/sbin/zic" --version | awk '{print $NF}' > "${TZPV}/version"
	log "Local Time Zone database updated to version $(cat ${TZPV}/version) on $TZPV"
	# Cleanup
	find "$TZPV" -mindepth 1 -mmin +5 -delete # Delete old files/folders
	rm -rf "$SCRATCH"
else
	log 'Local Time Zone database is up to date'
fi
