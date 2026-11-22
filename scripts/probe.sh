#!/usr/bin/env bash
# End-to-end probe of a running spike: discovery, CRUD, ctag/sync-token,
# incremental sync-collection, PROPPATCH. Usage: ./probe.sh [base-url]
set -u
BASE=${1:-http://127.0.0.1:8080}
AUTH=alice:test
X='Content-Type: application/xml'
CAL=/dav/alice/calendars/default/
BOOK=/dav/alice/contacts/default/
pass=0 fail=0

check() { # name, haystack, needle
  if grep -qF -- "$3" <<<"$2"; then echo "PASS  $1"; pass=$((pass+1))
  else echo "FAIL  $1  (wanted: $3)"; echo "$2" | head -c 600; echo; fail=$((fail+1)); fi
}
req() { curl -s -u $AUTH -H "$X" "$@" -w '\nHTTP %{http_code}'; }
tok() { grep -o '<sync-token[^>]*>[^<]*' <<<"$1" | tail -1 | sed 's/.*>//'; }
ics() { printf 'BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//probe//EN\r\nBEGIN:VEVENT\r\nUID:%s\r\nDTSTAMP:20261122T100000Z\r\nDTSTART:20261201T100000Z\r\nSUMMARY:%s\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n' "$1" "$2"; }
sync_body() { echo "<D:sync-collection xmlns:D=\"DAV:\"><D:sync-token>$1</D:sync-token><D:sync-level>1</D:sync-level><D:prop><D:getetag/></D:prop></D:sync-collection>"; }
COLLPROPS='<propfind xmlns="DAV:" xmlns:CS="http://calendarserver.org/ns/"><prop><displayname/><CS:getctag/><sync-token/><supported-report-set/></prop></propfind>'

out=$(req -X PROPFIND -H 'Depth: 0' $BASE/dav/alice/ --data '<propfind xmlns="DAV:"><prop><C:calendar-home-set xmlns:C="urn:ietf:params:xml:ns:caldav"/><A:addressbook-home-set xmlns:A="urn:ietf:params:xml:ns:carddav"/></prop></propfind>')
check "principal has both home sets" "$out" "/dav/alice/contacts/"

out=$(req -X PROPFIND -H 'Depth: 0' $BASE$CAL --data "$COLLPROPS")
check "calendar advertises sync-collection" "$out" '<sync-collection xmlns="DAV:">'
check "calendar has getctag" "$out" '<getctag xmlns="http://calendarserver.org/ns/">https://nexus/ns/sync/' 
check "calendar has sync-token" "$out" "https://nexus/ns/sync/"

out=$(req -X REPORT -H 'Depth: 0' $BASE$CAL --data "$(sync_body '')")
check "initial sync returns token" "$out" "<sync-token>"; T0=$(tok "$out")

put_ics() { curl -s -o /dev/null -w '%{http_code}' -u $AUTH -X PUT -H 'Content-Type: text/calendar' $BASE$CAL$1.ics --data-binary @- < <(ics $1 $1); }
check "PUT a.ics" "$(put_ics a)" 201
check "PUT b.ics" "$(put_ics b)" 201
out=$(req -X REPORT -H 'Depth: 0' $BASE$CAL --data "$(sync_body "$T0")")
check "incremental sync sees a.ics" "$out" "${CAL}a.ics"
check "incremental sync sees b.ics" "$out" "${CAL}b.ics"; T1=$(tok "$out")

check "DELETE a.ics" "$(curl -s -o /dev/null -w '%{http_code}' -u $AUTH -X DELETE $BASE${CAL}a.ics)" 204
out=$(req -X REPORT -H 'Depth: 0' $BASE$CAL --data "$(sync_body "$T1")")
check "deleted a.ics reported as 404" "$out" "<href>${CAL}a.ics</href><status>HTTP/1.1 404 Not Found</status>"
if grep -qF "${CAL}b.ics" <<<"$out"; then echo "FAIL  unchanged b.ics re-sent"; fail=$((fail+1)); else echo "PASS  unchanged b.ics not re-sent"; pass=$((pass+1)); fi
T2=$(tok "$out")
out=$(req -X REPORT -H 'Depth: 0' $BASE$CAL --data "$(sync_body "$T2")")
if grep -q '<response' <<<"$out"; then echo "FAIL  no-op sync not empty"; fail=$((fail+1)); else echo "PASS  no-op sync is empty"; pass=$((pass+1)); fi

out=$(req -X REPORT -H 'Depth: 0' $BASE$CAL --data "$(sync_body 'https://nexus/ns/sync/999')")
check "bogus token -> 403 valid-sync-token" "$out" "valid-sync-token"

out=$(req -X PROPPATCH $BASE$CAL --data '<D:propertyupdate xmlns:D="DAV:" xmlns:I="http://apple.com/ns/ical/"><D:set><D:prop><D:displayname>Work</D:displayname><I:calendar-color>#ff0000</I:calendar-color></D:prop></D:set></D:propertyupdate>')
check "PROPPATCH calendar -> 200 propstat" "$out" "HTTP/1.1 200 OK"
out=$(req -X PROPFIND -H 'Depth: 0' $BASE$CAL --data '<propfind xmlns="DAV:"><prop><displayname/><I:calendar-color xmlns:I="http://apple.com/ns/ical/"/></prop></propfind>')
check "rename persisted" "$out" ">Work</displayname>"
check "colour persisted" "$out" ">#ff0000</calendar-color>"

out=$(req -X REPORT -H 'Depth: 0' $BASE$BOOK --data "$(sync_body '')"); B0=$(tok "$out")
curl -s -u $AUTH -X PUT -H 'Content-Type: text/vcard' $BASE${BOOK}c1.vcf --data-binary $'BEGIN:VCARD\r\nVERSION:3.0\r\nUID:c1\r\nFN:Ada Lovelace\r\nEND:VCARD\r\n' >/dev/null
out=$(req -X REPORT -H 'Depth: 0' $BASE$BOOK --data "$(sync_body "$B0")")
check "address book incremental sync sees c1.vcf" "$out" "${BOOK}c1.vcf"
out=$(req -X PROPFIND -H 'Depth: 0' $BASE$BOOK --data "$COLLPROPS")
check "address book has getctag" "$out" "<getctag xmlns=\"http://calendarserver.org/ns/\">https://nexus/ns/sync/1</getctag>"

echo "---- $pass passed, $fail failed"
[ $fail -eq 0 ]
