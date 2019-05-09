#!/bin/sh
# This script updates the time zone files using the content of the Alpine tzdata apk
set -euo pipefail

log() {
	echo "$(date -Iseconds 2>/dev/null) $1"
}


TZPV=${TZPV:="/tmp/zoneinfo"}
mkdir -p -m 755 "$TZPV"


UPSTREAM_VERSION=$(apk -q --no-cache --no-progress info tzdata -d | awk -F- 'NR==1{print $2}')

if [ ! -f ${TZPV}/version ] || [ "$UPSTREAM_VERSION" != "$(cat ${TZPV}/version)" ]; then
	SCRATCH=$(mktemp -d)
	cd "$SCRATCH"
	apk -q --no-cache --no-progress fetch tzdata
	tar xzf tzdata*.apk
	cd usr/share/zoneinfo/
	tar cf zoneinfo.tar * # Add zone files to tarball
	mv -f zoneinfo.tar "$TZPV" # Copy tarball to TZPV
	cd "$TZPV"
	tar xf zoneinfo.tar # Extract tarball (overwrites existing files)
	rm -f zoneinfo.tar
	${SCRATCH}/usr/sbin/zic --version | awk '{print $3}' > version
	log "Local Time Zone database updated to version $(cat version) on $TZPV"
	rm -rf ${SCRATCH}
else
	log 'Local Time Zone database is up to date'
fi
